# Repository Structure

## Top-level layout

| Path | Purpose |
|------|---------|
| `api/v1alpha1/` | CRD type definitions |
| `cmd/main.go` | Operator entry point and manager setup |
| `internal/reconciler/` | Base reconciliation loop |
| `internal/controller/` | Resource-specific controllers and transition system |
| `internal/client/` | Aruba CMP API client wrappers |
| `internal/config/` | Config loading from ConfigMap + Secret |
| `internal/mocks/` | Mockery-generated mocks (do not edit manually) |
| `internal/util/` | Shared utilities |
| `config/crd/` | Generated CRD manifests (committed, updated by `make manifests`) |
| `config/samples/` | Example CR manifests used in manual and e2e testing |
| `test/scripts/` | Manual testing tooling (`test_runner.sh` + fixtures) |

## Package details

### `api/v1alpha1/`

Defines all Custom Resource types and the shared status model.

- `common_types.go` — `ResourceStatus`, `ResourcePhase`, condition reason constants (`ShallSynchronize`, `Synchronizing`, `Synchronized`, `Failed`), `ResourcePhaseNature` (Transitory / Final), and the base `Object` struct embedded by every CRD.
- `<resource>_types.go` — one file per CRD (Project, BlockStorage, CloudServer, ElasticIP, KeyPair, SecurityGroup, SecurityRule, Subnet, VPC); each contains the `Spec`, `Status`, and list type for that resource.
- `zz_generated.deepcopy.go` — generated; do not edit.

### `internal/reconciler/`

Contains only `reconciler.go`. Defines the shared three-step loop (`Reconcile`), the `ResourceReconciler` interface that every controller must implement, the `ResourceObject` constraint interface, `ReconcilerConfig`, and the timing constants (`ShortRequeueAfter`, `LongRequeueAfter`, `MaxPhaseTimeout`).

Key elements of the `Reconciler` struct:
- Private `multiTenantClient arubamt.Multitenant` — thread-safe cache of `aruba.Client` per tenant, initialized in `NewReconciler()`.
- Private `config ReconcilerConfig` — holds all credential and endpoint configuration for deferred client creation.
- Method `ArubaClient(tenant string) (aruba.Client, error)` — lazily creates and caches a tenant-scoped SDK client.
- Exported context key `ArubaClientKey` (type `contextKey`) — used by controllers to pass the resolved client through `context.WithValue`.
- `NewReconcilerForTest(k8sClient, scheme, mtClient)` — test-only constructor that bypasses `ctrl.Manager` and real credentials; accepts a pre-seeded `arubamt.Multitenant` so mock clients can be injected per tenant.

### `internal/controller/`

One file per concern:

- `<resource>_controller.go` — embeds `*reconciler.Reconciler`, wires a `TransitionSet`, and implements `Object()`, `Finalizer()`, `HandleReconcile()`.
- `transition.go` — the generic state-machine types: `Transition[K,A]` interface, `AbstractTransition[K,A]` (concrete impl with function-pointer fields), `TransitionSet[K,A]` (ordered list + default fallback), and requeue helpers (`ShortRequeue`, `LongRequeue`, `NoRequeue`, `LongRequeueAndIgnoreError`, `SmartRequeueOnError`).
- `transition_conditions.go` — all reusable `ConditionFunc[K,A]` implementations (e.g. `kubeIsFirstReconciliation`, `kubeShouldBeCreatedOnCMP`, `kubePhaseTimedOut`, `kubeShouldDelete`). These are package-private; only referenced from controller transition wiring.
- `transition_actions.go` — all reusable status-patch helpers: `setPhaseAndCondition`, `setActiveAndSetID`, `setFailedOnTimeout`, and `kubeSetErrorMessageOnCMPError`. These use `retry.RetryOnConflict` internally and are the canonical way to write back Kubernetes status.
- `cmp_error.go` — CMP error types: `CMPError` struct, `CMPErrorCategory` enum (Semantic/Technical), `cmpTransportError` and `cmpResponseError` constructors, `cmpCheckResponse[T]` generic response checker, and `CMPErrorIsSemantic`/`CMPErrorIsTechnical` helpers.
- `common.go` — CMP (Aruba-side) state constants (`CSPResourceState*`) and `AssesCSPResourceStateNature` for classifying CMP states as Transitory/Final.

### `internal/client/`

Thin wrappers around the `github.com/Arubacloud/sdk-go` Aruba client, one file per resource type (`arubaproject_client.go`, `arubablockstorage_client.go`, etc.).

- `helper.go` — `HelperClient` (holds HTTP client + K8s client + gateway URL + bearer token) and `DoAPIRequest`, which handles serialisation, auth headers, expected-status logic, and `ApiError` extraction. DELETE treats 404/405 as success (CMP quirk).
- `oauth_client.go` / `vault_client.go` — authentication implementations for single-tenant (OAuth2 client credentials) and multi-tenant (Vault AppRole) modes.

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
