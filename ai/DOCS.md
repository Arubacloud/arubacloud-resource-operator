# Documentation

This file describes the user-facing documentation site and provides guidance on when and how to update it during development.

## 1. Triggers — when to update docs

| Change type | Docs update? | What to update |
|---|---|---|
| New CRD type | Yes | New `<Resource>.md` (EN + IT), `crds.md`, `sidebars.js` |
| CRD spec field added/removed/renamed | Yes | `<Resource>.md` (EN + IT) |
| CRD field semantics changed (mutability, validation) | Yes | `<Resource>.md` (EN + IT) |
| New cross-resource reference | Yes | Both referencing and referenced `<Resource>.md` |
| Installation/configuration changes | Yes | `installation.md` |
| Architecture changes (reconciliation, ownership model) | Yes | `architecture.md` |
| New sample manifests demonstrating a new pattern | Likely | `examples.md` |
| Internal refactor (no API/behaviour change) | No | — |
| Bug fix (no API/behaviour change) | No | — |
| Test-only / build-only changes | No | — |

## 2. File mapping — where to make changes

| Code change location | Documentation file(s) |
|---|---|
| `api/v1alpha1/<resource>_types.go` | `docs/website/docs/<Resource>.md` + IT mirror |
| `api/v1alpha1/<resource>_types.go` (new file) | New `docs/website/docs/<Resource>.md` + IT mirror + `crds.md` + `sidebars.js` |
| `internal/controller/<resource>_controller.go` (user-visible behaviour) | `docs/website/docs/<Resource>.md` |
| `config/samples/` | `docs/website/docs/examples.md` (if new pattern) |
| `cmd/main.go` (flags, startup config) | `docs/website/docs/installation.md` |
| `internal/reconciler/` (framework-level changes) | `docs/website/docs/architecture.md` |

Key paths:
- English docs: `docs/website/docs/`
- Italian docs: `docs/website/i18n/it/docusaurus-plugin-content-docs/current/`
- Sidebar config: `docs/website/sidebars.js`

## 3. Conventions — how to write CRD doc pages

Every CRD page follows this template (see `docs/website/docs/VPC.md` as a reference example):

```markdown
---
sidebar_position: <N>
---

## <Resource>

- **Kind**: `<Resource>`
- **CRD**: `<plural>.arubacloud.com`
- **Scope**: Namespaced

<One or two sentences describing what this resource represents.>

### Key fields *(include only if the resource has notable spec fields beyond references)*

- **`spec.<field>`**: <description>

### Common references

- `spec.<parent>Reference`: owning `<Parent>`

### Example

\`\`\`yaml
apiVersion: arubacloud.com/v1alpha1
kind: <Resource>
metadata:
  name: example-<resource>
spec:
  tenant: example-tenant
  ...
\`\`\`
```

Additional conventions:
- **English is the source of truth.** Italian files mirror the English structure and translate prose. When unable to translate, copy the English content and add `<!-- TODO: translate to Italian -->` at the top of the Italian file.
- **YAML examples** use placeholder values: `example-tenant`, `example-<resource>`, namespace `default`.
- **`sidebars.js`** lists CRD pages as bare string IDs matching the filename without `.md` (e.g. `'VPC'`). New resources are appended to the CRDs category `items` array.
- **`sidebar_position`** increments from 2 (Project) through the sidebar order. Match the order in `sidebars.js`.

## 4. Build commands

```bash
make docs-install    # Install npm dependencies for the docs site (one-time)
make docs            # Start English locale dev server at http://localhost:3000
make docs-serve-it   # Start Italian locale dev server
make docs-build      # Production build of the docs site
make docs-test       # Build with validation (catches broken links)
```
