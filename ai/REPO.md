# Repository Structure

## Top-level layout

| Path | Purpose |
|------|---------|
| `api/v1alpha1/` | CRD type definitions |
| `cmd/main.go` | Operator entry point and manager setup |
| `internal/reconciler/` | Base reconciliation loop |
| `internal/controller/` | Resource-specific controllers and transition system |
| `internal/config/` | Config loading from ConfigMap + Secret |
| `internal/mocks/` | Mockery-generated mocks (do not edit manually) |
| `internal/util/` | Shared utilities |
| `config/crd/` | Generated CRD manifests (committed, updated by `make manifests`) |
| `config/samples/` | Example CR manifests used in manual and e2e testing |
| `test/scripts/` | Manual testing tooling (`test_runner.sh` + fixtures) |
| `devex/build/` | Containerized development environment (`Dockerfile` for the devtools image) |

## Package details

### `api/v1alpha1/`

Defines all Custom Resource types and the shared status model.

- `common_types.go` — `ResourceStatus`, `ResourcePhase`, condition reason constants (`ShallSynchronize`, `Synchronizing`, `Synchronized`, `Failed`), `ResourcePhaseNature` (Transitory / Final), `ResourceReference`, and `ArubaOwnerReference`.
- `<resource>_types.go` — one file per CRD (Project, BlockStorage, CloudServer, ElasticIP, KeyPair, SecurityGroup, SecurityRule, Subnet, VPC); each contains the `Spec`, `Status`, and list type for that resource. Flat scalar fields are used for `Region string`, `Zone string`, `SizeGB int`, `BillingPeriod string`, and `CIDR string` — no nested structs for these.
- `zz_generated.deepcopy.go` — generated; do not edit.

### `internal/reconciler/`

The complete reconciliation framework. Contains:

- `reconciler.go` — the shared three-step loop (`Reconcile`), the `ResourceReconciler` interface that every controller must implement, the `ResourceObject` constraint interface, `ReconcilerConfig`, and the timing constants (`ShortRequeueAfter`, `LongRequeueAfter`, `MaxPhaseTimeout`).
- `metrics.go` — the `aruba_reconcile_step_duration_seconds` Prometheus histogram, registered with the controller-runtime metrics registry; provides `getResourceKind`, `getPhaseAndReason`, and `observeStep` helpers used to instrument each reconciliation step.
- `transition.go` — the generic state-machine types: `AbstractTransition[K,A]` (concrete struct with exported function-pointer fields), `TransitionSet[K,A]` (ordered list + default fallback), type aliases `ConditionFunc`, `ActionFunc`, `ActionOnErrorFunc`, `RequeueFunc`, `RequeueOnErrorFunc`, and all requeue helpers (`ShortRequeue`, `LongRequeue`, `NoRequeue`, `SmartRequeueOnError`, `LongRequeueAndIgnoreError`, etc.).
- `transition_conditions.go` — all reusable `ConditionFunc[K,A]` implementations (e.g. `KubeIsFirstReconciliation`, `KubeShouldBeCreatedOnCMP`, `KubePhaseTimedOut`, `KubeShouldDelete`). Unexported helpers `kubeHasPhaseAndReason` and `failedPhase` are used internally.
- `transition_actions.go` — all reusable status-patch helpers: `SetPhaseAndCondition`, `SetActiveAndSetID`, `SetFailedOnTimeout`, `KubeSetErrorMessageOnCMPError`, and `TagsAreEqual`. These use `retry.RetryOnConflict` internally and are the canonical way to write back Kubernetes status.
- `cmp_error.go` — CMP error types: `CMPError` struct, `CMPErrorCategory` enum (Semantic/Technical), `CMPTransportError` and `cmpResponseError` constructors, `CMPCheckResponse[T]` generic response checker, and `CMPErrorIsSemantic`/`CMPErrorIsTechnical` helpers.
- `common.go` — CMP (Aruba-side) state constants (`CSPResourceState*`) and `AssessCSPResourceStateNature` for classifying CMP states as Transitory/Final.

Key elements of the `Reconciler` struct:
- Private `multiTenantClient arubamt.Multitenant` — thread-safe cache of `aruba.Client` per tenant, initialized in `NewReconciler()`.
- Private `config ReconcilerConfig` — holds all credential and endpoint configuration for deferred client creation.
- Method `ArubaClient(tenant string) (aruba.Client, error)` — lazily creates and caches a tenant-scoped SDK client.
- Exported context key `ArubaClientKey` (type `contextKey`) — used by controllers to pass the resolved client through `context.WithValue`.
- `NewReconcilerForTest(k8sClient, scheme, mtClient)` — test-only constructor that bypasses `ctrl.Manager` and real credentials; accepts a pre-seeded `arubamt.Multitenant` so mock clients can be injected per tenant.

### `internal/controller/`

One file per concern:

- `<resource>_controller.go` — embeds `*reconciler.Reconciler`, wires a `reconciler.TransitionSet`, and implements `Object()`, `Finalizer()`, `HandleReconcile()`.
- `owner_reference.go` — custom two-layer ownership system (annotation + label, replacing standard K8s OwnerReferences): `resolveOwnerObject`, `ensureOwnerReference`, `setArubaControllerReference`, `parseArubaOwnerReferences`, `marshalArubaOwnerReferences`, `hasArubaOwnerReference`, `ownerLabelKey`, `hasOwnedChildren`, `deleteOwnedChildren`, `childToParentMapFunc`. Used by all child controllers in `HandleReconcile` (to set ownership metadata) and by parent controllers in the `WaitingChildrenDeletion` transition and `SetupWithManager`.
- `cmp_name_filter.go` — helper for filtering CMP list results by name.

### `internal/config/`

- `config.go` — `MainConfig` struct with all operator settings.
- `loader.go` — reads the `aruba-controller-manager` ConfigMap and Secret from the operator's namespace at startup; validates required fields.

### `internal/util/`

- `conditions.go` — `UpdateConditions` helper for mutating a `[]metav1.Condition` slice in-place (upsert by type).
- `cloudserver_util.go` — CloudServer-specific helpers.

---

## Guardrails — files you must not edit manually

| Path / pattern | Why | What to do instead |
|----------------|-----|--------------------|
| `api/v1alpha1/zz_generated.deepcopy.go` | Auto-generated by controller-gen | Run `make generate` |
| `internal/mocks/**` | Auto-generated by Mockery | Run `make generate` |
| `config/crd/bases/**` | Auto-generated CRD manifests | Run `make manifests` |

After changing **any type** in `api/v1alpha1/` (structs, fields, markers), always run:

```bash
make manifests generate
```

This regenerates CRDs, RBAC manifests, DeepCopy methods, and mocks in one step. Do not commit type changes without running this — the CI will fail.
