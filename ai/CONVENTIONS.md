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
    "github.com/Arubacloud/sdk-go/pkg/aruba"

    "github.com/Arubacloud/arubacloud-resource-operator/api/v1alpha1"
    "github.com/Arubacloud/arubacloud-resource-operator/internal/reconciler"
)
```

**Never import `github.com/Arubacloud/sdk-go/pkg/types`.** Use only the high-level
`pkg/aruba` package: the fluent wrapper types (`*aruba.VPC`, `*aruba.Project`, …),
their getters (`.ID()`, `.State()`, `.Region()`, `.Tags()`, …), the `Ref`
constructors (`aruba.VPCRef`, `aruba.URI`, …), `aruba.State*` constants, and
`aruba.WithFilter`/`WithLimit` call options.

## Error handling

### CMP (Aruba API) errors

All errors from CMP interactions use the `CMPError` struct (`internal/reconciler/cmp_error.go`). Never use plain `fmt.Errorf` in CMP action methods.

**Three error categories:**
- `CMPErrorCategorySemantic` — HTTP 4xx responses with a non-empty `Errors` array in the `ErrorResponse` (field-level validation failures). These are permanent — the resource spec is invalid and must be corrected. During `Creating` or `Updating` phase, `KubeSetErrorMessageOnCMPError` immediately moves the resource to `Failed+ValidationFailed`. The formatted validation details are appended to the `Detail` field.
- `CMPErrorCategoryTransient` — HTTP 4xx responses with an empty `Errors` array (no field-level validation details). These represent temporary conditions (e.g. a dependency resource in a wrong state). The error message is surfaced in the condition but the phase/reason is not changed; the resource retries with `LongRequeueAfter`.
- `CMPErrorCategoryTechnical` — HTTP 5xx responses and network/transport errors; transient infrastructure failures that warrant a `ShortRequeueAfter` retry.

**Single categorization entry point** — `CMPErrorFromResult(operation, resource, err, okStatusCodes...)`. The high-level SDK CRUD calls return a plain `error` (an `*aruba.HTTPError` on a non-2xx reply, or a transport/builder error otherwise) instead of a typed `*Response`. `CMPErrorFromResult` turns that into a `*CMPError`:
```go
func (r *XxxReconciler) cmpCreate(ctx context.Context, kubeXxx *v1alpha1.Xxx, _ *aruba.Xxx) error {
    arubaClient := ctx.Value(reconciler.ArubaClientKey).(aruba.Client)
    x := aruba.NewXxx().InProject(aruba.URI("/projects/"+prjID)).Named(kubeXxx.Name).Tagged(kubeXxx.Spec.Tags...)
    _, err := arubaClient.FromXxx().Xxx().Create(ctx, x)
    return reconciler.CMPErrorFromResult("create", kubeXxx.Name, err)
}
```
- `err == nil` → `nil` (success).
- `*aruba.HTTPError` → categorized by status code (4xx with field errors → Semantic, 4xx without → Transient, else Technical).
- any other error → Technical (transport/builder).
- `okStatusCodes` are treated as success — `cmpDelete` passes `http.StatusNotFound` so a 404 (already gone) is not an error.

`cmpUpdate` mutates the fetched wrapper in place (it already carries its ID and immutable fields) and calls `Update(ctx, cmpXxx)`; `cmpDelete` passes the fetched wrapper directly as the `Ref`.

**Inspecting errors** — use `errors.As` or the convenience helpers:
```go
var cmpErr *CMPError
if errors.As(err, &cmpErr) { /* inspect cmpErr.Category, cmpErr.StatusCode, etc. */ }

