# Plan: New / Refactored Controller for `<Resource>`

> **How to use this template**
> Fill in every `<…>` placeholder. Strike through sections that do not apply.
> Work through the steps in order; each implementation step must be followed by its testing step before moving to the next.

---

## 0. Nature of this work

- [ ] **New controller** — the CRD `<Resource>` does not exist yet; both the type and the controller must be created from scratch.
- [ ] **Refactor of existing controller** — `internal/controller/<resource>_controller.go` already exists but predates the `TransitionSet` pattern and must be rewritten to conform to it.

If this is a refactor, briefly describe what the old implementation does and what specifically needs to change:

> _<e.g. "The existing controller uses a simple if/else phase check with no TransitionSet, no timeout detection, and no immutable-field guards.">_

---

## 1. Resource overview

| Field | Value |
|-------|-------|
| CRD kind | `<Resource>` |
| API group/version | `arubacloud.com/v1alpha1` |
| CMP resource type | `<e.g. BlockStorage, VPC, …>` |
| CMP API reference | link to the relevant section in https://api.arubacloud.com/docs/docs/intro |
| SDK client accessor | `arubaClient.From<…>()` (client obtained from context via `reconciler.ArubaClientKey`) |
| Finalizer string | `<resource>.arubacloud.com/finalizer` |

---

## 2. Dependencies and resource relations

List every other CRD or external resource this controller depends on, and describe how to handle each during reconciliation.

| Dependency | Relation | How to resolve | Blocking? | What to do if unresolved |
|------------|----------|----------------|-----------|--------------------------|
| `<e.g. Project>` | `<e.g. spec.projectReference — parent scope>` | `<e.g. List CMP projects by name, extract ID, inject into context>` | Yes / No | `<e.g. Requeue with LongRequeueAfter until project is Active>` |
| … | … | … | … | … |

> **Note on blocking dependencies**: if a required parent resource is not yet Active on the CMP, `HandleReconcile` should return `ctrl.Result{RequeueAfter: LongRequeueAfter}` before reaching `ts.Run`, rather than letting transitions handle the absent parent.

---

## 3. Transition table

List every transition the `TransitionSet` will contain, in evaluation order (top-to-bottom). The timeout safety net must always be first.

