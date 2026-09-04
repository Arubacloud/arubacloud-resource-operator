# Architecture

This is a **Kubebuilder v4** Kubernetes operator for managing Aruba Cloud infrastructure declaratively via CRDs.

## Terminology

**CMP** — Aruba Cloud Management Platform. The cloud control plane that this operator talks to in order to provision and manage resources. It is accessed programmatically via the [Aruba Go SDK](https://github.com/Arubacloud/sdk-go); the full API specification is at [https://api.arubacloud.com/docs/docs/intro](https://api.arubacloud.com/docs/docs/intro).

Throughout the codebase and this documentation, "CMP resource" means the live cloud-side representation of a resource, as opposed to the Kubernetes object (the desired state).

## Reconciliation Flow

The core pattern is a **three-layer reconciliation**:

1. **`internal/reconciler/reconciler.go`** — Base `Reconciler` struct with the shared loop:
   - Step 1: Register finalizer + set `Phase=Pending` (requeue on add)
   - Step 2: Delegate to `ResourceReconciler.HandleReconcile()`
   - Step 3: Remove finalizer when phase == `Deleted`

2. **`internal/controller/<resource>_controller.go`** — Resource-specific reconciler implementing `ResourceReconciler`:
   - Fetches the CMP resource from the Aruba API via the high-level SDK wrapper client (nil if not found)
   - Passes both the Kubernetes object and the CMP wrapper (`*aruba.Xxx`) to `TransitionSet.Run()`
   - Each controller file follows a canonical 14-section layout documented in `ai/CONVENTIONS.md` ("Controller file layout")

3. **`internal/reconciler/transition.go`** — Generic state machine (`TransitionSet[K, A]`):
   - Evaluates transitions in order; executes the first whose condition matches
   - Each `AbstractTransition` has: `KCondition`, `ACondition`, `KAction`, `AAction`, `KActionOnASuccess`, `KActionOnAError`, `Requeue`, `RequeueOnError`
   - Falls back to `DefaultKAction`/`DefaultRequeue` if no transition matches

## Key Types

- **`ResourceObject`** interface (reconciler pkg) — all managed CRDs implement this; provides `GetResourceStatus()` and `GetTenant()`
- **`ResourcePhase`** — Kubernetes-side state machine phases: `Pending → Creating → Provisioning → WaitingCondition → Active → Updating → Deleting → Deleted → Failed`
- **`ResourceStatus.Conditions`** — standard `metav1.Condition` list; at any moment exactly one condition has `Status=True` and encodes the current `Phase+Reason` pair
- **`TransitionSet[K, A]`** — parameterized over the Kubernetes type (K) and the SDK CMP wrapper type (A, e.g. `*aruba.VPC`)
- Requeue constants: `ShortRequeueAfter` (1s), `LongRequeueAfter` (20s), `MaxPhaseTimeout` (10 min timeout for transitory phases)

## Logging

The operator uses **logr** backed by `log/slog` with a JSON handler (initialized in `cmd/main.go`). The standard pattern throughout the codebase is `log.FromContext(ctx)`.

In `HandleReconcile`, each controller enriches the logger with `tenant` and stores it back in context before calling `ts.Run()`:

```go
logger := log.FromContext(ctx).WithValues("tenant", kubeObj.Spec.Tenant)
ctx = log.IntoContext(ctx, logger)
```

This ensures the transition engine and action helpers downstream automatically inherit the `tenant` field via their own `log.FromContext(ctx)` calls. See `CONVENTIONS.md` for the full logging guide (levels, fields, security).

## Metrics

The operator exposes a single Prometheus histogram defined in `internal/reconciler/metrics.go`:

```
aruba_reconcile_step_duration_seconds
```

It measures the duration of each `HandleReconcile` call (the resource-specific reconciliation step in `Reconciler.Reconcile()`) and is registered with the controller-runtime metrics registry (`sigs.k8s.io/controller-runtime/pkg/metrics`). The phase and reason labels are captured by re-fetching the resource after `HandleReconcile` returns, so they reflect the status written during that reconciliation.

### Dimensions

| Label | Values | Source |
|-------|--------|--------|
| `resource_kind` | `Project`, `BlockStorage`, `CloudServer`, `ElasticIP`, `KeyPair`, `SecurityGroup`, `SecurityRule`, `Subnet`, `VPC` | `reflect.TypeOf(obj).Elem().Name()` |
| `result` | `success`, `error` | Whether `HandleReconcile` returned an error |
| `phase` | `Creating`, `Active`, `Deleting`, `Deleted`, `Failed`, etc. (or `""`) | `obj.GetResourceStatus().Phase` after `HandleReconcile` completes |
| `reason` | `ShallSynchronize`, `Synchronizing`, `Synchronized`, `Failed` (or `""`) | Active condition's `.Reason` after `HandleReconcile` completes |

### Endpoint

Metrics are served on `:9080` via plain HTTP (no TLS, no authentication). The controller-runtime manager automatically handles the `/metrics` path.

## Condition Reason State Machine

Within each phase, the `Reason` field on the active condition acts as a sub-state:

| Reason | Meaning |
|--------|---------|
| `ShallSynchronize` | Intent recorded; CMP call not yet dispatched |
| `Synchronizing` | CMP call dispatched; waiting for confirmation |
| `Synchronized` | CMP confirmed; ready to advance to next phase |
| `Failed` | Timeout or CMP-side failure state; terminal until manually resolved |

## Action Execution Order in a Transition

```
if KAction defined  → run KAction only
else if AAction defined:
    run AAction
    on success → run KActionOnASuccess (typically updates K8s status)
    on error   → run KActionOnAError   (typically sets error phase)
                 then → RequeueOnError  (determines requeue strategy)
```

KAction and AAction are mutually exclusive by design to avoid double side-effects.

**Standard error-handling wiring** for CMP-facing transitions (`ShouldBeCreatedInCMP`, `ShouldBeUpdatedOnCMP`, `ShouldBeDeletedOnCMP`):

- `KActionOnAError`: `KubeSetErrorMessageOnCMPError[K, A](r.Client)` — behavior depends on the error category:
  - **Semantic** (4xx with field-level validation errors): during `Creating` or `Updating` phase, moves the resource to `Failed+ValidationFailed` immediately so the user gets feedback. Recovery is generation-gated: `IsCMPValidationFailedAndSpecChanged` in `HandleReconcile` Stage 4 resets the phase only after the user edits the spec (generation changes).
  - **Transient** (4xx without validation errors) or **Technical** (5xx/network): surfaces the error message in the condition without changing the resource's phase or reason. Only timeouts (`PhaseTimedOut`) may eventually move these to `Failed`.
- `RequeueOnError`: `SmartRequeueOnError[K, A]` — uses `ShortRequeueAfter` for technical errors, `LongRequeueAfter` for transient errors, and `ctrl.Result{}` (no requeue) for semantic errors — the resource waits for a spec change to trigger the next reconcile. Exception: during the `Deleting` phase, semantic errors always get `LongRequeueAfter` (there is no "wait for spec change" recovery during deletion — the error is a temporary CMP-side condition that resolves once dependent resources are cleaned up).

## Transition Patterns

Every controller builds a `TransitionSet` evaluated top-to-bottom each reconciliation. The transitions below describe the standard patterns in use; all are evaluated against both the Kubernetes object state and the live CMP response (which may be `nil` if the resource doesn't exist on the CMP yet).

### 0. Timeout safety net (always first)

**`PhaseTimedOut`** — if the resource has been in any transitory phase with reason `ShallSynchronize` or `Synchronizing` for longer than `MaxPhaseTimeout`, move it to `Failed`. This must be the first transition to short-circuit stuck resources before any other logic runs.

### 1. Unblock deletion for ValidationFailed resources

When a resource is being deleted while stuck in any `*ValidationFailed` state (`ValidationFailed`, `IntentionValidationFailed`, or `PostValidationFailed`), the `ValidationFailedAndDeleting` transition resets the phase to `Pending+Synchronized` (no `ResourceID`) or `Active+Synchronized` (has `ResourceID`), allowing the normal deletion flow to proceed on the next reconcile.

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ValidationFailedAndDeleting` | deleting + `Phase=Failed` + any `*ValidationFailed` reason | any (always true) | `KubeResetValidationFailedForDeletion` — reset to `Pending+Synchronized` or `Active+Synchronized` |

**Trade-off**: Because this runs inside the `TransitionSet`, the CMP data fetch (Stages 5–6) executes before the transition fires, adding one unnecessary CMP API call for this rare edge case. This is acceptable because (a) it only happens once per deletion of a validation-failed resource, and (b) the reset causes a short requeue and the normal deletion flow takes over.

### 2. Deletion from Pending (before CMP resource ever exists)

When a resource is deleted while still in `Pending` phase (i.e., no CMP resource was ever created), the standard deletion flow is bypassed entirely. This transition must come before `ShouldBeDeleted`.

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `PendingAndDeleting` | `Phase=Pending` + deleting + no `ResourceID` | any (always true) | `KubeDeleteFromPending` — set `Deleted+Synchronized` directly (no CMP interaction; Pending condition was already Synchronized) |

After `KubeDeleteFromPending` sets `Phase=Deleted`, the base reconciler's Step 3 removes the finalizer immediately.

### 3. Deletion flow

Triggered by Kubernetes setting `DeletionTimestamp`. Steps:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeDeleted` | deleting + Active/Synchronized | CMP resource exists in a final state | Mark `Deleting+ShallSynchronize` |
| `ShouldDeleteTimedOut` | deleting + Failed (timed-out, not during Deleting) | any | Mark `Deleting+ShallSynchronize` |
| `WaitingChildrenDeletion` *(parent controllers only)* | `Deleting+ShallSynchronize` + owned K8s children still exist | CMP exists | **kAction**: `deleteOwnedChildren` — issues `c.Delete()` on each child not yet being deleted; then long requeue until all children are gone |
| `ShouldBeDeletedOnCMP` | `Deleting+ShallSynchronize` | CMP exists | Call CMP delete → on success mark `Deleting+Synchronizing`; on error: `KubeSetErrorMessageOnCMPError` + `SmartRequeueOnError` |
| `DeletionOnCMPNotNeeded` | `Deleting+ShallSynchronize` | CMP not found | Skip CMP call, mark `Deleting+Synchronized` directly |
| `WaitingDeletionOnCMP` | `Deleting+Synchronizing` | CMP still exists | No action, long requeue |
| `DeletionConfirmedOnCMP` | `Deleting+Synchronizing` | CMP gone | Mark `Deleting+Synchronized` |
| `DeletionAccomplished` | `Deleting+Synchronized` | CMP gone | Mark `Deleted` → base reconciler removes finalizer |

### 4. Update flow

Triggered when `ObservedGeneration != Generation` (spec changed). Resources may additionally guard immutable fields before entering this flow.

#### 4a. Standard update (CMP has an update API)

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `HasDeniedChanges` *(optional)* | `Active` + generation changed + immutable field differs | CMP exists in final state | Return error (surfaced as status message); long requeue |
| `SpecAlreadyInSyncWithCMP` | `Active` + generation changed + no actual diff | CMP exists | Re-stamp `ObservedGeneration`, stay `Active+Synchronized` |
| `ShouldBeUpdated` | `Active` + generation changed + real diff | CMP exists in final state | Mark `Updating+ShallSynchronize` |
| `ShouldBeUpdatedOnCMP` | `Updating+ShallSynchronize` | CMP exists in final state | Call CMP update → on success mark `Updating+Synchronizing`; on error: `KubeSetErrorMessageOnCMPError` + `SmartRequeueOnError` |
| `WaitingUpdateOnCMP` | `Updating+Synchronizing` + spec still differs | CMP exists (transitory or diverged) | No action, long requeue |
| `UpdateConfirmedOnCMP` | `Updating+Synchronizing` + spec converged | CMP exists | Mark `Updating+Synchronized` |
| `UpdateAccomplished` | `Updating+Synchronized` | CMP in final/active state | `SetActiveAndSetID` |

#### 4b. Update-not-supported rollback (CMP has no update API)

When the CMP provides no update endpoint, spec changes must be rejected and rolled back. The resource visibly enters the `Updating` phase, surfaces a `Failed` condition, then reverts the spec to the CMP's current state and returns to `Active`. This uses three transitions instead of the standard update flow:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeUpdated` | `Active` + `ObservedGeneration != Generation` | CMP exists | Mark `Updating+ShallSynchronize` |
| `UpdateNotSupported` | `Updating+ShallSynchronize` | CMP exists | `kubeMarkUpdatingFailed` — set `Updating+Failed` condition with message `"updating <Resource> resources is not supported"` |
| `UpdateRollback` | `kube<Resource>UpdatingFailed` (phase=Updating + condition reason=Failed) | CMP exists | `kubeRollbackSpecAndSetActive` — restore spec fields from CMP response (object patch), then call `reconciler.SetActiveAndSetID` (status patch) |

**Key implementation details:**

- `kubeMarkUpdatingFailed` is a thin wrapper over `reconciler.SetPhaseAndCondition` with `phase=Updating`, `reason=Failed`, and a resource-specific error message.
- `kube<Resource>UpdatingFailed` is a custom KCondition that checks `phase == Updating` AND `condition.Reason == Failed` (guards against matching other Updating sub-states).
- `kubeRollbackSpecAndSetActive` is a two-step action:
  1. **Spec rollback** (object patch via `retry.RetryOnConflict`): read a fresh copy, restore mutable spec fields from the CMP response, patch the object. This produces a new `Generation`.
  2. **Set Active** (`reconciler.SetActiveAndSetID`): reads fresh object (capturing the new generation from step 1), stamps `ObservedGeneration`, writes `Active+Synchronized`.
- The rollback transition uses `NoRequeue` because `reconciler.SetActiveAndSetID` internally stamps `ObservedGeneration` to the new generation, preventing re-entry into `ShouldBeUpdated` on the next reconcile.
- In tests, the `UpdateRollback` test verifies that `Spec.Tags` and `Spec.Region` (or the resource's equivalent mutable fields) are restored to the CMP response values.

### 5. Creation flow

Triggered on the first reconciliation (`Phase=Pending`, empty `ResourceID`, `Reason=Synchronized`).

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeCreated` | `Phase=Pending` + no `ResourceID` + `Reason=Synchronized` | CMP not found | Mark `Creating+ShallSynchronize` |
| `ShouldBeCreatedInCMP` | `Creating+ShallSynchronize` | CMP not found | Call CMP create → on success mark `Creating+Synchronizing`; on error: `KubeSetErrorMessageOnCMPError` + `SmartRequeueOnError` |
| `WaitingCreationInCMP` | `Creating+Synchronizing` | CMP not found or transitory | No action, long requeue |
| `CreationConfirmedOnCMP` | `Creating+Synchronizing` | CMP now found/active | Mark `Creating+Synchronized` |
| `CreationAccomplished` | `Creating+Synchronized` | CMP active | `SetActiveAndSetID` (stores `ResourceID`, stamps `ObservedGeneration`) |

### 6. CMP-side failure detection (resources with CMP failure states)

Resources whose CMP state machine includes an explicit `Failed` state include an additional catch-all transition:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `IsInError` | any (always true) | CMP state == `Failed` | Mark K8s resource `Failed+Synchronized` |

This is evaluated after all other transitions so it only fires when nothing else matches.

## Resource Ownership and Cascade Delete

Resources form a containment hierarchy. Each child has exactly one controller-owner — the closest lifecycle parent:

```
Project (root)
  ├── VPC            (Spec.ProjectReference)
  │   ├── Subnet           (Spec.VPCReference)
  │   └── SecurityGroup    (Spec.VPCReference)
  │       └── SecurityRule (Spec.SecurityGroupReference)
  ├── BlockStorage   (Spec.ProjectReference)
  ├── KeyPair        (Spec.ProjectReference)
  ├── ElasticIP      (Spec.ProjectReference)
  └── CloudServer    (Spec.ProjectReference)
```

CloudServer _uses_ VPC, Subnet, SecurityGroup, KeyPair, BlockStorage, and ElasticIP, but its **owner** is Project. Deleting a VPC does **not** cascade to CloudServer.

### Two-Layer Ownership Model

The operator uses a **custom two-layer ownership system** instead of standard Kubernetes OwnerReferences. Standard OwnerReferences are not used because they have no `Namespace` field — the K8s garbage collector resolves owners in the child's namespace only, making cross-namespace ownership impossible.

| Layer | Annotation / Label Key | Value | Purpose |
|---|---|---|---|
| **ArubaOwnerReference annotation** | `arubacloud.com/owner-references` | JSON `[]ArubaOwnerReference` | Source of truth. Contains full identity of the owner: namespace, name, UID, kind, apiVersion. Supports cross-namespace. |
| **Ownership label** | `arubacloud.com/owner-<lowerkind>` | owner UID | Efficient server-side cluster-wide label queries for `hasOwnedChildren` / `deleteOwnedChildren`. |

#### ArubaOwnerReference

Defined in `api/v1alpha1/common_types.go` as `ArubaOwnerReference`. Mirrors `metav1.OwnerReference` but adds a `Namespace` field. A child stores at most one controller-owner reference (same semantics as K8s OwnerReferences).

Example annotation value:
```json
[{"apiVersion":"arubacloud.com/v1alpha1","kind":"Project","namespace":"ns-a","name":"my-project","uid":"abc-123","controller":true,"blockOwnerDeletion":true}]
```

### How cascade delete works

Three mechanisms cooperate:

1. **ArubaOwnerReference annotation** — records the full ownership identity (including namespace) on each child. Source of truth for ownership.
2. **Ownership label** — `arubacloud.com/owner-<kind>=<uid>` enables cluster-wide label-selector queries to find all children of a given parent, regardless of namespace.
3. **Finalizers** — each resource's finalizer blocks K8s from removing the object from etcd until the CMP-side resource is deleted.

> **Why not K8s OwnerReferences:** The K8s garbage collector resolves the owner within the child's namespace only. A cross-namespace K8s OwnerReference would be treated as a dangling reference, potentially triggering premature GC of the child. The operator's WaitingChildrenDeletion mechanism handles all cascade deletion explicitly, making K8s GC redundant.

### Ownership setup

Each child controller sets ownership metadata in `HandleReconcile` **before** calling `ts.Run()`. The shared helpers in `internal/controller/owner_reference.go` handle the mechanics:

- `resolveOwnerObject` — fetches the parent K8s object by `ResourceReference` (name + namespace).
- `ensureOwnerReference` — idempotently sets both the annotation and label on the child in a single atomic patch. Returns `(needsRequeue=true, nil)` on first set; `(false, nil)` when already present.
- `setArubaControllerReference` — low-level helper used by `ensureOwnerReference`; upserts the owner entry in the annotation, enforcing the single-controller constraint.
- `ownerLabelKey(scheme, owner)` — derives the ownership label key from the owner's GVK (e.g. `"arubacloud.com/owner-project"`).

If the parent K8s object is not found (e.g., already deleted), the ownership setup step is skipped and reconciliation proceeds normally.

### Cross-Namespace Ownership

Ownership works identically regardless of namespace topology. A child in `ns-b` with a parent in `ns-a` gets the same annotation + label as a same-namespace child. There are no special cases.

### WaitingChildrenDeletion transition

Parent controllers (Project, VPC, SecurityGroup) include a `WaitingChildrenDeletion` transition **before** `ShouldBeDeletedOnCMP` in their transition set. It matches when the resource is in `Deleting+ShallSynchronize` and owned K8s children still exist.

The transition has two responsibilities:

1. **`kAction` — explicitly delete owned children**: calls `deleteOwnedChildren` (`owner_reference.go`), which does a cluster-wide label-selector query and issues `c.Delete()` on each child not yet being deleted. Children have finalizers, so the K8s API sets their `DeletionTimestamp` and they enter their own deletion flow.
2. **Block CMP deletion**: after kicking off child deletion, requeues with `LongRequeueAfter`. The `hasOwnedChildren` check (also cluster-wide label query) keeps this transition matched until all children are fully gone (finalizers removed, evicted from etcd).

### No child watches

Parent controllers deliberately do **not** watch their children. `SetupWithManager` registers only `For(&Parent{})`.

Children are only ever consulted from `WaitingChildrenDeletion`, i.e. during deletion, and that transition re-drives itself with `LongRequeueAfter` (20s) for as long as children remain. A watch would therefore only shorten the gap between the last child disappearing and the parent noticing — worth little, since children take minutes to delete CMP-side — while costing one parent reconcile (and one CMP `List`) for *every* child event, including the frequent status patches children emit while polling the CMP.

Note this does not reduce informer or cache load: `hasOwnedChildren` / `deleteOwnedChildren` read child types through the manager's cache-backed client, and each child type has its own controller anyway, so the shared informers exist regardless. The `watch` RBAC verb on child resources is still required.

Trade-off: cascade deletion can take up to 20s longer per level of the hierarchy.

### Deletion flow with cascade

When a parent is deleted:

1. K8s sets `DeletionTimestamp` on the parent → parent's `ShouldBeDeleted` marks `Deleting+ShallSynchronize`
2. `WaitingChildrenDeletion` fires → `kAction` calls `c.Delete()` on each owned child not yet being deleted (found via cluster-wide label query)
3. Children receive `DeletionTimestamp` (because they have finalizers) → their controllers run the standard deletion flow (CMP delete → finalizer removed → evicted from etcd)
4. The parent re-reconciles every `LongRequeueAfter` (20s); `WaitingChildrenDeletion` keeps matching until all children are gone
5. Once no owned children remain, `WaitingChildrenDeletion` no longer matches → `ShouldBeDeletedOnCMP` fires → parent deleted from CMP → finalizer removed → parent evicted from etcd

See `ai/KNOWN_ISSUES.md` for edge cases (concurrent sibling deletion order, stuck children, envtest limitations).

## HandleReconcile responsibilities

`HandleReconcile` in each controller follows an 8-stage pipeline:

1. **Setup** — type assertion, logger enrichment (`tenant` field), `isDeleting` flag.

2. **Fetch K8s dependencies + set owner reference** (`fetchKubeDependencies`) — resolves the K8s parent object (Project, VPC, SecurityGroup), sets the cross-namespace owner reference annotation/label, and returns `ShortRequeue` if the patch was applied. Returns `nil` (skip silently) when the parent is not found in K8s; this preserves all existing tests that don't create K8s parents. For CloudServer, also fetches K8s dependency objects (VPC, BootVolume, Subnets, SecurityGroups, KeyPair, ElasticIP) needed for intention validation.

3. **Parent readiness precondition** — blocks first-time CMP creation until the K8s parent is `Active+Synchronized`. Guard: `!isDeleting && kubeParent != nil && kubeResource.Status.ResourceID == "" && !reconciler.IsResourceReady(kubeParent)`. Returns `LongRequeue` when the parent is not yet ready. This prevents creating child CMP resources before the parent CMP resource exists.

4. **Intention cross-validation (`ivs.Run`)** — K8s-only validation, runs **before** any CMP calls. Skipped during deletion (`isDeleting`). If any rule fails → `Failed+IntentionValidationFailed` + return (no requeue — wait for spec change). If rules now pass but resource was previously `IntentionValidationFailed` → recovery: reset to `Pending+Synchronized` or `Active+Synchronized` + `ShortRequeue`. After ivs passes, if resource is `Failed+ValidationFailed` (CMP semantic error) and spec was changed since failure (`IsCMPValidationFailedAndSpecChanged`) → recovery: reset phase + `ShortRequeue`. See the Cross-Resource Consistency Validation section below.

5. **Aruba client creation** — calls `r.ArubaClient(kubeObj.Spec.Tenant)` to obtain a tenant-scoped `aruba.Client`.

6. **Resolve CMP dependencies** (`fetchCMPDependencies`) — looks up CMP parent IDs, fetches all CMP dependency responses, and fetches the primary CMP resource by name filter (returns `nil` if not found).

7. **CMP-aware drift validation (`vs.Run`)** — runs only when `!isDeleting && cmpObj != nil` (and, for CloudServer, `kubeBdl.KubeProject != nil`). If any rule fails → `Failed+PostValidationFailed` + return (no requeue). If rules now pass but resource was previously `PostValidationFailed` → recovery: reset to `Active+Synchronized` + `ShortRequeue`.

8. **Call `ts.Run(ctx, kubeObj, cmpObj)`** — invoke the state machine.

## Cross-Resource Consistency Validation Engine

Each controller uses a **two-tier** validation model:

| Set | Field | Stage | Runs when | Data source | Purpose |
|-----|-------|-------|-----------|-------------|---------|
| `ivs` | `r.ivs` | Stage 4 | Always (unless deleting) | K8s spec fields only | Reference presence + cross-resource K8s intent (fail-fast before any CMP calls) |
| `vs` | `r.vs` | Stage 7 | CMP resource exists (`cmpObj != nil`) | CMP responses + K8s specs | Post-creation drift detection |

Both sets use the same `ValidationSet[K, A, B]` engine in `internal/reconciler/validation.go`.

### How it works

**Stage 4 — ivs (intention validation)**: runs before any CMP calls. If any rule fails:
1. `SetPhaseAndCondition` sets `Failed+IntentionValidationFailed` on the K8s object.
2. `HandleReconcile` returns `ctrl.Result{}` (no requeue — resource waits for spec change).

If the current state is `Failed+IntentionValidationFailed` but all rules now pass, a **recovery block** resets the phase to `Pending+Synchronized` (or `Active+Synchronized` if `ResourceID` is non-empty) and returns `ShortRequeue`. After ivs passes, if the resource is `Failed+ValidationFailed` (CMP semantic error) and the spec has changed since the failure (`IsCMPValidationFailedAndSpecChanged` — generation mismatch), the same recovery pattern fires.

ivs is skipped when `isDeleting` is true — this ensures that resources with missing references can still be deleted (the finalizer runs unblocked).

**Stage 7 — vs (drift validation)**: runs after CMP dependencies are fetched. If any rule fails → `Failed+PostValidationFailed` + return (no requeue). If the resource was previously `PostValidationFailed` but rules now pass, a recovery block resets to `Active+Synchronized` + `ShortRequeue`.

**Empty bundle for ivs**: When `kubeBdl` can be nil (dependency not found in K8s), the controller passes `&kube<Resource>Bundle{}` so reference rules can fire without nil dereferences. Cross-validation rules that access bundle fields nil-guard those fields and return `nil` when the dependency object is not yet present.

All rules are **always evaluated** (no short-circuit); the caller sees every violation at once.

### ValidationSet API

```go
// In internal/reconciler/validation.go
type ValidationFunc[K ResourceObject, A any, B any] func(k K, a A, b B) error
type ValidationSet[K ResourceObject, A any, B any] struct { ... }
func (vs *ValidationSet[K, A, B]) Add(name string, fn ValidationFunc[K, A, B])
func (vs *ValidationSet[K, A, B]) Run(k K, a A, b B) error  // returns *ErrInvalid or nil
```

### Bundle struct

Controllers that reference other resources use a **two-part bundle composition** (see `CONVENTIONS.md` for the full pattern):

- `kube<Resource>Bundle` — K8s-only fields fetched by `fetchKubeDependencies`; the `ivs` type parameter is `*kube<Resource>Bundle`
- `cmp<Resource>Bundle` — CMP-only fields fetched by `fetchCMPDependencies`; carries resolved CMP responses for parent resources
- `<resource>Bundle` — embeds both sub-bundles via struct embedding; the `vs` type parameter is `*<resource>Bundle`

**Simple controllers** (VPC, BlockStorage, ElasticIP, KeyPair) have no `cmp<Resource>Bundle` — there are no CMP-only bundle fields for these controllers. The `<resource>Bundle` embeds only `kube<Resource>Bundle`. The split is still present: `kube<Resource>Bundle` is the ivs type parameter and `<resource>Bundle` is the vs type parameter.

Fields within each sub-bundle appear in the order they are fetched: `fetchKubeDependencies` fetch order for K8s fields, and `fetchCMPDependencies` / resolve* call order for CMP fields.

### Validation dimensions

| Dimension | `vs` (CMP drift) source | `ivs` (intention) source |
|---|---|---|
| **Tenant** | K8s spec fields | K8s spec fields |
| **VPC reference** | K8s spec fields | K8s spec fields |
| **Project reference** | K8s spec fields | K8s spec fields |

### Rules per controller

The `ivs` column always lists reference rules first (presence checks), followed by cross-resource consistency checks. The `vs` column contains only cross-resource consistency checks (no reference rules — the resource already exists on CMP by Stage 7).

| Controller | `ivs` rules | `vs` rules |
|---|---|---|
| VPC | ProjectReferenceRequired, TenantMustMatchProject | TenantMustMatchProject |
| BlockStorage | ProjectReferenceRequired, TenantMustMatchProject | TenantMustMatchProject |
| ElasticIP | ProjectReferenceRequired, TenantMustMatchProject | TenantMustMatchProject |
| KeyPair | ProjectReferenceRequired, TenantMustMatchProject | TenantMustMatchProject |
| Subnet | ProjectReferenceRequired, VPCReferenceRequired, TenantMustMatchVPC, ProjectMustMatchVPC, TenantMustMatchProject | TenantMustMatchVPC, ProjectMustMatchVPC, TenantMustMatchProject |
| SecurityGroup | ProjectReferenceRequired, VPCReferenceRequired, TenantMustMatchVPC, ProjectMustMatchVPC, TenantMustMatchProject | TenantMustMatchVPC, ProjectMustMatchVPC, TenantMustMatchProject |
| SecurityRule | ProjectReferenceRequired, VPCReferenceRequired, SecurityGroupReferenceRequired, TenantMustMatchSecurityGroup, VPCMustMatchSecurityGroup, ProjectMustMatchSecurityGroup, TenantMustMatchProject | TenantMustMatchSecurityGroup, VPCMustMatchSecurityGroup, ProjectMustMatchSecurityGroup, TenantMustMatchProject |
| CloudServer | ProjectReferenceRequired, VPCReferenceRequired, BootVolumeReferenceRequired, SubnetReferencesRequired, SecurityGroupReferencesRequired, TenantMustMatchProject, VPCMustMatchAllSubnets, VPCMustMatchAllSecurityGroups, TenantMustMatchVPC\†, TenantMustMatchBootVolume\†, TenantMustMatchKeyPair\*†, TenantMustMatchElasticIP\*†, TenantMustMatchAllSubnets, TenantMustMatchAllSecurityGroups, ProjectMustMatchVPC\†, ProjectMustMatchBootVolume\†, ProjectMustMatchKeyPair\*†, ProjectMustMatchElasticIP\*†, ProjectMustMatchAllSubnets, ProjectMustMatchAllSecurityGroups | TenantMustMatchProject, VPCMustMatchAllSubnets, VPCMustMatchAllSecurityGroups, TenantMustMatchVPC\†, TenantMustMatchBootVolume\†, TenantMustMatchKeyPair\*†, TenantMustMatchElasticIP\*†, TenantMustMatchAllSubnets, TenantMustMatchAllSecurityGroups, ProjectMustMatchVPC\†, ProjectMustMatchBootVolume\†, ProjectMustMatchKeyPair\*†, ProjectMustMatchElasticIP\*†, ProjectMustMatchAllSubnets, ProjectMustMatchAllSecurityGroups |

\* Optional rules: silently skipped when the optional resource is not referenced.

\† Nil-guarded: silently skipped when the referenced K8s object is not found (dependency not yet applied).

### Condition reason

`ConditionReasonValidationFailed = "ValidationFailed"` is defined in `api/v1alpha1/common_types.go`.

### Nil-safety

**`ivs` cross-validation rules**: All rules that access K8s dependency objects in the bundle (KubeProject, KubeVpc, KubeSG, etc.) nil-guard the field and return `nil` when the object is missing. If the user hasn't applied the referenced K8s object yet, the cross-validation rule is silently skipped. It will fire on the next reconcile once the dependency object exists.

**`ivs` reference rules** (e.g. `ProjectReferenceRequired`): These only access the K8s object (`k`), never the bundle, so nil-guarding the bundle is not needed. They fire regardless of whether the bundle dependency is present.

**`vs` rules**: `FieldMustMatch` is used for most vs rules where the bundle dependency is guaranteed non-nil at Stage 7 (e.g., `KubeVpc` in Subnet/SecurityGroup vs, `KubeSG` in SecurityRule vs). However, `TenantMustMatchProject` in Subnet, SecurityGroup, and SecurityRule vs uses a nil-guarded inline lambda because `KubeProject` is not a direct parent for these controllers and may be nil. For CloudServer, Stage 7 is additionally gated on `kubeBdl.KubeProject != nil` because the `TenantMustMatchProject` vs rule uses `FieldMustMatch` which dereferences `KubeProject` directly.

### Project reference cross-validation

`ProjectMustMatch*` rules validate that the resource's `Spec.ProjectReference.Name` matches the parent's (or dependency's) `Spec.ProjectReference.Name`. This catches misconfigured resources referencing resources from different projects before any CMP interaction occurs.

Note: the `resolveProjectID` helpers also pass the project ID as a path parameter to every CMP API call, so the CMP API independently scopes responses to the resolved project. The K8s-side `ProjectMustMatch*` rules serve as an earlier, declarative consistency check that fires at the Pending phase without requiring a round-trip to CMP.

## Authentication Modes

The operator supports two credential modes, selected by `ReconcilerConfig.VaultIsEnabled`.

### Multi-tenant client lifecycle

The `Reconciler` holds a private `multiTenantClient arubamt.Multitenant` (from `github.com/Arubacloud/sdk-go/pkg/multitenant`). This is a thread-safe cache of `aruba.Client` instances keyed by tenant string, initialized with `arubamt.New()` in `NewReconciler()`.

When `HandleReconcile` calls `r.ArubaClient(tenant)`:
1. The cache is checked via `multiTenantClient.Get(tenant)`.
2. On a cache miss, a new `aruba.Client` is built from `ReconcilerConfig`:
   - **Vault mode** (`VaultIsEnabled = true`): credentials are fetched from HashiCorp Vault AppRole using the tenant string as the KV path scope (`WithVaultCredentialsRepository(vaultAddress, kvMount, tenant, ...)`).
   - **Direct mode** (`VaultIsEnabled = false`): the global `ClientID`/`ClientSecret` are used for all tenants (a warning is logged).
3. The new client is added to the cache via `multiTenantClient.Add(tenant, client)` and returned.

### Tenant source

Every CRD type has a `Spec.Tenant` field and satisfies `ResourceObject.GetTenant()`. Controllers read `kubeObj.Spec.Tenant` to determine which tenant-scoped client to request.

`Spec.Tenant` is **intentionally mutable** — there is no CRD-level CEL XValidation rule enforcing immutability. This allows users to correct a wrong tenant on a `Failed` resource without deleting and recreating it. Tenant consistency is still enforced at reconcile time via the `TenantMustMatch*` cross-validation rules (both `ivs` and `vs`), which compare the resource's tenant against its K8s dependencies.

## Testing Conventions

- Ginkgo v2 + Gomega for BDD-style tests
- Integration tests use `controller-runtime/envtest` (fake K8s API server)
- The CMP API is faked with a real `aruba.Client` pointed at an `httptest.Server`. The SDK v1.0.4 wrapper objects (`*aruba.VPC`, …) cannot be constructed with server-assigned fields (ID/State/…) outside the `aruba` package, so they cannot be returned from a mock. Instead, `internal/controller/common_test.go` provides `fakeCMP` — an httptest server that serves canned JSON per resource collection — and `newTestReconciler` seeds the multitenant cache with a genuine SDK client bound to it. Controllers therefore exercise the real request/response/hydration path.
- Test helpers and fixtures are in `common_test.go` in each package; per-resource CMP item builders follow the pattern `<resource>Item(id, name, state)` and `default<Resource>Spec(...)`; the `controller` package's `common_test.go` provides `fakeCMP`, `newTestReconciler`, `strPtr`, and `findCondition`
- CMP error categories are exercised by staging a fake CMP response status code (`fakeCMP.postStatus/putStatus/deleteStatus` and `errKind = "validation"` for a semantic 4xx)

## Adding a New Controller

1. Define the CRD type in `api/v1alpha1/` and run `make manifests generate`
2. Create `internal/controller/<resource>_controller.go`: embed `*reconciler.Reconciler`, define `kube<Resource>Bundle` and `<resource>Bundle` structs, declare `ivs`, `vs`, and `ts` fields on the reconciler struct
3. Implement `Object()`, `Finalizer()`, `HandleReconcile()` — use the 8-stage pipeline (see `CONVENTIONS.md` "Calling the engine in HandleReconcile")
4. Implement `fetchKubeDependencies` (K8s parent resolution + owner reference), `fetchCMPDependencies` (CMP ID resolution + primary resource fetch), `newIntentionValidationSet` (reference rules + nil-safe cross-validation), `newValidationSet` (drift rules), `newTransitionSet` (full lifecycle state machine)
5. Register the controller in `cmd/main.go`

See `ai/templates/plans/NEW_CONTROLLER.md` for the full scaffold template.
