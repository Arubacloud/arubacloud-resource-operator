---
sidebar_position: 3
---

# VPC

| Property | Value |
|----------|-------|
| **Kind** | `VPC` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `vpcs.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `vpc`, `aruvpc` |

## Description

A VPC (Virtual Private Cloud) defines an isolated network environment within an Aruba Cloud project. It is the network container for Subnets and SecurityGroups, and CloudServers are attached to a VPC for network connectivity. A VPC must be created before any network-dependent resources (Subnet, SecurityGroup, CloudServer) can be provisioned.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning Project's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region where the VPC is created |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud VPC ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID from the referenced Project |

## References

**Owned by:** [Project](./Project)

**Parent of:**
- [Subnet](./Subnet) — via `spec.vpcReference`
- [SecurityGroup](./SecurityGroup) — via `spec.vpcReference`

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.vpcReference` (use-reference, not ownership)

## Lifecycle

- **Creation ordering**: The referenced Project must be `Active` before the VPC can be created.
- **Deletion behaviour**: The operator blocks VPC deletion while any Subnet or SecurityGroup belonging to it still exists. Delete those children first, or delete the VPC and let the operator cascade-delete them.
- **Update behaviour**: `tags` can be updated. `region` and `projectReference` are effectively immutable (changing the region would require recreating the VPC in the cloud).
- **Tenant validation**: The operator validates that `spec.tenant` matches the parent Project's `spec.tenant`. A mismatch causes `Failed+IntentionValidationFailed`.

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: VPC
metadata:
  name: web-vpc
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
```

## kubectl Quick Reference

```bash
# List all VPCs
kubectl get vpc -n default

# Describe a VPC
kubectl describe aruvpc web-vpc -n default

# Watch phase changes
kubectl get vpc web-vpc -n default -w
```
