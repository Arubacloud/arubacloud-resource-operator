---
sidebar_position: 7
---

# KeyPair

| Property | Value |
|----------|-------|
| **Kind** | `KeyPair` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `keypairs.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `kp`, `arukp` |

## Description

A KeyPair registers an SSH public key in Aruba Cloud. CloudServers reference a KeyPair to allow SSH access: when a CloudServer is provisioned, the public key is injected into the server, enabling passwordless login with the corresponding private key.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning Project's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region |
| `value` | string | Yes | — | Min length: 1 | The SSH **public key** string (e.g., `ssh-ed25519 AAAA...`) |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud KeyPair ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |

## References

**Owned by:** [Project](./Project)

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.keyPairReference`

## Lifecycle

- **Creation ordering**: The Project must be `Active`.
- **Deletion behaviour**: KeyPairs have no children. Deletion calls the Aruba Cloud API and removes the finalizer. Note: deleting a KeyPair that is currently referenced by a CloudServer may cause issues on the cloud side; delete the CloudServer first.
- **Update behaviour**: `tags` can be updated. `value` (the public key) is effectively immutable — changing it requires recreating the KeyPair.

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: KeyPair
metadata:
  name: my-ssh-key
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  value: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyValueHereReplaceWithYourPublicKey user@host"
  projectReference:
    name: my-project
    namespace: default
  tags:
    - platform-team
```

## kubectl Quick Reference

```bash
# List all key pairs
kubectl get kp -n default

# Describe a key pair
kubectl describe arukp my-ssh-key -n default
```