CMPErrorIsSemantic(err)  // true for 4xx CMPErrors with validation errors
CMPErrorIsTransient(err) // true for 4xx CMPErrors without validation errors
CMPErrorIsTechnical(err) // true for 5xx/transport CMPErrors
```

**Standard transition wiring** for CMP-facing transitions (`ShouldBeCreatedInCMP`, `ShouldBeUpdatedOnCMP`, `ShouldBeDeletedOnCMP`):
```go
kActionOnAError: kubeSetErrorMessageOnCMPError[*v1alpha1.Xxx, *aruba.Xxx](r.Client),
requeueOnError:  SmartRequeueOnError[*v1alpha1.Xxx, *aruba.Xxx],
```

`SmartRequeueOnError[K, A]` — uses `ShortRequeueAfter` for technical errors, `LongRequeueAfter` for transient errors, and `ctrl.Result{}` (no requeue) for semantic errors — the resource waits for a spec change to trigger recovery. Exception: during the `Deleting` phase, semantic errors always get `LongRequeueAfter` (there is no "wait for spec change" recovery during deletion).

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
| `<type>Item(id, name, state)` | Test helper: build a CMP wire item (JSON map) for staging on the fake CMP server | `bsItem`, `subnetItem` |
| `default<Type>Spec(...)` | Test helper: sensible default K8s spec | `defaultBSSpec` |
| `createTest<Type>(...)` | Test helper: create and persist a K8s resource | `createTestProject` |
| `set<Type>Status(...)` | Test helper: put resource in a specific phase/reason | `setBSStatus` |

## Ownership

### Annotation and label conventions

The operator uses a **custom two-layer ownership model** instead of standard Kubernetes OwnerReferences (which do not support cross-namespace). Both layers are set atomically by `ensureOwnerReference` on every child reconciliation.

| Layer | Key | Value | Purpose |
|---|---|---|---|
| **Annotation** | `arubacloud.com/owner-references` | JSON `[]ArubaOwnerReference` | Source of truth. Full identity (namespace, name, UID, kind, apiVersion). |
| **Label** | `arubacloud.com/owner-<lowerkind>` | owner UID | Efficient cluster-wide server-side label queries. |

Both are **operator-managed** — never edit them manually. They self-heal on the next reconcile.

### No K8s OwnerReferences

Standard Kubernetes OwnerReferences (`metadata.ownerReferences`) are **not set** by this operator. This is intentional: K8s OwnerReferences have no `Namespace` field, so the GC resolves owners in the child's namespace only. Cross-namespace OwnerReferences are treated as dangling references by the GC, which may trigger premature deletion. The operator handles all cascade deletion explicitly via `WaitingChildrenDeletion`.

### Ownership infrastructure naming

Ownership helpers in `internal/controller/owner_reference.go` follow the "no prefix = shared infrastructure" convention:

| Function | Purpose |
|---|---|
| `ensureOwnerReference` | Idempotent entry point — sets annotation + label in one patch |
| `setArubaControllerReference` | Upserts the annotation entry for a given owner |
| `parseArubaOwnerReferences` | Reads and parses the annotation |
| `marshalArubaOwnerReferences` | Serialises and writes the annotation |
| `hasArubaOwnerReference` | Checks if the annotation contains a given UID |
| `ownerLabelKey` | Derives the label key from the owner's GVK |
| `hasOwnedChildren` | Cluster-wide label query — used in WaitingChildrenDeletion condition |
| `deleteOwnedChildren` | Cluster-wide label query + delete — used in WaitingChildrenDeletion action |
| `childToParentMapFunc` | Returns a `handler.MapFunc` for `Watches()` in parent controllers |

## Context usage

`context.Context` is always the first parameter in functions that perform I/O (API calls, K8s client operations, reconciliation methods).

## Kubernetes status updates

Always use `retry.RetryOnConflict(retry.DefaultRetry, ...)` with `.Status().Patch(ctx, obj, client.MergeFrom(objCopy))` for optimistic concurrency. The canonical helpers live in `transition_actions.go` (`SetPhaseAndCondition`, `SetActiveAndSetID`, `SetFailedOnTimeout`) — prefer calling these over writing raw status patches.

### kubeMarkToCreate pattern

Every `kubeMarkToCreate` method marks the resource `Creating+ShallSynchronize`. The `Pending` condition is already `Reason=Synchronized, Status=True` from the base reconciler, so `SetPhaseAndCondition` will automatically flip it to `Status=False` when it sets all existing conditions to False before writing the new Creating condition.

```go
func (r *XxxReconciler) kubeMarkToCreate(ctx context.Context, kubeXxx *v1alpha1.Xxx, _ *aruba.Xxx) error {
    return reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
        v1alpha1.ResourcePhaseCreating, v1alpha1.ConditionReasonShallSynchronize, nil,
        // resource-specific prePatch (ProjectID, VPCID, etc.) if any
    )
}
```

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
- **`fakeCMP`** — a real `aruba.Client` bound to an `httptest.Server` — stands in for the CMP API. The SDK client cannot be mocked to return populated wrappers (their ID/State are set only by `fromResponse`, which is unexported), so the fake serves canned JSON and lets the genuine SDK hydrate the wrappers.
- **`controller-runtime/envtest`** for integration tests with a fake K8s API server.

### Test file organisation

| File | Content |
|------|---------|
| `suite_test.go` | Global `envtest` setup (`BeforeSuite` / `AfterSuite`), shared `k8sClient` and `testEnv` |
| `common_test.go` | `fakeCMP` server + item builders + `newTestReconciler`, `strPtr`, `findCondition` |
| `transition_test.go` | Unit tests for the `TransitionSet` state machine |
| `transition_conditions_test.go` | Unit tests for reusable condition functions |
| `transition_actions_test.go` | Unit tests for reusable action helpers + `findCondition` utility |
| `<resource>_controller_test.go` | Integration tests for each controller's full lifecycle |

### Fake CMP setup pattern

`newTestReconciler` (defined in `common_test.go`) builds a **real** `aruba.Client` pointed at the fake server (via `WithBaseURL(f.server.URL).WithToken(...)` — a static token means no IDP round-trip) and seeds the multitenant cache for `"test-tenant"`:

```go
func newTestReconciler(_ GinkgoTInterface, f *fakeCMP) *reconciler.Reconciler {
    client, _ := aruba.NewClient(aruba.NewOptions().WithBaseURL(f.server.URL).WithToken("test-token"))
    mt := arubamt.New()
    mt.Add("test-tenant", client)
    return reconciler.NewReconcilerForTest(k8sClient, k8sClient.Scheme(), mt)
}
```

Each controller wraps this in `new<Xxx>ReconcilerWithFake()` and stages CMP items per resource collection (keyed by the last URL path segment). The operator only ever LISTs, so every `GET` is a collection listing:

```go
func newBSReconcilerWithFake() *bsFake {
    f := newFakeCMP()
    DeferCleanup(f.close)
    return &bsFake{r: NewBlockStorageReconciler(newTestReconciler(GinkgoT(), f)), f: f}
}

