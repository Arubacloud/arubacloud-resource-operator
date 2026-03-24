# Code Conventions

Go version: **1.24** · Module: `github.com/Arubacloud/arubacloud-resource-operator`

---

## Import ordering

Three groups separated by blank lines: stdlib → external → internal.

```go
import (
    "context"
    "fmt"

    ctrl "sigs.k8s.io/controller-runtime"
    arubatypes "github.com/Arubacloud/sdk-go/pkg/types"

    "github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
    "github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)
```

## Error handling

### CMP (Aruba API) errors

All errors from CMP interactions use the `CMPError` struct (`internal/controller/cmp_error.go`). Never use plain `fmt.Errorf` in CMP action methods.

**Two error categories:**
- `CMPErrorCategorySemantic` — HTTP 4xx responses; may be user/config mistakes or transient dependency blockages (e.g. deleting a project with remaining resources). The error message is surfaced in the condition but the phase/reason is never changed to `Failed` (only timeouts cause `Failed`).
- `CMPErrorCategoryTechnical` — HTTP 5xx responses and network/transport errors; transient infrastructure failures that warrant a short retry.

**Constructors:**
```go
// For Go-level transport failures (network, timeout, context cancel)
cmpTransportError("create", resourceName, err) // always Technical

// For non-success HTTP responses
cmpResponseError("delete", resourceName, statusCode, resp.Error) // 4xx→Semantic, 5xx→Technical
```

**Generic response checker** — canonical replacement for the status-code switch in CMP action methods:
```go
func (r *XxxReconciler) cmpCreate(ctx context.Context, kubeXxx *v1alpha1.Xxx, _ *arubatypes.XxxResponse) error {
    resp, err := arubaClient.FromXxx().Create(ctx, ...)
    if err != nil {
        return cmpTransportError("create", kubeXxx.Name, err)
    }
    return cmpCheckResponse("create", kubeXxx.Name, resp, http.StatusOK, http.StatusCreated)
}
```

**Inspecting errors** — use `errors.As` or the convenience helpers:
```go
var cmpErr *CMPError
if errors.As(err, &cmpErr) { /* inspect cmpErr.Category, cmpErr.StatusCode, etc. */ }

CMPErrorIsSemantic(err)  // true for 4xx CMPErrors
CMPErrorIsTechnical(err) // true for 5xx/transport CMPErrors
```

**Standard transition wiring** for CMP-facing transitions (`ShouldBeCreatedInCMP`, `ShouldBeUpdatedOnCMP`, `ShouldBeDeletedOnCMP`):
```go
kActionOnAError: kubeSetErrorMessageOnCMPError[*v1alpha1.Xxx, *arubatypes.XxxResponse](r.Client),
requeueOnError:  SmartRequeueOnError[*v1alpha1.Xxx, *arubatypes.XxxResponse],
```

### General error handling

- Wrap errors with `fmt.Errorf("...: %w", err)` to preserve the error chain.
- Define sentinel errors with `errors.New()` in the package where they originate (e.g. `ErrNotAllowedChanges` in `common.go`).
- Include relevant identifiers in the message (resource name, ID) for debuggability.

## Naming conventions

### Controller functions

| Prefix | Domain | Example |
|--------|--------|---------|
| `kube` | Kubernetes-side condition or action | `kubePhaseTimedOut`, `kubeMarkToCreate` |
| `cmp` | CMP (Aruba cloud) condition or action | `cmpProjectExists`, `cmpDelete` |

### Transition components

- **KConditions** (Kubernetes-side): `kube<Description>` — e.g. `kubeIsFirstReconciliation`, `kubeShouldBeUpdatedOnCMP`
- **AConditions** (CMP-side): `cmp<Resource><State>` — e.g. `cmpBlockStorageIsFinal`, `cmpProjectNotExists`
- **KActions**: `kubeMarkTo<Phase>`, `kubeMarkDeleted`, `kubeSetActiveAndSetID`, `kubeSetFailedOnTimeout`
- **AActions**: `cmpCreate`, `cmpUpdate`, `cmpDelete`

