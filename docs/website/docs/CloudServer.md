---
sidebar_position: 10
---

# CloudServer

| Property | Value |
|----------|-------|
| **Kind** | `CloudServer` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `cloudservers.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `cs`, `arucs` |

## Description

A CloudServer is a virtual machine instance on Aruba Cloud. It is the most reference-heavy resource: a CloudServer ties together a Project, a VPC, one or more Subnets, one or more SecurityGroups, a KeyPair for SSH access, a bootable BlockStorage volume for the operating system, optional data volumes, and an optional ElasticIP for a static public address. All referenced resources must be `Active` before the CloudServer can be provisioned.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match all referenced resources' tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region. Must match all referenced resources' region. |
| `zone` | string | Yes | `ITBG-1` | — | Availability zone. Must match the boot volume's zone. |
| `flavorName` | string | Yes | — | — | Aruba Cloud instance flavor/size identifier (e.g., `CSO4A8` for 4 vCPU / 8 GB RAM) |
| `vpcReference` | ResourceReference | Yes | — | — | Reference to the [VPC](./VPC) for network connectivity |
| `userData` | string | No | — | Immutable | cloud-init user data applied at first boot. Base64-encoded by the operator before it is sent to Aruba Cloud — write plain text here. |
| `subnetReferences` | []ResourceReference | Yes | — | Min items: 1 | One or more [Subnets](./Subnet) to attach the server to |
| `securityGroupReferences` | []ResourceReference | Yes | — | Min items: 1 | One or more [SecurityGroups](./SecurityGroup) governing the server's traffic |
| `keyPairReference` | ResourceReference | Yes | — | — | Reference to the [KeyPair](./KeyPair) for SSH access |
| `bootVolumeReference` | ResourceReference | Yes | — | — | Reference to a bootable [BlockStorage](./BlockStorage) volume (must have `bootable: true`) |
| `dataVolumeReferences` | []ResourceReference | No | — | — | Optional additional [BlockStorage](./BlockStorage) data volumes to attach |
| `elasticIPReference` | ResourceReference | No | — | — | Optional [ElasticIP](./ElasticIP) to assign as a static public address |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud CloudServer ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |
| `vpcID` | string | Resolved Aruba Cloud VPC ID |
| `bootVolumeID` | string | Resolved Aruba Cloud boot volume ID |
| `elasticIPID` | string | Resolved Aruba Cloud ElasticIP ID (if assigned) |
| `keyPairID` | string | Resolved Aruba Cloud KeyPair ID |
| `subnetIDs` | []string | Resolved Aruba Cloud subnet IDs |
| `securityGroupIDs` | []string | Resolved Aruba Cloud security group IDs |
| `dataVolumeIDs` | []string | Resolved Aruba Cloud data volume IDs |
| `volumeIDs` | []string | All volume IDs attached to the server (boot + data) |

## References

**Owned by:** [Project](./Project)

**Uses (not owned by):**
- [VPC](./VPC) — network environment
- [Subnet](./Subnet) — network attachment (one or more)
- [SecurityGroup](./SecurityGroup) — firewall policy (one or more)
- [KeyPair](./KeyPair) — SSH access
- [BlockStorage](./BlockStorage) — boot disk (required) and data disks (optional)
- [ElasticIP](./ElasticIP) — static public IP (optional)

## Lifecycle

- **Creation ordering**: The CloudServer is the last resource to create. All of the following must be `Active` first: Project, VPC, all referenced Subnets, all referenced SecurityGroups, the KeyPair, and the boot BlockStorage. ElasticIP must also be `Active` if referenced.
- **Cross-validation**: The operator validates that all referenced resources share the same `tenant` and `region`. It also validates that all referenced Subnets' `projectReference` and `vpcReference` match the CloudServer's own references. Mismatches cause `Failed+IntentionValidationFailed`.
- **Zone consistency**: The `zone` of the CloudServer must match the `zone` of the boot BlockStorage.
- **Deletion behaviour**: CloudServers have no children. When you delete a CloudServer, the operator calls the Aruba Cloud API to power it down and delete it, then removes the finalizer. Referenced resources (volumes, key pairs, etc.) are **not** deleted automatically.
- **Update behaviour**: `tags` and `region` are compared against the cloud state and trigger an update cycle when they differ. Some updates may not be supported by the Aruba Cloud API — in that case, the resource enters `Updating+Failed` briefly and the spec is rolled back to match the cloud state.
- **Immutable fields**: `zone`, `flavorName`, and `userData` cannot be changed after creation.
  - `zone` and `flavorName` are checked against the cloud state; editing either leaves the resource in an update-rejected state until you restore the original value.
  - `userData` is rejected by the API server at admission — the update is refused outright, so a `kubectl apply` that changes it fails with `userData is immutable`. cloud-init re-reads user data on every boot, so a new value could in principle apply on the next restart, but Aruba Cloud does not return `userData` on read and offers no way to change it after creation. Rejecting the edit is deliberate: the alternative is accepting a change that would never reach the server. To use different user data, delete the CloudServer and create it again.

## Example

### Full CloudServer with ElasticIP and data volume

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: web-server
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "CSO4A8"
  userData: |
    #cloud-config
    package_update: true
    packages:
      - nginx
    runcmd:
      - [ systemctl, enable, --now, nginx ]
  projectReference:
    name: my-project
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  subnetReferences:
    - name: web-subnet
      namespace: default
  securityGroupReferences:
    - name: web-sg
      namespace: default
  keyPairReference:
    name: my-ssh-key
    namespace: default
  bootVolumeReference:
    name: web-server-boot
    namespace: default
  dataVolumeReferences:
    - name: web-server-data
      namespace: default
  elasticIPReference:
    name: web-server-ip
    namespace: default
  tags:
    - production
    - web
```

### Minimal CloudServer (no ElasticIP, no data volumes)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: worker-node
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "CSO2A4"
  projectReference:
    name: my-project
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  subnetReferences:
    - name: web-subnet
      namespace: default
  securityGroupReferences:
    - name: web-sg
      namespace: default
  keyPairReference:
    name: my-ssh-key
    namespace: default
  bootVolumeReference:
    name: worker-boot
    namespace: default
```

## kubectl Quick Reference

```bash
# List all cloud servers
kubectl get cs -n default

# Describe a server and view all resolved IDs in status
kubectl describe arucs web-server -n default

# Watch provisioning progress
kubectl get cs web-server -n default -w
```