// stage the parent project + the volume the operator should find:
m.f.stage("projects", projectItem(bsProjectID, bsProjectName, nil, "", false))
m.f.stage("blockStorages", bsItem("bs-id-1", "test-bs", "Active"))
```

Create/Update/Delete responses are driven by `f.postStatus` / `f.putStatus` / `f.deleteStatus` (and `f.errKind = "validation"` to make a 4xx carry a field-level error → Semantic). All test specs must use `Tenant: "test-tenant"` so `r.ArubaClient("test-tenant")` hits the seeded cache entry.

Multi-reconcile tests must re-fetch the K8s object before each `HandleReconcile` (the production loop does), because the transition engine reads the in-memory object's status.

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

## Cross-resource consistency validation

### ValidationSet pattern

Every controller that references other resources declares two `ValidationSet` fields and builds them via dedicated methods. The bundle type is split into `kube*Bundle` (K8s-only, used by `ivs`) and `cmp*Bundle` (CMP-only), composed together as `*Bundle` for `vs`:

```go
type XxxReconciler struct {
    *reconciler.Reconciler
    ivs *reconciler.ValidationSet[*v1alpha1.Xxx, *aruba.Xxx, *kubeXxxBundle]
    vs  *reconciler.ValidationSet[*v1alpha1.Xxx, *aruba.Xxx, *xxxBundle]
    ts  *reconciler.TransitionSet[*v1alpha1.Xxx, *aruba.Xxx]
}

type kubeXxxBundle struct {
    KubeParent *v1alpha1.ParentType // from fetchKubeDependencies
}

type cmpXxxBundle struct {
    CMPParent *aruba.Parent // from fetchCMPDependencies
}