| # | Name | KCondition | ACondition | KAction | AAction | KActionOnASuccess | KActionOnAError | Requeue | RequeueOnError | Description |
|---|------|-----------|-----------|---------|---------|------------------|----------------|---------|---------------|-------------|
| 0 | `PhaseTimedOut` | `kubePhaseTimedOut` | `AlwaysTrue` | `kubeSetFailedOnTimeout` | — | — | — | `NoRequeue` | `NoRequeueButIgnoreError` | Safety net: move to Failed if stuck in transitory phase > MaxPhaseTimeout |
| 1 | `ShouldBeDeleted` | `kubeShouldDelete` | `cmp<Resource>IsFinal` | `kubeMarkToDelete` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | DeletionTimestamp set + Active → mark Deleting+ShallSynchronize |
| 2 | `ShouldDeleteTimedOut` | `kubeShouldDeleteTimedOut` | `AlwaysTrue` | `kubeMarkToDelete` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | Timed-out resource with DeletionTimestamp → enter deletion flow |
| 2b *(parent controllers only)* | `WaitingChildrenDeletion` | `kubeShouldBeDeletedOnCMP` + owned K8s children still exist | `cmp<Resource>IsFinal` | `kube<Resource>DeleteOwnedChildren` | — | — | — | `LongRequeue` | `ShortRequeueAndIgnoreError` | Explicitly delete owned K8s children (GC cannot cascade while parent finalizer exists); long requeue until all children are gone, blocking CMP deletion |
| 3 | `ShouldBeDeletedOnCMP` | `kubeShouldBeDeletedOnCMP` | `cmp<Resource>Exists` | — | `cmpDelete` | `kubeMarkDeleting` | `kubeSetErrorMessageOnCMPError` | `ShortRequeue` | `SmartRequeueOnError` | Dispatch CMP delete call |
| 4 | `DeletionOnCMPNotNeeded` | `kubeShouldBeDeletedOnCMP` | `cmp<Resource>NotExists` | `kubeMarkDeletingDone` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | CMP resource already gone; skip delete call |
| 5 | `WaitingDeletionOnCMP` | `kubeWaitingDeletionOnCMP` | `cmp<Resource>Exists` | — | — | — | — | `LongRequeue` | `NoRequeueButIgnoreError` | Poll until CMP confirms deletion |
| 6 | `DeletionConfirmedOnCMP` | `kubeWaitingDeletionOnCMP` | `cmp<Resource>NotExists` | `kubeMarkDeletingDone` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | CMP gone → advance to Synchronized |
| 7 | `DeletionAccomplished` | `kubeDeletionAccomplished` | `cmp<Resource>NotExists` | `kubeMarkDeleted` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | Mark Deleted; base reconciler removes finalizer |
| 8 | `HasDeniedChanges` *(if applicable)* | `kube<Resource>HasDeniedChanges` | `cmp<Resource>IsFinal` | _(return error)_ | — | — | — | `NoRequeue` | `LongRequeueAndIgnoreError` | Surface immutable field violations |
| 9 | `SpecAlreadyInSyncWithCMP` | `kube<Resource>SpecInSyncWithCMP` | `cmp<Resource>Exists` | `kubeSetActiveAndSetID` | — | — | — | `NoRequeue` | `NoRequeueButIgnoreError` | Generation bumped but no real diff; re-stamp ObservedGeneration |
| 10 | `ShouldBeUpdated` | `kube<Resource>ShouldUpdate` | `cmp<Resource>IsFinal` | `kubeMarkToUpdate` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | Spec changed → mark Updating+ShallSynchronize |
| 11 | `ShouldBeUpdatedOnCMP` | `kubeShouldBeUpdatedOnCMP` | `cmp<Resource>IsFinal` | — | `cmpUpdate` | `kubeMarkUpdating` | `kubeSetErrorMessageOnCMPError` | `ShortRequeue` | `SmartRequeueOnError` | Dispatch CMP update call |
| 12 | `WaitingUpdateOnCMP` | `kube<Resource>WaitingUpdateOnCMP` | `cmp<Resource>IsTransitory` | — | — | — | — | `LongRequeue` | `NoRequeueButIgnoreError` | Poll until CMP settles |
| 13 | `UpdateConfirmedOnCMP` | `kube<Resource>UpdateConfirmedOnCMP` | `cmp<Resource>IsFinal` | `kubeMarkUpdatingDone` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | CMP converged → mark Updating+Synchronized |
| 14 | `UpdateAccomplished` | `kubeUpdateAccomplished` | `cmp<Resource>IsActive` | `kubeSetActiveAndSetID` | — | — | — | `NoRequeue` | `NoRequeueButIgnoreError` | Transition back to Active |
| 15 | `ShouldBeCreated` | `kubeIsFirstReconciliation` | `cmp<Resource>NotExists` | `kubeMarkToCreate` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | First reconciliation → mark Creating+ShallSynchronize |
| 16 | `ShouldBeCreatedInCMP` | `kubeShouldBeCreatedOnCMP` | `cmp<Resource>NotExists` | — | `cmpCreate` | `kubeMarkCreating` | `kubeSetErrorMessageOnCMPError` | `ShortRequeue` | `SmartRequeueOnError` | Dispatch CMP create call |
| 17 | `WaitingCreationInCMP` | `kubeWaitingCreationInCMP` | `cmp<Resource>NotExistsOrTransitory` | — | — | — | — | `LongRequeue` | `NoRequeueButIgnoreError` | Poll until CMP resource appears |
| 18 | `CreationConfirmedOnCMP` | `kubeWaitingCreationInCMP` | `cmp<Resource>IsActive` | `kubeMarkCreatingDone` | — | — | — | `ShortRequeue` | `NoRequeueButIgnoreError` | CMP active → mark Creating+Synchronized |
| 19 | `CreationAccomplished` | `kubeIsCreatedOnCMP` | `cmp<Resource>IsActive` | `kubeSetActiveAndSetID` | — | — | — | `NoRequeue` | `NoRequeueButIgnoreError` | Store ResourceID, stamp ObservedGeneration, go Active |
| 20 | `IsInError` *(if CMP has Failed state)* | `AlwaysTrue` | `cmp<Resource>IsFailed` | `kubeSetFailed` | — | — | — | `NoRequeue` | `NoRequeueButIgnoreError` | CMP-side failure → mark K8s resource Failed |

