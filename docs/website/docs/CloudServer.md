---
sidebar_position: 10
---

## CloudServer

- **Kind**: `CloudServer`
- **CRD**: `cloudservers.arubacloud.com`
- **Scope**: Namespaced
- **Short names**: `cs`, `arucs`

Represents a VM instance. It ties together networking (VPC/Subnet/SecurityGroup), identity (KeyPair), and storage (BlockStorage volumes).

### Key references (from the CRD schema)

- `spec.projectReference` (required)
- `spec.vpcReference` (required)
- `spec.subnetReferences` (required, min 1)
- `spec.securityGroupReferences` (required, min 1)
- `spec.bootVolumeReference` (required)
- `spec.dataVolumeReferences` (optional)
- `spec.keyPairReference` (required)
- `spec.elasticIPReference` (optional)

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: example-server
spec:
  tenant: example-tenant
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "small"
  projectReference:
    name: example-project
    namespace: default
  vpcReference:
    name: example-vpc
    namespace: default
  subnetReferences:
    - name: example-subnet
      namespace: default
  securityGroupReferences:
    - name: example-sg
      namespace: default
  keyPairReference:
    name: example-keypair
    namespace: default
  bootVolumeReference:
    name: example-volume
    namespace: default
```