type xxxBundle struct {
    kubeXxxBundle
    cmpXxxBundle
}

func NewXxxReconciler(...) *XxxReconciler {
    r := &XxxReconciler{...}
    r.ivs = r.newIntentionValidationSet()
    r.vs = r.newValidationSet()
    r.ts = r.newTransitionSet()
    return r
}
```

### Cross-validation rule patterns

`ivs` and `vs` use different patterns by design:

**`ivs` rules always use inline lambdas** (Patterns 2 and 3 below). Bundle fields are nil-guarded because the K8s dependency object may not be present yet when the empty-bundle fallback is used.

**`vs` rules use `FieldMustMatch` when the bundle dependency is guaranteed non-nil** (Pattern 1). When the dependency may be nil at Stage 7 (e.g., `KubeProject` in Subnet/SecurityGroup/SecurityRule), use a nil-guarded inline lambda instead.

**1. `FieldMustMatch` — simple pair comparison (vs only, dependency guaranteed non-nil):**
```go
// vs (xxxBundle, dependency guaranteed present at Stage 7):
vs.Add("TenantMustMatchVPC", reconciler.FieldMustMatch[*v1alpha1.Subnet, *aruba.Subnet, *subnetBundle](
    "tenant",
    func(k *v1alpha1.Subnet) string { return k.Spec.Tenant },
    func(b *subnetBundle) string { return b.KubeVpc.Spec.Tenant },
    "VPC",
))
```

**2. Inline lambda with nil-guard — all ivs rules that access bundle fields; vs rules where dependency may be nil:**
```go
// ivs: nil-guard because KubeProject may be absent when empty-bundle fallback is used
ivs.Add("TenantMustMatchProject", func(k *v1alpha1.Subnet, _ *aruba.Subnet, b *kubeSubnetBundle) error {
    if b.KubeProject == nil {
        return nil
    }
    if k.Spec.Tenant != "" && b.KubeProject.Spec.Tenant != "" && k.Spec.Tenant != b.KubeProject.Spec.Tenant {
        return fmt.Errorf("tenant mismatch with Project: %q != %q", k.Spec.Tenant, b.KubeProject.Spec.Tenant)
    }
    return nil
})
```
All reference-presence rules (`ProjectReferenceRequired`, etc.) access only `k`, never the bundle — no nil-guard needed.

**3. Inline lambda with slice iteration — collection dependencies (both ivs and vs):**
```go
ivs.Add("ProjectMustMatchAllSubnets", func(k *v1alpha1.CloudServer, _ *aruba.CloudServer, b *kubeCloudServerBundle) error {
    var msgs []string
    for _, s := range b.KubeSubnets {
        if k.Spec.ProjectReference.Name != s.Spec.ProjectReference.Name {
            msgs = append(msgs, fmt.Sprintf("Subnet %q: %q != %q", s.Name, k.Spec.ProjectReference.Name, s.Spec.ProjectReference.Name))
        }
    }
    if len(msgs) > 0 {
        return fmt.Errorf("project reference mismatch: %s", strings.Join(msgs, "; "))
    }
    return nil
})
```
Use for `[]ResourceReference` fields (SubnetReferences, SecurityGroupReferences). The vs versions use the full `*xxxBundle` type parameter but are otherwise identical in logic.

### Calling the engine in HandleReconcile

`ivs` runs at Stage 4 (K8s-only, before CMP calls). `vs` runs at Stage 7 (after CMP calls). They are in separate stages with separate conditions:

```go
// Stage 4: Intention cross-validation (K8s-only, before CMP calls).
if !isDeleting {
    bdl := kubeBdl
    if bdl == nil {
        bdl = &kubeXxxBundle{} // empty — all fields nil; reference rules still fire
    }
    if validationErr := r.ivs.Run(kubeXxx, nil, bdl); validationErr != nil {
        setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
            v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonIntentionValidationFailed, validationErr,
        )
        if setErr != nil {
            return ctrl.Result{}, setErr
        }
        return ctrl.Result{}, nil // no requeue — wait for spec change
    }
    // Recovery: ivs now passes but resource is still IntentionValidationFailed → reset.
    if reconciler.IsIntentionValidationFailed(kubeXxx) {
        resetPhase := v1alpha1.ResourcePhasePending
        if kubeXxx.Status.ResourceID != "" {
            resetPhase = v1alpha1.ResourcePhaseActive
        }
        if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
            resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
            return ctrl.Result{}, setErr
        }
        return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
    }
    // Recovery: CMP semantic error — user has updated the spec since failure.
    if reconciler.IsCMPValidationFailedAndSpecChanged(kubeXxx) {
        resetPhase := v1alpha1.ResourcePhasePending
        if kubeXxx.Status.ResourceID != "" {
            resetPhase = v1alpha1.ResourcePhaseActive
        }
        if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
            resetPhase, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
            return ctrl.Result{}, setErr
        }
        return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
    }
}

