# Architecture

This is a **Kubebuilder v4** Kubernetes operator for managing Aruba Cloud infrastructure declaratively via CRDs.

## Terminology

**CMP** — Aruba Cloud Management Platform. The cloud control plane that this operator talks to in order to provision and manage resources. It is accessed programmatically via the [Aruba Go SDK](https://github.com/Arubacloud/sdk-go); the full API specification is at [https://api.arubacloud.com/docs/docs/intro](https://api.arubacloud.com/docs/docs/intro).

Throughout the codebase and this documentation, "CMP resource" means the live cloud-side representation of a resource, as opposed to the Kubernetes object (the desired state).

## Reconciliation Flow

The core pattern is a **three-layer reconciliation**:

1. **`internal/reconciler/reconciler.go`** — Base `Reconciler` struct with the shared loop:
   - Step 1: Register finalizer (requeue on add)
   - Step 2: Delegate to `ResourceReconciler.HandleReconcile()`
   - Step 3: Remove finalizer when phase == `Deleted`

2. **`internal/controller/<resource>_controller.go`** — Resource-specific reconciler implementing `ResourceReconciler`:
   - Fetches the CMP resource from the Aruba API (nil if not found)
   - Passes both the Kubernetes object and the CMP response to `TransitionSet.Run()`

3. **`internal/controller/transition.go`** — Generic state machine (`TransitionSet[K, A]`):
   - Evaluates transitions in order; executes the first whose condition matches
   - Each `AbstractTransition` has: `KCondition`, `ACondition`, `KAction`, `AAction`, `KActionOnASuccess`, `KActionOnAError`, `Requeue`, `RequeueOnError`
   - Falls back to `DefaultAction`/`DefaultRequeue` if no transition matches

## Key Types

- **`ResourceObject`** interface (reconciler pkg) — all managed CRDs implement this; provides `GetResourceStatus()` and `GetTenant()`
- **`ResourcePhase`** — Kubernetes-side state machine phases: `Creating → Provisioning → WaitingCondition → Active → Updating → Deleting → Deleted → Failed`
- **`ResourceStatus.Conditions`** — standard `metav1.Condition` list; at any moment exactly one condition has `Status=True` and encodes the current `Phase+Reason` pair
- **`TransitionSet[K, A]`** — parameterized over the Kubernetes type (K) and CMP API response type (A)
- Requeue constants: `ShortRequeueAfter` (1s), `LongRequeueAfter` (20s), `MaxPhaseTimeout` (10 min timeout for transitory phases)

## Condition Reason State Machine

Within each phase, the `Reason` field on the active condition acts as a sub-state:

| Reason | Meaning |
|--------|---------|
| `ShallSynchronize` | Intent recorded; CMP call not yet dispatched |
| `Synchronizing` | CMP call dispatched; waiting for confirmation |
| `Synchronized` | CMP confirmed; ready to advance to next phase |
| `Failed` | Timeout or CMP failure; terminal until manually resolved |

## Action Execution Order in a Transition

```
if KAction defined  → run KAction only
else if AAction defined:
    run AAction
    on success → run KActionOnASuccess (typically updates K8s status)
    on error   → run KActionOnAError   (typically sets error phase)
```

KAction and AAction are mutually exclusive by design to avoid double side-effects.

## Transition Patterns

Every controller builds a `TransitionSet` evaluated top-to-bottom each reconciliation. The transitions below describe the standard patterns in use; all are evaluated against both the Kubernetes object state and the live CMP response (which may be `nil` if the resource doesn't exist on the CMP yet).

### 0. Timeout safety net (always first)

**`PhaseTimedOut`** — if the resource has been in any transitory phase with reason `ShallSynchronize` or `Synchronizing` for longer than `MaxPhaseTimeout`, move it to `Failed`. This must be the first transition to short-circuit stuck resources before any other logic runs.

### 1. Deletion flow

Triggered by Kubernetes setting `DeletionTimestamp`. Steps:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeDeleted` | deleting + Active/Synchronized | CMP resource exists in a final state | Mark `Deleting+ShallSynchronize` |
| `ShouldDeleteTimedOut` | deleting + Failed (timed-out, not during Deleting) | any | Mark `Deleting+ShallSynchronize` |
| `ShouldBeDeletedOnCMP` | `Deleting+ShallSynchronize` | CMP exists | Call CMP delete → on success mark `Deleting+Synchronizing` |
| `DeletionOnCMPNotNeeded` | `Deleting+ShallSynchronize` | CMP not found | Skip CMP call, mark `Deleting+Synchronized` directly |
| `WaitingDeletionOnCMP` | `Deleting+Synchronizing` | CMP still exists | No action, long requeue |
| `DeletionConfirmedOnCMP` | `Deleting+Synchronizing` | CMP gone | Mark `Deleting+Synchronized` |
| `DeletionAccomplished` | `Deleting+Synchronized` | CMP gone | Mark `Deleted` → base reconciler removes finalizer |

### 2. Update flow

Triggered when `ObservedGeneration != Generation` (spec changed). Resources may additionally guard immutable fields before entering this flow.

#### 2a. Standard update (CMP has an update API)

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `HasDeniedChanges` *(optional)* | `Active` + generation changed + immutable field differs | CMP exists in final state | Return error (surfaced as status message); long requeue |
| `SpecAlreadyInSyncWithCMP` | `Active` + generation changed + no actual diff | CMP exists | Re-stamp `ObservedGeneration`, stay `Active+Synchronized` |
| `ShouldBeUpdated` | `Active` + generation changed + real diff | CMP exists in final state | Mark `Updating+ShallSynchronize` |
| `ShouldBeUpdatedOnCMP` | `Updating+ShallSynchronize` | CMP exists in final state | Call CMP update → on success mark `Updating+Synchronizing` |
| `WaitingUpdateOnCMP` | `Updating+Synchronizing` + spec still differs | CMP exists (transitory or diverged) | No action, long requeue |
| `UpdateConfirmedOnCMP` | `Updating+Synchronizing` + spec converged | CMP exists | Mark `Updating+Synchronized` |
| `UpdateAccomplished` | `Updating+Synchronized` | CMP in final/active state | `setActive+setID` |

#### 2b. Update-not-supported rollback (CMP has no update API)

When the CMP provides no update endpoint, spec changes must be rejected and rolled back. The resource visibly enters the `Updating` phase, surfaces a `Failed` condition, then reverts the spec to the CMP's current state and returns to `Active`. This uses three transitions instead of the standard update flow:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeUpdated` | `Active` + `ObservedGeneration != Generation` | CMP exists | Mark `Updating+ShallSynchronize` |
| `UpdateNotSupported` | `Updating+ShallSynchronize` | CMP exists | `kubeMarkUpdatingFailed` — set `Updating+Failed` condition with message `"updating <Resource> resources is not supported"` |
| `UpdateRollback` | `kube<Resource>UpdatingFailed` (phase=Updating + condition reason=Failed) | CMP exists | `kubeRollbackSpecAndSetActive` — restore spec fields from CMP response (object patch), then call `setActiveAndSetID` (status patch) |

**Key implementation details:**

- `kubeMarkUpdatingFailed` is a thin wrapper over `setPhaseAndCondition` with `phase=Updating`, `reason=Failed`, and a resource-specific error message.
- `kube<Resource>UpdatingFailed` is a custom KCondition that checks `phase == Updating` AND `condition.Reason == Failed` (guards against matching other Updating sub-states).
- `kubeRollbackSpecAndSetActive` is a two-step action:
  1. **Spec rollback** (object patch via `retry.RetryOnConflict`): read a fresh copy, restore mutable spec fields from the CMP response, patch the object. This produces a new `Generation`.
  2. **Set Active** (`setActiveAndSetID`): reads fresh object (capturing the new generation from step 1), stamps `ObservedGeneration`, writes `Active+Synchronized`.
- The rollback transition uses `NoRequeue` because `setActiveAndSetID` internally stamps `ObservedGeneration` to the new generation, preventing re-entry into `ShouldBeUpdated` on the next reconcile.
- In tests, the `UpdateRollback` test verifies that `Spec.Tags`, `Spec.Location.Value`, and `Spec.Value` (or the resource's equivalent mutable fields) are restored to the CMP response values.

### 3. Creation flow

Triggered on the first reconciliation (empty `ResourceID`, no phase, no conditions).

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `ShouldBeCreated` | first reconciliation | CMP not found | Mark `Creating+ShallSynchronize` |
| `ShouldBeCreatedInCMP` | `Creating+ShallSynchronize` | CMP not found | Call CMP create → on success mark `Creating+Synchronizing` |
| `WaitingCreationInCMP` | `Creating+Synchronizing` | CMP not found or transitory | No action, long requeue |
| `CreationConfirmedOnCMP` | `Creating+Synchronizing` | CMP now found/active | Mark `Creating+Synchronized` |
| `CreationAccomplished` | `Creating+Synchronized` | CMP active | `setActive+setID` (stores `ResourceID`, stamps `ObservedGeneration`) |

### 4. CMP-side failure detection (resources with CMP failure states)

Resources whose CMP state machine includes an explicit `Failed` state include an additional catch-all transition:

| Transition | K condition | CMP condition | Action |
|-----------|-------------|---------------|--------|
| `IsInError` | any (always true) | CMP state == `Failed` | Mark K8s resource `Failed+Synchronized` |

This is evaluated after all other transitions so it only fires when nothing else matches.

## HandleReconcile responsibilities

`HandleReconcile` in each controller does the following before calling `ts.Run`:

1. **Resolve parent references** — some resources reference others (e.g. a resource scoped to a project). The project ID is looked up from the CMP API and injected into the context so action methods can access it without re-fetching.
2. **Fetch the CMP resource** — list by name filter, validate cardinality (must be 0 or 1), return `nil` if not found.
3. **Validate consistency** — e.g. detect if a previously recorded parent ID no longer matches.

## Authentication Modes

- **Single-tenant**: Direct OAuth2 `ClientID`/`ClientSecret` via Keycloak
- **Multi-tenant**: HashiCorp Vault AppRole for per-tenant credential lookup

## Testing Conventions

- Ginkgo v2 + Gomega for BDD-style tests; Testify for mocks
- Mocks live in `internal/mocks/` and are generated by mockery — regenerate with `make generate`
- Integration tests use `controller-runtime/envtest` (fake K8s API server)
- Test helpers and fixtures are in `common_test.go`; builder functions follow the pattern `build<Resource>Response(...)` and `default<Resource>Spec(...)`

## Adding a New Controller

1. Define the CRD type in `api/v1alpha1/` and run `make manifests generate`
2. Create `internal/controller/<resource>_controller.go` embedding `*reconciler.Reconciler`
3. Implement `Object()`, `Finalizer()`, `HandleReconcile()` to satisfy `ResourceReconciler`
4. Build a `TransitionSet` covering all expected phase transitions
5. Register the controller in `cmd/main.go`