> Remove rows for transitions that do not apply. Add resource-specific transitions as needed.
> Row 2b (`WaitingChildrenDeletion`) only applies to **parent controllers** (Project, VPC, SecurityGroup). Remove it for leaf resources (BlockStorage, KeyPair, ElasticIP, Subnet, CloudServer, SecurityRule).
> The standard `requeueOnError` for CMP-facing transitions (rows 3, 11, 16) is **`SmartRequeueOnError`** — never `LongRequeueAndIgnoreError`. See `CONVENTIONS.md` for the canonical wiring pattern.

### Alternative: update-not-supported rollback

When the CMP provides **no update endpoint**, replace transitions 8–14 above with the following three transitions. The resource visibly enters `Updating`, surfaces a `Failed` condition with a clear message, then has its spec rolled back to the CMP's current state before returning to `Active`.

| # | Name | KCondition | ACondition | KAction | Requeue | RequeueOnError | Description |
|---|------|-----------|-----------|---------|---------|---------------|-------------|
| 8 | `ShouldBeUpdated` | `kubeActiveAndGenerationChanged` | `cmp<Resource>Exists` | `kubeMarkToUpdate` | `ShortRequeue` | `NoRequeueButIgnoreError` | Spec changed → mark `Updating+ShallSynchronize` |
| 9 | `UpdateNotSupported` | `kubeShouldBeUpdatedOnCMP` | `cmp<Resource>Exists` | `kubeMarkUpdatingFailed` | `ShortRequeue` | `NoRequeueButIgnoreError` | Signal that update is not supported: set `Updating+Failed` with error message |
| 10 | `UpdateRollback` | `kube<Resource>UpdatingFailed` | `cmp<Resource>Exists` | `kubeRollbackSpecAndSetActive` | `NoRequeue` | `NoRequeueButIgnoreError` | Restore spec from CMP response (object patch) then set `Active+Synchronized` (status patch) |

**Reference implementation**: `internal/controller/keypair_controller.go` (transitions 8–10).

---

## 4. Component reuse analysis

Identify which pieces already exist and can be reused as-is, and which must be implemented specifically for this resource.

### 4.1 Generic components (reuse from `transition_conditions.go` / `transition_actions.go`)