// ... Stage 5: Create Aruba client. Stage 6: fetchCMPDependencies. ...

// Stage 7: CMP-aware validation (vs only).
if !isDeleting && kubeBdl != nil && cmpXxx != nil {
    if validationErr := r.vs.Run(kubeXxx, cmpXxx, &xxxBundle{
        kubeXxxBundle: *kubeBdl,
        cmpXxxBundle:  cmpXxxBundle{CMPParent: cmpParent},
    }); validationErr != nil {
        setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
            v1alpha1.ResourcePhaseFailed, v1alpha1.ConditionReasonPostValidationFailed, validationErr,
            func(x *v1alpha1.Xxx) {
                if x.Status.ProjectID == "" {
                    x.Status.ProjectID = prjID
                }
            },
        )
        if setErr != nil {
            return ctrl.Result{}, setErr
        }
        return ctrl.Result{}, nil // no requeue — wait for spec change
    }
    // Recovery: vs now passes but resource is still PostValidationFailed → reset to Active.
    if reconciler.IsPostValidationFailed(kubeXxx) {
        if setErr := reconciler.SetPhaseAndCondition(r.Client, ctx, kubeXxx,
            v1alpha1.ResourcePhaseActive, v1alpha1.ConditionReasonSynchronized, nil); setErr != nil {
            return ctrl.Result{}, setErr
        }
        return ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
    }
}

