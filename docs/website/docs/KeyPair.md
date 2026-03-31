---
sidebar_position: 7
---

## KeyPair

- **Kind**: `KeyPair`
- **CRD**: `keypairs.arubacloud.com`
- **Scope**: Namespaced

Represents an SSH key pair in Aruba Cloud. `CloudServer` references it via `spec.keyPairReference`.

### Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: KeyPair
metadata:
  name: example-keypair
spec:
  tenant: example-tenant
  projectReference:
    name: example-project
    namespace: default
  publicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA..."
```

