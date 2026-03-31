---
sidebar_position: 9
---

## BlockStorage

- **Kind**: `BlockStorage`
- **CRD**: `blockstorages.arubacloud.com`
- **Scope**: Namespaced

Represents a block storage volume. `CloudServer` references volumes via:

- `spec.bootVolumeReference` (required)
- `spec.dataVolumeReferences` (optional)

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: example-volume
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 20
```

