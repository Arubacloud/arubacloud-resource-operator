---
sidebar_position: 8
---

## ElasticIP

- **Kind**: `ElasticIP`
- **CRD**: `elasticips.arubacloud.com`
- **Scope**: Namespaced

Represents an Elastic IP that can be attached to a `CloudServer` via `spec.elasticIPReference`.

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: ElasticIP
metadata:
  name: example-eip
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
  region: ITBG-Bergamo
  zone: ITBG-1
```

