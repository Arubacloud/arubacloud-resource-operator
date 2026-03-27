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

**Status**: **Resolved** — see `ai/plans/cross-namespace_cascade_delete.md`.

**Resolution**: Standard Kubernetes OwnerReferences have been replaced entirely by a two-layer custom ownership model:

1. **`arubacloud.com/owner-references` annotation** — JSON `[]ArubaOwnerReference` storing the full owner identity including `Namespace`. Source of truth. Set by `ensureOwnerReference` via `setArubaControllerReference`.
2. **`arubacloud.com/owner-<kind>` label** — UID-valued label for efficient cluster-wide label-selector queries in `hasOwnedChildren` / `deleteOwnedChildren`.

`Owns()` watches in `SetupWithManager` have been replaced by `Watches()` with `childToParentMapFunc`, which reads the parent reference from the child's spec and works for both same and cross-namespace relationships.

Cross-namespace children participate fully in cascade deletion. No special cases.

---

## 5. CMP API Does Not Distinguish Dependency Errors from Permanent Errors

**Context**: When the CMP API rejects a deletion because dependent resources still exist (e.g., deleting a Project with active VPCs), it returns a generic HTTP 4xx error. The error response does not use a distinct status code or error code to differentiate "has dependencies" from other semantic errors (invalid request, permission denied, etc.).

**Current mitigation**: All CMP 4xx errors are classified as `CMPErrorCategorySemantic` and retried with `LongRequeueAfter`. The `WaitingChildrenDeletion` guard prevents most dependency-related CMP errors by ensuring children are deleted first. For cases where the guard is insufficient (e.g., CMP-side resources not managed by the operator), the retry loop eventually succeeds once dependencies are manually cleared.

**Potential future solutions**:
- Work with the CMP API team to introduce a specific error code for dependency violations (e.g., HTTP 409 Conflict with a structured error body)
- Parse CMP error messages to detect dependency-related keywords and surface a more specific condition message to the user
- Implement a CMP-side dependency check before attempting deletion: query the CMP for child resources and surface a clear message if any remain

---

## 6. Ownership Setup Adds a K8s API Call Per Reconciliation

**Context**: Each child controller's `HandleReconcile` fetches the parent K8s object (via `resolveOwnerObject`) to set the ownership annotation and label. This is a local API server call but adds latency to every reconciliation loop, even after the metadata is already set.

**Current mitigation**: The `ensureOwnerReference` helper is idempotent — if both the annotation and label are already present and correct, it returns immediately without patching. The `resolveOwnerObject` call (a `Get` by name) is lightweight. The extra latency is negligible compared to CMP API calls.

**Potential future solutions**:
- Skip the parent K8s `Get` call if the child's annotation already contains an entry for the expected parent UID (check locally before fetching). This trades consistency (won't detect parent UID changes after delete/recreate) for performance.
- Move ownership setup to a one-time init step (e.g., only during `Creating` phase) instead of every reconciliation. This loses the self-healing property but eliminates ongoing overhead.
