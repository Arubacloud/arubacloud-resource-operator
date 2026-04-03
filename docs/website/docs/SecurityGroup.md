---
sidebar_position: 5
---

# SecurityGroup

| Property | Value |
|----------|-------|
| **Kind** | `SecurityGroup` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `securitygroups.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `sg`, `arusg` |

## Description

A SecurityGroup is a named firewall policy within a VPC. It acts as a container for SecurityRules, which define which network traffic is allowed in (ingress) or out (egress) of CloudServers that reference this group. A CloudServer can reference multiple SecurityGroups, and each SecurityGroup can contain multiple SecurityRules.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning VPC's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region. Must match the parent VPC's region. |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `vpcReference` | ResourceReference | Yes | — | — | Reference to the owning [VPC](./VPC) |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud SecurityGroup ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |
| `vpcID` | string | Resolved Aruba Cloud VPC ID |

## References

**Owned by:** [VPC](./VPC) (and [Project](./Project))

**Parent of:**
- [SecurityRule](./SecurityRule) — via `spec.securityGroupReference`

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.securityGroupReferences` (one or more SecurityGroups can be attached)

## Lifecycle

- **Creation ordering**: Both the VPC and Project must be `Active` before the SecurityGroup can be created.
- **Deletion behaviour**: The operator blocks SecurityGroup deletion while any SecurityRule belonging to it still exists. Delete the SecurityRules first, or delete the SecurityGroup and let the operator cascade-delete them.
- **Update behaviour**: `tags` can be updated. `region` and the parent references are effectively immutable.
- **Tenant and region validation**: The operator validates that `spec.tenant` and `spec.region` match the parent VPC. Mismatches cause `Failed+IntentionValidationFailed`.

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityGroup
metadata:
  name: web-sg
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
    - web
```

## kubectl Quick Reference

```bash
# List all security groups
kubectl get sg -n default

# Describe a security group and view its rules
kubectl describe arusg web-sg -n default
```