| Component | Type | Source |
|-----------|------|--------|
| `kubePhaseTimedOut` | KCondition | `transition_conditions.go` |
| `kubeShouldDelete` | KCondition | `transition_conditions.go` |
| `kubeShouldDeleteTimedOut` | KCondition | `transition_conditions.go` |
| `kubeShouldBeDeletedOnCMP` | KCondition | `transition_conditions.go` |
| `kubeWaitingDeletionOnCMP` | KCondition | `transition_conditions.go` |
| `kubeDeletionAccomplished` | KCondition | `transition_conditions.go` |
| `kubeIsFirstReconciliation` | KCondition | `transition_conditions.go` |
| `kubeShouldBeCreatedOnCMP` | KCondition | `transition_conditions.go` |
| `kubeWaitingCreationInCMP` | KCondition | `transition_conditions.go` |
| `kubeIsCreatedOnCMP` | KCondition | `transition_conditions.go` |
| `kubeShouldBeUpdatedOnCMP` | KCondition | `transition_conditions.go` |
| `kubeWaitingUpdateOnCMP` | KCondition | `transition_conditions.go` |
| `kubeUpdateAccomplished` | KCondition | `transition_conditions.go` |
| `kubeActiveAndGenerationChanged` | KCondition (helper) | `transition_conditions.go` |
| `AlwaysTrue` | KCondition / ACondition | `transition.go` |
| `setPhaseAndCondition` | KAction (helper) | `transition_actions.go` |
| `setActiveAndSetID` | KAction (helper) | `transition_actions.go` |
| `setFailedOnTimeout` | KAction (helper) | `transition_actions.go` |
| `kubeSetErrorMessageOnCMPError` | KActionOnAError — surfaces CMP error details in condition without changing phase/reason; standard wiring for all CMP-facing transitions | `transition_actions.go` |
| `hasOwnedChildren` | helper — lists namespace objects across multiple list types, filters by OwnerReference UID | `owner_reference.go` |
| `deleteOwnedChildren` | helper — issues `c.Delete()` on each owned child not yet being deleted; used in `WaitingChildrenDeletion` kAction *(parent controllers only)* | `owner_reference.go` |
| `resolveOwnerObject` | helper — fetches the parent K8s object by `ResourceReference` | `owner_reference.go` |
| `ensureOwnerReference` | helper — idempotently sets `controllerutil.SetControllerReference`; returns `(needsRequeue=true, nil)` on first set | `owner_reference.go` |
| `ShortRequeue`, `LongRequeue`, `NoRequeue` | Requeue | `transition.go` |
| `NoRequeueButIgnoreError`, `LongRequeueAndIgnoreError`, `ShortRequeueAndIgnoreError` | RequeueOnError | `transition.go` |
| `SmartRequeueOnError` | RequeueOnError — `ShortRequeue` for technical (5xx/transport) errors, `LongRequeue` for semantic (4xx) errors; **standard wiring for all CMP-facing transitions** | `transition.go` |

### 4.2 Resource-specific components to implement

| Component | Type | Notes |
|-----------|------|-------|
| `cmp<Resource>Exists` | ACondition | CMP response is non-nil |
| `cmp<Resource>NotExists` | ACondition | CMP response is nil |
| `cmp<Resource>IsFinal` | ACondition | CMP state nature is Final (uses `AssessCSPResourceStateNature`) |
| `cmp<Resource>IsTransitory` | ACondition | CMP state nature is Transitory |
| `cmp<Resource>IsActive` | ACondition | CMP state is one of the active/usable states |
| `cmp<Resource>IsFailed` | ACondition *(if applicable)* | CMP state == `Failed` |
| `cmp<Resource>NotExistsOrTransitory` | ACondition | nil or transitory (used in WaitingCreationInCMP) |
| `kube<Resource>HasDeniedChanges` | KCondition *(if applicable)* | detects immutable field violations |
| `kube<Resource>SpecInSyncWithCMP` | KCondition | generation changed but no actual diff |
| `kube<Resource>ShouldUpdate` | KCondition | generation changed + real diff |
| `kube<Resource>WaitingUpdateOnCMP` | KCondition | Updating+Synchronizing + spec still differs |
| `kube<Resource>UpdateConfirmedOnCMP` | KCondition | Updating+Synchronizing + spec converged |
| `checkImmutableChanges` | helper *(if applicable)* | returns error listing which immutable fields changed |
| `kube<Resource>NeedsUpdate` | helper | compares K8s spec fields to CMP response fields |
| `cmpCreate` | AAction | build and dispatch CMP create request |
| `cmpUpdate` | AAction | build and dispatch CMP update request |
| `cmpDelete` | AAction | dispatch CMP delete request |
| `cmp<Resource>RequestFromKube` | helper | build CMP create request from K8s spec |
| `cmp<Resource>RequestFromCMP` | helper | build CMP update request seeded from current CMP state (preserve CMP-managed fields) |
| `kubeMarkToCreate` / `kubeMarkCreating` / `kubeMarkCreatingDone` | KAction wrappers | thin wrappers over `setPhaseAndCondition` |
| `kubeMarkToUpdate` / `kubeMarkUpdating` / `kubeMarkUpdatingDone` | KAction wrappers | thin wrappers over `setPhaseAndCondition` |
| `kubeMarkToDelete` / `kubeMarkDeleting` / `kubeMarkDeletingDone` / `kubeMarkDeleted` | KAction wrappers | thin wrappers over `setPhaseAndCondition` |
| `kubeSetActiveAndSetID` | KAction wrapper | thin wrapper over `setActiveAndSetID` |
| `kubeSetFailedOnTimeout` | KAction wrapper | thin wrapper over `setFailedOnTimeout` |
| `kubeSetFailed` *(if applicable)* | KAction wrapper | thin wrapper over `setPhaseAndCondition` for CMP-driven failures |

