# Known Issues

This document tracks edge cases, limitations, and open problems in the operator that are not yet fully resolved. Each entry describes the issue, its impact, and potential future solutions.

---

## 1. Cascade Delete: Concurrent Sibling Deletion Order Not Guaranteed

**Context**: When a parent resource is deleted (e.g., Project), its `WaitingChildrenDeletion` action explicitly calls `c.Delete()` on all direct children simultaneously. Children that share infrastructure dependencies may encounter transient CMP API failures.

**Example**: Deleting a Project triggers simultaneous deletion of CloudServer and VPC. VPC's `WaitingChildrenDeletion` guard holds VPC's CMP deletion until its children (Subnet, SecurityGroup) are deleted. Meanwhile, CloudServer's CMP deletion may temporarily fail if the CMP rejects deleting a server whose Subnet or SecurityGroup is in a transitory "deleting" state.

**Current mitigation**: The existing `SmartRequeueOnError` retry loop handles CMP 4xx errors with `LongRequeueAfter` (20s). CloudServer's CMP delete will eventually succeed once its referenced resources reach a compatible state, but may produce transient error log entries.

**Potential future solutions**:
- Add an explicit ordering mechanism: delete CloudServers before VPCs by making VPC's `WaitingChildrenDeletion` also check for CloudServers in the same Project (cross-ownership dependency awareness)
- Alternatively, make CloudServer owned by VPC instead of Project, which would naturally serialize the deletion (but changes ownership semantics)

---

## 2. Cascade Delete: Stuck Child Blocks Parent Deletion Indefinitely

**Context**: The `WaitingChildrenDeletion` transition causes a parent to wait until all its K8s children are fully deleted (finalizers removed, objects garbage-collected). If a child gets stuck in `Failed` phase and its CMP deletion cannot complete, the parent waits forever.

**Example**: A SecurityRule's CMP deletion fails permanently (e.g., CMP returns 500 repeatedly). SecurityGroup's `WaitingChildrenDeletion` blocks indefinitely. VPC's deletion is also blocked (waiting for SecurityGroup). Project's deletion is blocked (waiting for VPC). The entire branch is stuck.

**Current mitigation**: The `PhaseTimedOut` transition moves stuck children to `Failed` after `MaxPhaseTimeout` (10 min), but a `Failed` child still has its finalizer — it does not auto-resolve. Manual intervention is required: either fix the CMP issue and re-trigger reconciliation, or manually remove the child's finalizer to unblock the cascade.

**Potential future solutions**:
- Implement a configurable "force delete" policy: after a child has been stuck in `Deleting+Failed` for a threshold (e.g., 30 min), automatically remove its finalizer and allow GC to proceed. This risks leaving orphaned CMP resources but unblocks the cascade
- Add operator-level alerts/events when a child blocks parent deletion for more than N minutes, so operators are notified before the situation becomes critical
- Expose a `spec.deletionPolicy` field (e.g., `Cascade`, `CascadeForce`, `Orphan`) to let users control the behavior per resource

---

## 3. Cascade Delete: envtest Does Not Run the Kubernetes Garbage Collector

**Context**: Integration tests use `controller-runtime/envtest`, which provides a fake K8s API server but does **not** run the Kubernetes garbage collector controller. This means that even after a parent is fully removed from etcd, owned children are **not** automatically deleted by the GC in tests.

Note: in production this is less of an issue than originally thought, because the `WaitingChildrenDeletion` kAction explicitly deletes children rather than relying on the GC (see ARCHITECTURE.md). The GC is still needed to clean up children after the parent finalizer is removed, but the critical cascade trigger (issuing the first `c.Delete()` on children) is handled by the controller itself.

**Impact**: The full cascade delete flow cannot be tested end-to-end in integration tests. Tests must either:
- Manually delete child resources to simulate what the GC would do for the final cleanup step
- Test the `WaitingChildrenDeletion` transition in isolation (verify it issues deletes when children exist and unblocks when children are gone)
- Rely on e2e tests against a real cluster for full cascade validation

