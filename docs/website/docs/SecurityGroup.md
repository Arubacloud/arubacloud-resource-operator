---
sidebar_position: 5
---

## SecurityGroup

- **Kind**: `SecurityGroup`
- **CRD**: `securitygroups.arubacloud.com`
- **Scope**: Namespaced

Represents a security group. `CloudServer` resources reference one or more security groups via `spec.securityGroupReferences`.

### Common references

- `spec.projectReference`: owning `Project`
- `spec.vpcReference`: owning `VPC`

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityGroup
metadata:
  name: example-sg
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
  vpcReference:
    name: example-vpc
    namespace: default
```

