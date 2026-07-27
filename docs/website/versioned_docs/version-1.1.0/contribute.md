---
sidebar_position: 99
---

# Contribute

Contributions to the Aruba Cloud Resource Operator are welcome. This document explains how to set up a development environment, add a new controller, and submit a pull request.

## Before you start

- Open an issue to discuss the change you want to make (unless it is already on the roadmap).
- Check that no one else is already working on the same feature or fix.

## Development setup

Prerequisites:

- Go 1.23+
- Docker (for the containerised devtools targets)
- A running Kubernetes cluster (optional — `envtest` provides a fake API server for unit tests)

List all available make targets:

```bash
make help
```

Run lint and tests using the containerised environment (no local Go required):

```bash
make lint-ctzd
make test-ctzd
```

## Adding a new controller

### 1. Define the CRD type

Add `api/v1alpha1/<resource>_types.go` following an existing resource as a template (e.g. `vpc_types.go`). Then regenerate manifests and deep-copy code:

```bash
make manifests generate
```

### 2. Implement the controller

Create `internal/controller/<resource>_controller.go`. Follow the 8-stage `HandleReconcile` pipeline documented in `ai/ARCHITECTURE.md` and the canonical 14-section file layout in `ai/CONVENTIONS.md`.

### 3. Register the controller

Add the controller to `cmd/main.go` following the existing pattern.

### 4. Add documentation

Create `docs/website/docs/<Resource>.md` and `docs/website/i18n/it/docusaurus-plugin-content-docs/current/<Resource>.md` using the CRD page template in `ai/DOCS.md`. Add the new page to `docs/website/sidebars.js`.

### 5. Add sample manifests

Add one or more example CRs to `config/samples/` for use in integration tests and the Examples page.

## Code style

- Format with `gofmt` / `goimports` before committing.
- Follow the naming, error handling, logging, and testing conventions in `ai/CONVENTIONS.md`.
- Do not hardcode tenant IDs, region names, or zone names in production code — always read them from the CRD spec.
- All status writes must go through `retry.RetryOnConflict` using the helpers in `internal/reconciler/transition_actions.go`.

## Pull request checklist

- [ ] `make lint-ctzd` passes with no new errors
- [ ] `make test-ctzd` passes with no new failures
- [ ] `make manifests generate` was re-run if any type in `api/v1alpha1/` changed
- [ ] Docs in `docs/website/docs/` updated if CRD fields or operator behaviour changed
- [ ] Italian translation in `docs/website/i18n/it/` mirrors English changes
- [ ] New CRD page added to `docs/website/sidebars.js`

## Running docs locally

```bash
make docs-install    # install npm dependencies (one-time)
make docs            # start English docs at http://localhost:3000
make docs-serve-it   # start Italian docs
make docs-build      # production build
make docs-test       # build with broken-link validation
```

## More information

- [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)
- [controller-runtime Documentation](https://pkg.go.dev/sigs.k8s.io/controller-runtime)

