---
sidebar_position: 4
---

## Subnet

- **Kind**: `Subnet`
- **CRD**: `subnets.arubacloud.com`
- **Scope**: Namespaced

Represents a subnet inside a `VPC`. `CloudServer` resources attach to one or more subnets via `spec.subnetReferences`.

### Common references

- `spec.projectReference`: owning `Project`
- `spec.vpcReference`: owning `VPC`

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Subnet
metadata:
  name: example-subnet
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
  vpcReference:
    name: example-vpc
    namespace: default
```

