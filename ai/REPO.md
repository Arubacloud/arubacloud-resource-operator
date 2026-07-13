# Repository Structure

## Top-level layout

| Path | Purpose |
|------|---------|
| `api/v1alpha1/` | CRD type definitions |
| `cmd/main.go` | Operator entry point and manager setup |
| `internal/reconciler/` | Base reconciliation loop |
| `internal/controller/` | Resource-specific controllers and transition system |
| `internal/client/` | Thin port over the Aruba Go SDK (interfaces + adapter) |
| `internal/config/` | Config loading from ConfigMap + Secret |
| `internal/mocks/` | Mockery-generated mocks (do not edit manually) |
| `internal/util/` | Shared utilities |
| `config/crd/` | Generated CRD manifests (committed, updated by `make manifests`) |
| `config/samples/` | Example CR manifests used in manual and e2e testing |
| `test/scripts/` | Manual testing tooling (`test_runner.sh` + fixtures) |
| `devex/build/` | Containerized development environment (`Dockerfile` for the devtools image) |
| `docs/website/` | Docusaurus documentation site (EN + IT); see `ai/DOCS.md` for structure and conventions |

## Package details

### `api/v1alpha1/`

Defines all Custom Resource types and the shared status model.

- `common_types.go` — `ResourceStatus`, `ResourcePhase` (including `ResourcePhasePending` as the initial phase set alongside the finalizer), condition reason constants (`ShallSynchronize`, `Synchronizing`, `Synchronized`, `Failed`, `ValidationFailed`), `ResourcePhaseNature` (Transitory / Final — `Pending` is classified as Final so timeouts never apply), `ResourceReference`, and `ArubaOwnerReference`.
- `<resource>_types.go` — one file per CRD (Project, BlockStorage, CloudServer, ElasticIP, KeyPair, SecurityGroup, SecurityRule, Subnet, VPC); each contains the `Spec`, `Status`, and list type for that resource. Flat scalar fields are used for `Region string`, `Zone string`, `SizeGB int`, `BillingPeriod string`, and `CIDR string` — no nested structs for these. `Spec.Tenant` carries no CRD-level XValidation immutability marker — it is intentionally mutable so users can correct a wrong tenant on a failed resource (see `ARCHITECTURE.md` § "Tenant source").
- `zz_generated.deepcopy.go` — generated; do not edit.

### `internal/reconciler/`

The complete reconciliation framework. Contains:

- `reconciler.go` — the shared three-step loop (`Reconcile`), the `ResourceReconciler` interface that every controller must implement, the `ResourceObject` constraint interface, `ReconcilerConfig`, and the timing constants (`ShortRequeueAfter`, `LongRequeueAfter`, `MaxPhaseTimeout`).
- `metrics.go` — the `aruba_reconcile_step_duration_seconds` Prometheus histogram, registered with the controller-runtime metrics registry; provides `getResourceKind`, `getPhaseAndReason`, and `observeStep` helpers used to instrument each reconciliation step.
- `transition.go` — the generic state-machine types: `AbstractTransition[K,A]` (concrete struct with exported function-pointer fields), `TransitionSet[K,A]` (ordered list + default fallback), type aliases `ConditionFunc`, `ActionFunc`, `ActionOnErrorFunc`, `RequeueFunc`, `RequeueOnErrorFunc`, and all requeue helpers (`ShortRequeue`, `LongRequeue`, `NoRequeue`, `SmartRequeueOnError`, `LongRequeueAndIgnoreError`, etc.).
- `transition_conditions.go` — all reusable `ConditionFunc[K,A]` implementations (e.g. `KubeIsFirstReconciliation`, `KubeShouldBeCreatedOnCMP`, `KubePhaseTimedOut`, `KubeShouldDelete`). Unexported helpers `kubeHasPhaseAndReason` and `failedPhase` are used internally.
- `transition_actions.go` — all reusable status-patch helpers: `SetPhaseAndCondition`, `SetActiveAndSetID`, `SetFailedOnTimeout`, `KubeSetErrorMessageOnCMPError`, and `TagsAreEqual`. These use `retry.RetryOnConflict` internally and are the canonical way to write back Kubernetes status.
- `cmp_error.go` — CMP error types: `CMPError` struct, `CMPErrorCategory` enum (Semantic/Technical), `CMPTransportError` and `cmpResponseError` constructors, `CMPCheckResponse[T]` generic response checker, and `CMPErrorIsSemantic`/`CMPErrorIsTechnical` helpers.
- `common.go` — the `CSPResourceStateNature` enum (Transitory / Final / Invalid / Undetermined) and `AssessCSPResourceStateNature(*arubatypes.ResourceStatusResponse)`, which classifies a CMP state's nature (delegates the transitory check to `arubatypes.State.IsTransitory()`). Raw CMP states are compared directly against the SDK's `arubatypes.State*` constants — the operator keeps no re-exported copies.
- `validation.go` — cross-resource consistency validation engine: `ValidationFunc[K,A,B]`, `ValidationSet[K,A,B]` (ordered rule list with `Add`/`Run`), and reusable rule builder `FieldMustMatch`.
- `validation_error.go` — `ErrInvalid` (aggregate error returned by `ValidationSet.Run` when any rule fails), `ValidationViolation` (rule name + error message pair), `IsErrInvalid` predicate.