### Other naming patterns

| Pattern | Usage | Example |
|---------|-------|---------|
| `New<Type>Reconciler` | Constructor for a controller | `NewProjectReconciler` |
| `<resource>FinalizerName` | Finalizer constant | `projectFinalizerName` |
| `build<Type>Response(...)` | Test helper: build a CMP response | `buildProjectResponse` |
| `build<Type>List(...)` | Test helper: wrap responses in a list | `buildBlockStorageList` |
| `default<Type>Spec(...)` | Test helper: sensible default K8s spec | `defaultBSSpec` |
| `createTest<Type>(...)` | Test helper: create and persist a K8s resource | `createTestProject` |
| `set<Type>Status(...)` | Test helper: put resource in a specific phase/reason | `setBSStatus` |

## Context usage

`context.Context` is always the first parameter in functions that perform I/O (API calls, K8s client operations, reconciliation methods).

## Kubernetes status updates

Always use `retry.RetryOnConflict(retry.DefaultRetry, ...)` with `.Status().Patch(ctx, obj, client.MergeFrom(objCopy))` for optimistic concurrency. The canonical helpers live in `transition_actions.go` (`setPhaseAndCondition`, `setActiveAndSetID`, `setFailedOnTimeout`) — prefer calling these over writing raw status patches.

## Logging

### Framework

The operator uses **logr** (via `sigs.k8s.io/controller-runtime/pkg/log`) backed by Go's `log/slog` with a JSON handler.
The logger is initialized in `cmd/main.go` and set globally via `ctrl.SetLogger()`.

### Getting a logger

In reconcilers and any function that receives `ctx context.Context`:

```go
logger := log.FromContext(ctx)
```

In `HandleReconcile`, enrich the logger with tenant info and store it back in context so downstream code (transition engine, action helpers) inherits the fields:

```go
logger := log.FromContext(ctx).WithValues("tenant", kubeObj.Spec.Tenant)
ctx = log.IntoContext(ctx, logger)
```

In code without context (startup, config validation):

```go
ctrl.Log.WithName("component").Info("message", "key", value)
```

### Log levels

logr V-levels map to JSON `"level"` strings in the output as follows:

| logr call | JSON `"level"` | Enabled by `--log-level` | When to use |
|-----------|---------------|--------------------------|-------------|
| `logger.Error(err, ...)` | `"ERROR"` | always | Failures requiring attention: CMP errors, K8s write failures |
| `logger.Info(...)` | `"INFO"` | `info` (default) | Significant state changes: phase transitions, resource active, reconcile start |
| `logger.V(1).Info(...)` | `"DEBUG"` | `debug` | Diagnostic: CMP resource state, transition completed, requeue reasons |
| `logger.V(2).Info(...)` | `"TRACE"` | `trace` | Deep debugging: transition matched/no-match, condition evaluation (timeout check, generation check) |

logr has no native Warn level. For warning-grade messages, use `Info` with a clear message (standard K8s operator convention).

### Structured fields

Always use key-value pairs. Standard operator-domain fields:

| Field | When |
|-------|------|
| `"resource"` | namespace/name of the K8s object |
| `"resourceKind"` | GVK kind (e.g. `"Project"`) |
| `"resourceID"` | CMP resource ID |
| `"phase"` | Current ResourcePhase |
| `"transition"` | Transition name (in transition engine) |
| `"tenant"` | Spec.Tenant |
| `"cmpOperation"` | `"create"`, `"update"`, `"delete"` |

Infrastructure fields (cluster, namespace, pod, etc.) are injected by the log pipeline — do NOT add them in code.

### Security

Never log secrets, tokens, or credentials — even at debug/trace levels.

## Metrics

Custom Prometheus metrics are defined in `internal/reconciler/metrics.go` and registered with the controller-runtime metrics registry:

```go
func init() {
    metrics.Registry.MustRegister(myHistogram)
}
```