**Additional components for parent controllers** *(only when this resource owns other K8s resources — see ownership graph in `ai/ARCHITECTURE.md`)*:

| Component | Type | Notes |
|-----------|------|-------|
| `kube<Resource>HasOwnedChildren` | method on reconciler — closes over `r.Client`, calls `hasOwnedChildren` with all child list types; used as inline `kCondition` in `WaitingChildrenDeletion` | implement on the reconciler struct |
| `kube<Resource>DeleteOwnedChildren` | `ActionFunc[K, A]` method on reconciler — closes over `r.Client`, calls `deleteOwnedChildren` with all child list types; used as `kAction` in `WaitingChildrenDeletion` | implement on the reconciler struct |

---

**Additional components for the update-not-supported rollback pattern** *(include these instead of the standard update components when the CMP has no update API)*:

| Component | Type | Notes |
|-----------|------|-------|
| `kubeMarkUpdatingFailed` | KAction wrapper | wraps `setPhaseAndCondition` with `phase=Updating`, `reason=Failed`, resource-specific error message |
| `kube<Resource>UpdatingFailed` | KCondition | checks `phase == Updating` AND condition `Reason == Failed`; needed to distinguish from other Updating sub-states |
| `kubeRollbackSpecAndSetActive` | KAction | two-step: (1) object patch restoring mutable spec fields from CMP response via `retry.RetryOnConflict`; (2) `setActiveAndSetID` to write `Active+Synchronized` and stamp the new `ObservedGeneration` |

> Update this table after the component analysis: mark items that turn out to be reusable from another controller as "reuse from `<resource>_controller.go`".

---

## 5. Implementation

Work through each sub-step in order. Do not proceed to the next sub-step until the current one and its tests pass.

### 5.1 CRD type definition

- [ ] **New controller only**: create `api/v1alpha1/<resource>_types.go` defining `<Resource>Spec`, `<Resource>Status` (embedding `ResourceStatus`), `<Resource>`, and `<Resource>List`.
- [ ] **Refactor**: verify the existing type is complete and correct; update if needed.
- [ ] Run `make manifests-ctzd generate-ctzd` and confirm no diff errors.

### 5.2 Controller scaffold