// Stage 8: Run transitions.
return r.ts.Run(ctx, kubeXxx, cmpXxx)
```

**Recovery in Stage 7**: if `vs` now passes but the resource is still `PostValidationFailed`, the recovery block resets to `Active+Synchronized` (since `vs` only runs when `cmpXxx != nil`, meaning the resource already has a `ResourceID`). The `IntentionValidationFailed` recovery in Stage 4 fires earlier, so by the time Stage 7 runs on the next reconcile, any ivs-driven failure is already resolved.

### fetchKubeDependencies pattern

`fetchKubeDependencies` resolves K8s parent objects and sets the owner reference. It always returns `(*kubeXxxBundle, ctrl.Result, error)`:

```go
func (r *XxxReconciler) fetchKubeDependencies(
    ctx context.Context,
    kubeXxx *v1alpha1.Xxx,
    isDeleting bool,
) (*kubeXxxBundle, ctrl.Result, error) {
    if isDeleting {
        return nil, ctrl.Result{}, nil
    }
    kp := &v1alpha1.ParentType{}
    if err := resolveOwnerObject(ctx, r.Client, kubeXxx.Spec.ParentReference, kubeXxx.Namespace, kp); err != nil {
        if apierrors.IsNotFound(err) {
            return nil, ctrl.Result{}, nil // non-fatal: parent not found yet
        }
        return nil, ctrl.Result{}, fmt.Errorf("resolving parent for owner reference: %w", err)
    }
    requeue, err := ensureOwnerReference(ctx, r.Client, r.Scheme, kp, kubeXxx)
    if err != nil {
        return nil, ctrl.Result{}, fmt.Errorf("setting owner reference: %w", err)
    }
    if requeue {
        return nil, ctrl.Result{RequeueAfter: reconciler.ShortRequeueAfter}, nil
    }
    return &kubeXxxBundle{KubeParent: kp}, ctrl.Result{}, nil
}
```

### Validation test pattern

**ivs tests** (Stage 4 — fires before CMP calls) follow the **two-reconcile pattern**:
1. **First reconcile** — `ensureOwnerReference` returns `(requeue=true, nil)` before any CMP calls → returns `ShortRequeueAfter`. No CMP items need staging.
2. **Second reconcile** — owner reference is already set; ivs fires at Stage 4, before CMP. **No CMP items need staging** — the reconcile returns before reaching Stage 6.

```go
It("sets Failed+ValidationFailed when tenant does not match", func() {
    kubeParent := createTestParent(ctx, parentName, v1alpha1.ParentSpec{Tenant: "other-tenant", ...})
    defer func() { _ = k8sClient.Delete(ctx, kubeParent) }()

    m := newXxxReconcilerWithFake()
    xxx = createTestXxx(ctx, "test-validation", defaultXxxSpec(parentName, ...))

    // First: owner ref setup → requeue
    result, err := m.r.HandleReconcile(ctx, xxx)
    Expect(err).To(Succeed())
    Expect(result.RequeueAfter).To(Equal(reconciler.ShortRequeueAfter))
    Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(xxx), xxx)).To(Succeed())

    // Second: ivs fires at Stage 4 — nothing to stage on the fake CMP
    _, err = m.r.HandleReconcile(ctx, xxx)
    Expect(err).To(Succeed())

    updated := &v1alpha1.Xxx{}
    Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(xxx), updated)).To(Succeed())
    Expect(updated.Status.Phase).To(Equal(v1alpha1.ResourcePhaseFailed))
    cond := findCondition(updated.Status.Conditions, string(v1alpha1.ResourcePhaseFailed))
    Expect(cond.Reason).To(Equal(v1alpha1.ConditionReasonValidationFailed))
    Expect(cond.Message).To(ContainSubstring("tenant mismatch with Parent"))
})
```

**vs tests** (Stage 7 — fires after CMP calls) still require the CMP parent + resource to be staged on the fake, since Stage 6 (`fetchCMPDependencies`) runs before Stage 7.

### Error message format

Validation functions produce human-readable messages:
- Tenant: `"tenant mismatch with <Kind>: \"actual\" != \"expected\""`
- Region: `"region mismatch with <Kind>: \"actual\" != \"expected\""`
- Zone: `"zone mismatch with <Kind>: \"actual\" != \"expected\""`
- VPC reference: `"VPC reference mismatch with <Kind>: \"actual\" != \"expected\""`
- Project reference: `"project reference mismatch with <Kind>: \"actual\" != \"expected\""`
- Plural (CloudServer): `"region mismatch: Subnet[0]: \"a\" != \"b\"; Subnet[1]: ..."`
- Plural project (CloudServer): `"project reference mismatch: Subnet \"name\": \"a\" != \"b\"; ..."`

These messages are stored in the condition's `Message` field prefixed by `"ERROR: "` (added by `SetPhaseAndCondition`).

---

## Controller file layout

Every controller file follows a canonical 14-section ordering. Sections that don't apply to a given controller are simply omitted.

### Section order

| # | Section | Contents |
|---|---------|----------|
| 1 | **Constants** | `const` block — finalizer name, context keys |
| 2 | **Types** | `type` blocks — `kube*Bundle`, `cmp*Bundle`, `*Bundle` structs; `+kubebuilder` RBAC directives; `*Reconciler` struct |
| 3 | **Constructor** | `New<Resource>Reconciler` |
| 4 | **Interface methods** | `Reconcile`, `Object`, `Finalizer` (no RBAC directives) |
| 5 | **HandleReconcile** | `HandleReconcile` |
| 6 | **Major HandleReconcile helpers** | `fetchKubeDependencies`, `fetchCMPDependencies`, `newIntentionValidationSet` (if present), `newValidationSet`, `newTransitionSet` — in this exact order, under one banner |
| 7 | **Owned-children helpers** | `kubeXxxHasOwnedChildren`, `kubeXxxDeleteOwnedChildren` (VPC, SecurityGroup only) |
| 8 | **CMP resolve helpers** | CloudServer only: `resolveProjectID`, `resolveVpcID`, etc. |
| 9 | **Kube conditions** | `kube*` condition functions (update-phase guards); omit section if none |
| 10 | **CMP conditions** | `cmp*` condition functions (delete-phase guards) |
| 11 | **Kube actions** | `kubeMarkTo*`, `kubeSetFailedOnTimeout`, `kubeMarkUpdatingFailed`, `kubeRollbackSpecAndSetActive` |
| 12 | **CMP actions** | Always ordered: `cmpDelete` → `cmpUpdate` → `cmpCreate` |
| 13 | **Other helpers** | `checkDenied`, `needsUpdate`, `buildUpdateRequest`, `fromCMP`, `fromKube`, URI builders, `applyNameFilter*` |
| 14 | **Setup** | `SetupWithManager` |

### Section separator comments

Each section is preceded by a visible banner to make navigation easy:

```go
// ---------------------------------------------------------------------------
// Section Name
// ---------------------------------------------------------------------------
```

Use the section name exactly as listed in the table above (e.g. "Constructor", "Interface methods", "HandleReconcile", "Major HandleReconcile helpers", "CMP actions").

### Bundle struct composition

Every controller that references other resources uses a **two-part bundle composition**:

```go
// K8s-only fields — used by ivs (intention validation, runs before CMP resource exists)
type kubeXxxBundle struct {
    KubeParent *v1alpha1.ParentType
    // ... other K8s dependency objects
}