- Use `sigs.k8s.io/controller-runtime/pkg/metrics` for the registry (not the global `prometheus.DefaultRegisterer`).
- Metric names follow the `aruba_<subsystem>_<name>_<unit>` pattern — e.g. `aruba_reconcile_step_duration_seconds`.
- Label names use `snake_case`.

## Comments

- Exported types and functions get standard Go doc comments (full sentence starting with the symbol name).
- Internal/unexported functions: comment only when the logic is non-obvious. Do not add comments to code you did not write or modify.

## Testing

### Framework

- **Ginkgo v2 + Gomega** for all tests (BDD style: `Describe` / `It` / `BeforeEach` / `AfterEach`).
- **Testify mocks** (generated by Mockery) for CMP API client interfaces.
- **`controller-runtime/envtest`** for integration tests with a fake K8s API server.

### Test file organisation

| File | Content |
|------|---------|
| `suite_test.go` | Global `envtest` setup (`BeforeSuite` / `AfterSuite`), shared `k8sClient` and `testEnv` |
| `common_test.go` | Tests for shared utilities (`AssesCSPResourceStateNature`) |
| `transition_test.go` | Unit tests for the `TransitionSet` state machine |
| `transition_conditions_test.go` | Unit tests for reusable condition functions |
| `transition_actions_test.go` | Unit tests for reusable action helpers + `findCondition` utility |
| `<resource>_controller_test.go` | Integration tests for each controller's full lifecycle |

### Mock setup pattern

Group mocks in a struct with an `expect*` method per API call scenario.

The reconciler is constructed using `newTestReconciler` (defined in `common_test.go`), which creates a real `arubamt.Multitenant` cache pre-seeded with the mock client for `"test-tenant"`:

```go
// common_test.go
func newTestReconciler(t GinkgoTInterface, mockArubaClient aruba.Client) *reconciler.Reconciler {
    mt := arubamt.New()
    mt.Add("test-tenant", mockArubaClient)
    return reconciler.NewReconcilerForTest(k8sClient, k8sClient.Scheme(), mt)
}
```

```go
type bsMocks struct {
    r           *BlockStorageReconciler
    mockAruba   *arubamocks.MockClient
    mockProject *arubamocks.MockProjectClient
    // ...
}

func newBSReconcilerWithMocks(t GinkgoTInterface) *bsMocks {
    mockAruba := arubamocks.NewMockClient(t)
    // ...
    r := NewBlockStorageReconciler(newTestReconciler(t, mockAruba))
    return &bsMocks{r: r, mockAruba: mockAruba, ...}
}

func (m *bsMocks) expectProjectList(projectID, projectName string) {
    m.mockAruba.EXPECT().FromProject().Return(m.mockProject)
    m.mockProject.EXPECT().List(mock.Anything, mock.Anything).Return(...)
}
```

All test specs must use `Tenant: "test-tenant"` so that `r.ArubaClient("test-tenant")` hits the pre-seeded cache entry.

### Assertion style

Use Gomega matchers exclusively in test logic (not testify assertions):

```go
Expect(err).To(Succeed())
Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseActive))
Expect(cond).NotTo(BeNil())
Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonSynchronized))
Expect(result.RequeueAfter).To(Equal(reconciler.LongRequeueAfter))
```

Use `DescribeTable` with `Entry` for parametrised tests.

### Functional-option builders for unit tests

Use the `with<Option>` pattern to construct test objects with specific state:

```go
type projectOpt func(*v1alpha1.Project)
func withPhase(p v1alpha1.ResourcePhase) projectOpt { ... }
func newTestProject(opts ...projectOpt) *v1alpha1.Project { ... }
```

## CMP client patterns

One client file per resource type in `internal/client/`. Methods follow:

```go
func (c *HelperClient) Create<Resource>(ctx context.Context, req <Resource>Request) (*<Resource>Response, error) {
    var resp <Resource>Response
    if err := c.DoAPIRequest(ctx, "POST", "/<endpoint>", req, &resp); err != nil {
        return nil, err
    }
    return &resp, nil
}
```

All HTTP details (auth headers, JSON marshalling, error extraction) are handled by `DoAPIRequest` — never bypass it.
