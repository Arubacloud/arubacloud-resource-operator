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

Every CRD page follows this standard template (see `docs/website/docs/VPC.md` or `docs/website/docs/CloudServer.md` as reference examples):

```markdown
---
sidebar_position: <N>
---

# <Resource>

| Property | Value |
|----------|-------|
| **Kind** | `<Resource>` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `<plural>.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `<abbr>`, `aru<abbr>` |

## Description
2-4 sentences: what it represents, when to create it, role in the hierarchy.

## Spec Fields
| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
...rows...

## Status Fields
| Field | Type | Description |
|-------|------|-------------|
...common fields (phase, resourceID, message, observedGeneration, phaseStartTime, conditions) + resource-specific resolved IDs...

## References
- Ownership references (parent)
- Use references (consumed by other resources)

## Lifecycle
- Creation ordering (what must be Active first)
- Deletion behaviour (finalizer, cascade)
- Update behaviour (mutable fields, update cycle)
- Immutable fields (if any)

## Example
Complete YAML with realistic placeholder values (not __PLACEHOLDER__ style).

## kubectl Quick Reference
\`\`\`bash
kubectl get <shortname> -n <ns>
kubectl describe aru<shortname> <name> -n <ns>
\`\`\`
```

Additional conventions:
- **English is the source of truth.** Italian files mirror the English structure and translate all prose. Keep technical terms (Kind, CRD, Spec, Status, Phase, namespace, YAML, kubectl) and field names in backticks untranslated. Translate headings (e.g., "Description" → "Descrizione", "Lifecycle" → "Ciclo di vita").
- **YAML examples** use realistic placeholder values (e.g., `my-project`, `web-vpc`, `ARU-123456`), not `example-tenant` style.
- **Italian table headers** use: Campo, Tipo, Obbligatorio, Predefinito, Validazione, Descrizione, Proprietà, Valore.
- **`sidebars.js`** lists CRD pages as bare string IDs matching the filename without `.md` (e.g. `'VPC'`). New resources are appended to the CRDs category `items` array.
- **`sidebar_position`**: Project=2, VPC=3, Subnet=4, SecurityGroup=5, SecurityRule=6, KeyPair=7, ElasticIP=8, BlockStorage=9, CloudServer=10. New resources append beyond 10.

## 4. Build commands

```bash
make docs-install    # Install npm dependencies for the docs site (one-time)
make docs            # Start English locale dev server at http://localhost:3000
make docs-serve-it   # Start Italian locale dev server
make docs-build      # Production build of the docs site
make docs-test       # Build with validation (catches broken links)
```