- [ ] Create (or rewrite) `internal/controller/<resource>_controller.go`:
  - Struct `<Resource>Reconciler` embedding `*reconciler.Reconciler` and holding `ts *TransitionSet[*v1alpha1.<Resource>, *arubatypes.<CMPType>]`
  - `Object()`, `Finalizer()`, `Reconcile()` (delegates to base), `SetupWithManager()`
  - `HandleReconcile()`:
    1. Resolve tenant client: `arubaClient, err := r.ArubaClient(kubeObj.Spec.Tenant)` → inject into context: `ctx = context.WithValue(ctx, reconciler.ArubaClientKey, arubaClient)`
    2. Validate required spec references (return error immediately if empty)
    3. **Set OwnerReference *(child controllers only)***: if `DeletionTimestamp.IsZero()`, call `resolveOwnerObject` to fetch the parent K8s object by `Spec.<Parent>Reference`, then `ensureOwnerReference`. If `ensureOwnerReference` returns `(true, nil)`, return `ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}` immediately. Skip silently if the parent K8s object is not found (`apierrors.IsNotFound`). See `owner_reference.go` for the pattern used by all existing child controllers.
    4. Resolve parent references (if any) → inject parent IDs into context
    5. Fetch CMP resource → validate cardinality → pass to `ts.Run()`
- [ ] **Parent controllers only** — `SetupWithManager`: register `Watches()` for each owned child type using `childToParentMapFunc`. Do **not** use `Owns()` — this operator does not set K8s OwnerReferences, so `Owns()` would never trigger. Add `delete` verb to RBAC markers for each child resource:
  ```go
  // +kubebuilder:rbac:groups=arubacloud.com,resources=<children>,verbs=get;list;watch;delete
  func (r *<Resource>Reconciler) SetupWithManager(mgr ctrl.Manager) error {
      return ctrl.NewControllerManagedBy(mgr).
          For(&v1alpha1.<Resource>{}).
          Watches(&v1alpha1.<Child>{}, handler.EnqueueRequestsFromMapFunc(
              childToParentMapFunc(func(o client.Object) *v1alpha1.ResourceReference {
                  if v, ok := o.(*v1alpha1.<Child>); ok {
                      return &v.Spec.<Resource>Reference
                  }
                  return nil
              }))).
          Named("<resource>").
          Complete(r)
  }
  ```
- [ ] Write tests for `HandleReconcile` covering: dependency not yet ready (requeue), CMP fetch error, cardinality error, happy path (delegates to ts).

### 5.3 TransitionSet

#### 5.3.1 KConditions

Implement all resource-specific KConditions listed in §4.2.

- [ ] `kube<Resource>HasDeniedChanges` + test
- [ ] `kube<Resource>SpecInSyncWithCMP` + test
- [ ] `kube<Resource>ShouldUpdate` + test
- [ ] `kube<Resource>WaitingUpdateOnCMP` + test
- [ ] `kube<Resource>UpdateConfirmedOnCMP` + test
- [ ] `checkImmutableChanges` helper + test *(if applicable)*

#### 5.3.2 AConditions

Implement all resource-specific AConditions listed in §4.2.

- [ ] `cmp<Resource>Exists` + test
- [ ] `cmp<Resource>NotExists` + test
- [ ] `cmp<Resource>IsFinal` + test
- [ ] `cmp<Resource>IsTransitory` + test
- [ ] `cmp<Resource>IsActive` + test
- [ ] `cmp<Resource>IsFailed` + test *(if applicable)*
- [ ] `cmp<Resource>NotExistsOrTransitory` + test

#### 5.3.3 KActions

- [ ] `kubeMarkToCreate` / `kubeMarkCreating` / `kubeMarkCreatingDone` + tests
- [ ] `kubeMarkToUpdate` / `kubeMarkUpdating` / `kubeMarkUpdatingDone` + tests
- [ ] `kubeMarkToDelete` / `kubeMarkDeleting` / `kubeMarkDeletingDone` / `kubeMarkDeleted` + tests
- [ ] `kubeSetActiveAndSetID` + test
- [ ] `kubeSetFailedOnTimeout` + test
- [ ] `kubeSetFailed` + test *(if applicable)*

#### 5.3.4 AActions

AActions receive `ctx` and must extract the `aruba.Client` from it:

```go
func (r *<Resource>Reconciler) cmpCreate(ctx context.Context, kube *v1alpha1.<Resource>, _ *arubatypes.<CMPType>) error {
    arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
    // use arubaClient.From<…>()...
}
```

