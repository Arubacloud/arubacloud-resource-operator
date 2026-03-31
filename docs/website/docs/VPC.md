---
sidebar_position: 3
---

## VPC

- **Kind**: `VPC`
- **CRD**: `vpcs.arubacloud.com`
- **Scope**: Namespaced

Defines a Virtual Private Cloud. Other network resources (like `Subnet`) and compute resources (like `CloudServer`) reference a VPC.

### Common references

- `spec.projectReference`: owning `Project`

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: VPC
metadata:
  name: example-vpc
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
```

