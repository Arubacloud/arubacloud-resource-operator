---
sidebar_position: 4
---

# Subnet

| Property | Value |
|----------|-------|
| **Kind** | `Subnet` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `subnets.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `sn`, `arusn` |

## Description

A Subnet defines a network segment within a VPC. CloudServers attach to one or more Subnets to receive network connectivity. Each Subnet belongs to exactly one VPC and one Project. Subnets support DHCP configuration and can be of type `Advanced` (full-featured, with DHCP support) or `Basic`.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning VPC's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region. Must match the parent VPC's region. |
| `type` | string | Yes | — | Enum: `Advanced`, `Basic` | Subnet type. `Advanced` supports DHCP; `Basic` is a simpler variant. |
| `cidr` | string | Yes | — | IPv4 CIDR pattern (e.g., `10.0.1.0/24`) | The IP address range for this subnet |
| `dhcp.enabled` | bool | Yes | — | — | Whether DHCP is enabled for this subnet |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `vpcReference` | ResourceReference | Yes | — | — | Reference to the owning [VPC](./VPC) |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud Subnet ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |
| `vpcID` | string | Resolved Aruba Cloud VPC ID |

## References

**Owned by:** [VPC](./VPC) (and [Project](./Project))

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.subnetReferences` (one or more Subnets can be attached)

## Lifecycle

- **Creation ordering**: Both the VPC and Project must be `Active` before the Subnet can be created.
- **Deletion behaviour**: Subnets have no children. Deletion proceeds directly: the operator calls the Aruba Cloud API to delete the subnet, then removes the finalizer.
- **Update behaviour**: `tags` can be updated. `cidr`, `type`, `dhcp.enabled`, and the parent references are effectively immutable (changing a subnet's CIDR requires recreating it).
- **Tenant and region validation**: The operator validates that `spec.tenant` and `spec.region` match the parent VPC. Mismatches cause `Failed+IntentionValidationFailed`.

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Subnet
metadata:
  name: web-subnet
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  type: Advanced
  cidr: "10.0.1.0/24"
  dhcp:
    enabled: true
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
```

## kubectl Quick Reference

```bash
# List all subnets
kubectl get sn -n default

# Describe a subnet
kubectl describe arusn web-subnet -n default
```