- [ ] `cmpCreate` (using `cmp<Resource>RequestFromKube`) + test
- [ ] `cmpUpdate` (using `cmp<Resource>RequestFromCMP` seeded, then overwrite mutable fields) + test
- [ ] `cmpDelete` + test

#### 5.3.5 KActionOnASuccess

All `KActionOnASuccess` entries in this controller are existing KAction wrappers (`kubeMarkCreating`, `kubeMarkUpdating`, `kubeMarkDeleting`). Confirm they are correctly wired — no new implementations needed unless a resource-specific one is required:

- [ ] _<list any resource-specific KActionOnASuccess here, or strike through if none>_

#### 5.3.6 KActionOnAError

Currently no standard `KActionOnAError` is used in the reference implementations. Add entries here if this resource requires rollback or error-status writes on AAction failure:

- [ ] _<list any KActionOnAError here, or strike through if none>_

#### 5.3.7 Compose the TransitionSet

- [ ] Implement `newTransitionSet()` wiring all transitions from §3 in the correct order.
- [ ] Write controller-level integration tests covering the full lifecycle:
  - Create flow (first reconciliation → CMP create → polling → Active)
  - Update flow (spec change → CMP update → polling → Active) *(standard update)*
  - Update with immutable field change → error surfaced, no CMP call *(if applicable)*
  - Update-not-supported rollback *(if CMP has no update API)*:
    - Spec change → `Updating+ShallSynchronize` (`ShouldBeUpdated`)
    - Next reconcile → `Updating+Failed` with error message (`UpdateNotSupported`)
    - Next reconcile → spec fields restored to CMP values + `Active+Synchronized` (`UpdateRollback`)
  - Delete flow (DeletionTimestamp → CMP delete → polling → Deleted → finalizer removed)
  - Timeout detection (transitory phase exceeds MaxPhaseTimeout → Failed)
  - CMP-side failure detection *(if applicable)*

### 5.4 Register the controller

- [ ] Add `<Resource>Reconciler` setup in `cmd/main.go`.
- [ ] Run `make test-ctzd` and confirm all tests pass.
- [ ] Run `make lint-ctzd` and fix any issues.

---

## 6. Manual testing

Use `test/scripts/test_runner.sh` to apply and delete resources against a live cluster (see `ai/DEVEX.md` for usage).

Create or identify a fixture file for this resource, then exercise the scenarios below. Check each off as you confirm the expected behaviour.

| # | Scenario | Fixture / command | Expected outcome |
|---|----------|-------------------|-----------------|
| 1 | Resource created successfully | `NN=<N> ACTION=apply …` | Phase reaches `Active`; `ResourceID` is set in status |
| 2 | Resource deleted cleanly | `NN=<N> ACTION=delete …` | Phase reaches `Deleted`; finalizer removed; CMP resource gone |
| 3 | Spec update (mutable field) | Edit the resource, re-apply | Phase cycles `Active → Updating → Active`; CMP reflects change |
| 3b | Spec update (no CMP update API) *(if applicable)* | Edit any spec field, re-apply | Phase cycles `Active → Updating (ShallSynchronize) → Updating (Failed) → Active`; spec rolled back to CMP values; `Updating+Failed` condition visible briefly |
| 4 | Spec update (immutable field) *(if applicable)* | Edit an immutable field, re-apply | Phase stays `Active`; status message describes the rejected change |
| 5 | Dependency not yet ready | Apply before parent resource is Active | Controller requeues; eventually reaches `Active` once parent is ready |
| 6 | CMP-side failure *(if applicable)* | Trigger a CMP failure (e.g. invalid config) | Phase moves to `Failed`; status message reflects CMP error |
| 7 | Timeout detection | Force resource into transitory phase and wait | Phase moves to `Failed` after `MaxPhaseTimeout` (10 min) |

> Add resource-specific scenarios as needed.
