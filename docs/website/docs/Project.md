---
sidebar_position: 2
---

## Project

- **Kind**: `Project`
- **CRD**: `projects.arubacloud.com`
- **Scope**: Namespaced

Represents an Aruba Cloud Project. Most other resources reference a `Project` via `spec.projectReference`.

### Key fields

- **spec.tenant**: owning account/tenant (immutable in most resources that carry it)
- **status.resourceID**: the remote Project ID in Aruba Cloud
- **status.phase**: reconciliation phase (e.g. `Pending`, `Creating`, `Active`, `Failed`)

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: example-project
spec:
  tenant: example-tenant
```

