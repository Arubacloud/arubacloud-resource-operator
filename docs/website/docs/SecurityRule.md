---
sidebar_position: 6
---

## SecurityRule

- **Kind**: `SecurityRule`
- **CRD**: `securityrules.arubacloud.com`
- **Scope**: Namespaced

Represents an individual security rule associated with a `SecurityGroup`.

### Common references

- `spec.securityGroupReference`: target `SecurityGroup`
- `spec.projectReference`: owning `Project` (if present in your manifests/types)

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: example-rule
spec:
  tenant: example-tenant
  securityGroupReference:
    name: example-sg
    namespace: default
```