// CMP-only fields — used by vs (drift validation, runs after CMP resource exists)
type cmpXxxBundle struct {
    CMPParent *aruba.Parent
    // ... other CMP dependency responses
}

// Full bundle — used by vs; embeds both sub-bundles
type xxxBundle struct {
    kubeXxxBundle
    cmpXxxBundle
}
```

- `ivs` type parameter is `*kubeXxxBundle` — never receives CMP data
- `vs` type parameter is `*xxxBundle` — receives both K8s and CMP data via embedding
- Fields within each bundle struct appear in the order they are fetched (K8s fetch order in `fetchKubeDependencies`, CMP fetch order in `fetchCMPDependencies`)

**Simple controllers** (VPC, BlockStorage, ElasticIP, KeyPair) have no `cmpXxxBundle` — the `xxxBundle` embeds only `kubeXxxBundle` since these controllers have no CMP-only bundle fields. The split is still present: `kubeXxxBundle` is the ivs type parameter and `xxxBundle` is the vs type parameter.

### Reconciler struct field ordering

Fields in `*Reconciler` always appear in usage order: `ivs` (Stage 4), then `vs` (Stage 7), then `ts` (Stage 8). All controllers have all three fields.

Constructor initializes in the same order: `r.ivs = r.newIntentionValidationSet()`, then `r.vs = r.newValidationSet()`, then `r.ts = r.newTransitionSet()`.

### +kubebuilder RBAC directive placement

`+kubebuilder:rbac:...` directives are placed in the **Types** section, directly above the `*Reconciler` struct definition (not in Interface methods). This keeps all type declarations together and allows kubebuilder's code generator to scan them in one location.

### Mandatory HandleReconcile helpers

Every `*Reconciler` must implement both:

- **`fetchKubeDependencies(ctx, kubeObj, isDeleting)`** — fetches K8s parent objects, sets owner reference, returns `(*kubeXxxBundle, ctrl.Result, error)`. Skips all work when `isDeleting` is true.

- **`fetchCMPDependencies(ctx, kubeObj, arubaClient, isDeleting)`** — resolves CMP parent IDs, fetches all CMP dependency responses, fetches the primary CMP resource, enriches context with ID values and `ArubaClientKey`. Returns the primary CMP resource, the CMP bundle, and `ctrl.Result`/`error`.

### CMP action ordering rule

Within **CMP actions** (section 12), always use the order `cmpDelete` → `cmpUpdate` → `cmpCreate`. This mirrors the lifecycle priority: deletion before update before creation.

## Documentation

When code changes affect CRD types, user-facing behaviour, or installation/configuration, update the corresponding documentation. See `ai/DOCS.md` for the full mapping of code paths to documentation files, page structure conventions, and i18n requirements.