**Potential future solutions**:
- Implement a lightweight GC simulator in the test suite that watches for deletions and propagates `DeletionTimestamp` to owned children
- Use [envtest with a full control plane](https://github.com/kubernetes-sigs/controller-runtime/issues/1571) if/when the feature becomes available
- Accept the limitation and ensure e2e tests comprehensively cover the cascade flow

---

## 4. Cross-Namespace OwnerReferences Not Supported

**Status**: **Resolved**.

**Resolution**: Standard Kubernetes OwnerReferences have been replaced entirely by a two-layer custom ownership model:

1. **`arubacloud.com/owner-references` annotation** — JSON `[]ArubaOwnerReference` storing the full owner identity including `Namespace`. Source of truth. Set by `ensureOwnerReference` via `setArubaControllerReference`.
2. **`arubacloud.com/owner-<kind>` label** — UID-valued label for efficient cluster-wide label-selector queries in `hasOwnedChildren` / `deleteOwnedChildren`.

`Owns()` watches in `SetupWithManager` have been replaced by `Watches()` with `childToParentMapFunc`, which reads the parent reference from the child's spec and works for both same and cross-namespace relationships.

Cross-namespace children participate fully in cascade deletion. No special cases.

---

## 5. CMP API Does Not Distinguish Dependency Errors from Permanent Errors

**Status**: **Partially resolved** (SDK v1.0.4).

**Context**: When the CMP API rejects a deletion because dependent resources still exist (e.g., deleting a Project with active VPCs), it returns a generic HTTP 4xx error. The error response does not use a distinct status code or error code to differentiate "has dependencies" from other semantic errors (invalid request, permission denied, etc.).

**Current state**: The SDK `ErrorResponse` (surfaced through `aruba.HTTPError.ErrResp`) includes an `Errors []ValidationError` array for field-level validation failures. `CMPErrorFromResult` uses this to split the former single `CMPErrorCategorySemantic` into two categories:
- `CMPErrorCategorySemantic` — 4xx with non-empty `Errors` (true validation failure; moves resource to `Failed+ValidationFailed` during Creating/Updating).
- `CMPErrorCategoryTransient` — 4xx with empty `Errors` (temporary condition, e.g. dependency in wrong state; surfaces error in condition, long-requeue without phase change).

This means dependency-related 4xx errors are now correctly classified as Transient and do not prematurely move resources to `Failed+ValidationFailed`.

**Resolved**: Two complementary fixes fully close the deletion-stuck gap:
1. `ValidationFailedAndDeleting` transition (transition #1 in all controllers) — unblocks resources stuck in `Failed+*ValidationFailed` when deletion is requested, resetting the phase to allow normal deletion flow.
2. `SmartRequeueOnError` phase-awareness — during the `Deleting` phase, semantic CMP errors (4xx with validation details) now always requeue with `LongRequeueAfter` instead of returning no-requeue. This prevents resources from getting permanently stuck in `Deleting+ShallSynchronize` when the CMP rejects the delete call with a temporary dependency error.

**Potential future solution** (if finer-grained error handling is needed):
- Work with the CMP API team to introduce a specific error code for dependency violations (e.g., HTTP 409 Conflict with a structured error body), allowing the operator to distinguish "retry later" from "permanent failure" during deletion.

---

## 6. SDK Leaks `pkg/types` Through Two High-Level Values

**Context**: The operator deliberately imports only `github.com/Arubacloud/sdk-go/pkg/aruba` and never `pkg/types` (see `ai/CONVENTIONS.md`). Two fields the operator needs have no high-level accessor, so they are read by reaching through a `pkg/aruba` value into the `pkg/types` struct behind it:

1. `aruba.HTTPError.ErrResp` is a bare `*types.ErrorResponse`. `cmpResponseError` reads its `Title`/`Detail`/`Instance`/`Errors` to categorize the error and build the condition message.
2. `aruba.SecurityGroup` has no `regionalMixin` and therefore no `Region()`, so `checkSecurityGroupDeniedChanges` reads `sg.Raw().Metadata.LocationResponse.Value` — the same field the SDK's own `regionalMixin` is hydrated from for VPC and Subnet.

**Impact**: Both compile without the import, so the rule holds in letter, but the structural dependency is real and **invisible**: an upstream field rename breaks these files with no import line to grep for. The `ErrResp` case also blocks the natural refactor seam — a `formatValidationErrors([]types.ValidationError) string` helper cannot be written because its parameter type cannot be named.

**Current mitigation**: The validation-formatting logic is kept testable by copying the two fields it needs into a local `cmpValidationError` struct at the boundary and putting the logic behind `appendValidationDetail`, which has direct unit tests. The reach-through is reduced to a two-field copy. The SecurityGroup region read is nil-guarded (`Raw()`, `LocationResponse`, and empty `Value`) and annotated in place.

**Potential future solutions**:
- **Preferred (upstream)**: have `aruba.HTTPError` expose validation entries through a `pkg/aruba`-owned shape (e.g. `ValidationErrors() []aruba.ValidationError`), so consumers honoring the single-import principle never touch `pkg/types`. Worth filing against sdk-go — it defeats the principle exactly where every consumer needs it.
- **Upstream**: add `regionalMixin` to `aruba.SecurityGroup` (its response already carries `Metadata.LocationResponse`), removing reach-through #2 entirely.
- Re-check both sites on every SDK bump; if a high-level accessor has appeared, switch to it and delete the reach-through.

---

## 7. Ownership Setup Adds a K8s API Call Per Reconciliation

**Context**: Each child controller's `HandleReconcile` fetches the parent K8s object (via `resolveOwnerObject`) to set the ownership annotation and label. This is a local API server call but adds latency to every reconciliation loop, even after the metadata is already set.

**Current mitigation**: The `ensureOwnerReference` helper is idempotent — if both the annotation and label are already present and correct, it returns immediately without patching. The `resolveOwnerObject` call (a `Get` by name) is lightweight. The extra latency is negligible compared to CMP API calls.

**Potential future solutions**:
- Skip the parent K8s `Get` call if the child's annotation already contains an entry for the expected parent UID (check locally before fetching). This trades consistency (won't detect parent UID changes after delete/recreate) for performance.
- Move ownership setup to a one-time init step (e.g., only during `Creating` phase) instead of every reconciliation. This loses the self-healing property but eliminates ongoing overhead.

---

## 8. CloudServer Data-Volume Attachment Is Unimplemented

**Context**: `api/v1alpha1/cloudserver_types.go` declares `Spec.DataVolumeReferences` (input) and `Status.DataVolumeIDs` (output), but **no controller code reads or populates either** — `grep -rn DataVolume internal/` returns nothing, on this branch and on `main`. The CloudServer controller silently ignores `spec.dataVolumeReferences` and never sets `status.dataVolumeIDs`. Only the boot volume (`Spec.BootVolumeReference` → `Status.BootVolumeID`) is wired up.

**Impact**: A CloudServer referencing data volumes still reaches `Active`, but the volumes are not attached and the status field stays empty. The e2e suite's `08-ComputeWithDataVolumes` asserts `status.dataVolumeIDs` is non-empty, so it fails at that step; the assertion is currently disabled with a TODO pointing here. (This is a pre-existing feature gap, not a regression from the SDK v1.0.4 migration.)

**Potential future solutions**:
- Implement attachment in the CloudServer controller: resolve `DataVolumeReferences` to CMP volume IDs, attach each to the server via the SDK, and populate `Status.DataVolumeIDs` on success. Re-enable the `08` assertion once it lands.
- If data volumes are out of scope, drop the two CRD fields and the `08` assertion rather than leaving dead API surface.

---

## 9. No Adoption Path: A Resource Stuck in `Pending` With an Existing CMP Resource Stalls Forever

**Context**: A resource enters `Pending` on first reconcile (finalizer set). `ShouldBeCreated` fires only when the CMP resource does **not** exist (`cmp*NotExists`), and `CreationAccomplished` fires only from phase `Creating`. If the CMP resource **already exists** while the K8s resource is still `Pending` — for example a leftover from an interrupted run, or a manually re-created CR pointing at existing infrastructure — **no transition matches**. The `TransitionSet` default is `NoRequeue`, so the loop returns with no error, no requeue, and no phase change.

**Impact**: The resource sits in `Pending` indefinitely with no diagnostic. Reconciliation genuinely stops (verified: two loops, then silence). Any flow that lands a CR in `Pending` next to a pre-existing CMP resource is stuck until a human intervenes.

**Current mitigation**: Manually patch the status to phase `Creating` / reason `Synchronized` with an empty `resourceID`; that satisfies `KubeIsCreatedOnCMP`, so `CreationAccomplished` then adopts the existing CMP resource and stamps its ID (verified working).

**Potential future solutions**:
- Add an adoption transition: when the K8s resource is `Pending` and the CMP resource exists, move to `Creating`/adopt rather than falling through to the no-op default.
- Make the `TransitionSet` default requeue non-silent (short requeue + a log line) so a "no transition matched" state is at least observable instead of a permanent silent stall.

---

## 10. Deleting a `Pending` Resource Orphans Its CMP Counterpart

**Context**: The `PendingAndDeleting` transition uses `KubeDeleteFromPending`, which sets the phase to `Deleted` and removes the finalizer **without issuing any CMP delete** — on the assumption that a `Pending` resource never provisioned anything. That assumption is false whenever a CMP resource exists behind a `Pending` K8s resource (see issue #9): the create may have succeeded on the CMP while the status write lagged or the run was interrupted.

**Impact**: Deleting such a resource makes the Kubernetes object disappear cleanly while its CMP counterpart is silently orphaned — billable, and no longer tracked by any CR. This is the exact mechanism by which interrupted e2e runs left CMP resources behind.

**Potential future solutions**:
- Before the finalizer is dropped from a `Pending` resource, do a CMP existence check (the controllers already list by name); if the resource exists, route through the normal CMP-delete flow instead of `KubeDeleteFromPending`.

---

## 11. Untracked CMP Sub-Resources Block Parent Deletion

**Context**: After an interrupted `07`/`08` run, deleting every K8s-tracked resource under a project (CloudServer, volumes, KeyPair, ElasticIP, Subnet, SecurityRules, SecurityGroup, VPC — all confirmed removed from the CMP) still left the project stuck in `Deleting`, with the CMP returning `project can't be deleted due to the presence of resources`. Something remained under the project on the CMP that no Kubernetes CR represented, so the operator had no handle to delete it.

**Impact**: Project deletion blocks indefinitely on a resource the operator cannot see or remove; only manual console cleanup clears it. The specific untracked resource was not definitively identified (a CloudServer-created volume is the leading candidate), which is itself part of the problem — nothing surfaces what is still holding the project open.

**Potential future solutions**:
- Have the operator surface the CMP's list of blocking child resources on the parent's condition when a delete is rejected for "presence of resources", so the untracked resource is at least identifiable.
- Track every CMP resource the CloudServer flow creates (notably volumes) under an owning CR so cascade delete can reach them; relates to issues #8 and #10.