Key elements of the `Reconciler` struct:
- Private `clients map[string]client.Client` (guarded by `clientsMu sync.Mutex`) — per-tenant cache of port clients, built lazily by `ArubaClient` and initialized empty in `NewReconciler()`.
- Private `config ReconcilerConfig` — holds all credential and endpoint configuration for deferred client creation.
- Method `ArubaClient(tenant string) (client.Client, error)` — lazily builds a raw `aruba.Client` from config, wraps it in the port via `client.New(...)`, caches the result per tenant, and returns it.
- Exported context key `ArubaClientKey` (type `contextKey`) — used by controllers to pass the resolved `client.Client` through `context.WithValue`.
- `NewReconcilerForTest(k8sClient, scheme, clients)` — test-only constructor that bypasses `ctrl.Manager` and real credentials; accepts a pre-seeded `map[string]client.Client` so mock port clients can be injected per tenant.

### `internal/client/`

The operator's thin port over the Aruba Go SDK (`github.com/Arubacloud/sdk-go`). Exists because the SDK's public API is object-oriented (resource wrappers hydrated only by real client calls, with no public constructor from a wire response), whereas the reconciliation engine is generic over the SDK's wire response types (`*arubatypes.XxxResponse`) so responses can be constructed directly in unit tests. This package is the single seam that translates between the two — do not call the SDK directly from controllers.

- `client.go` — the port interfaces: `Client` (root, with `FromProject`/`FromNetwork`/`FromCompute`/`FromStorage`) and one interface per resource the operator manages (`VPCsClient`, `SubnetsClient`, `SecurityGroupsClient`, `SecurityGroupRulesClient`, `ElasticIPsClient`, `CloudServersClient`, `KeyPairsClient`, `VolumesClient`, `ProjectClient`). Method signatures are request-in / wire-response-out (`List/Create/Update/Delete` taking `projectID`/parent IDs + a `*arubatypes.XxxRequest` and returning `*arubatypes.Response[T]`), so controllers and tests are decoupled from the SDK's wrapper API.
- `adapter.go` — the implementation over a raw `aruba.Client`. `client.New(sdkClient)` wraps an SDK client; each method translates: **reads** (`List`) return the wire payload via `list.Raw()`; **writes** (`Create`/`Update`) build an SDK wrapper from the wire request via fluent setters (`applyVPC`, `applyCloudServer`, …) — `Update` first does a `Get` to hydrate the wrapper's ID since wrappers expose no public ID setter; **deletes** call the wrapper client with a `Ref`. Shared helpers (`objResp`, `listResp`, `deleteResp`, `classify`) reconstruct `*arubatypes.Response[T]` envelopes and map the SDK's `*aruba.HTTPError` back into the response (as the pre-v1 SDK did), so the engine's existing status-code/error handling is unchanged.

Mocks for these interfaces are generated by mockery into `internal/mocks/aruba` (package `arubamocks`); see `.mockery.yaml`.

### `internal/controller/`

One file per concern:

- `<resource>_controller.go` — embeds `*reconciler.Reconciler`, wires a `reconciler.TransitionSet` and two `reconciler.ValidationSet` instances (`ivs` for K8s-only intention validation at Stage 4 and `vs` for CMP-aware drift validation at Stage 7), and implements `Object()`, `Finalizer()`, `HandleReconcile()`.
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
