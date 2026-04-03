---
sidebar_position: 8
---

# ElasticIP

| Property | Value |
|----------|-------|
| **Kind** | `ElasticIP` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `elasticips.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `eip`, `arueip` |

## Description

An ElasticIP is a static public IP address that can be assigned to a CloudServer. Unlike the ephemeral IPs that Aruba Cloud may assign by default, an ElasticIP persists independently of any server — you can reserve it, attach it to a server, and later reassign it to a different server without losing the IP address. ElasticIPs are optional: a CloudServer can be created without one.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning Project's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region where the IP is reserved |
| `billingPeriod` | string | Yes | `Hour` | Enum: `Hour`, `Month` | Billing cycle for the reserved IP address. `Month` is typically cheaper for long-running workloads. |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

:::note
The `billingPeriod` affects cost from the moment the ElasticIP is created, regardless of whether it is attached to a CloudServer. Reserve ElasticIPs only when needed.
:::

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud ElasticIP ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |

## References

**Owned by:** [Project](./Project)

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.elasticIPReference` (optional)

## Lifecycle

- **Creation ordering**: The Project must be `Active`. An ElasticIP can be created before a CloudServer and attached later.
- **Deletion behaviour**: ElasticIPs have no children. Delete the CloudServer that references this ElasticIP first (or remove the reference) before deleting the ElasticIP.
- **Update behaviour**: `tags` and `billingPeriod` can potentially be updated. The IP address itself is assigned by Aruba Cloud and cannot be chosen.

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: ElasticIP
metadata:
  name: web-server-ip
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  billingPeriod: Hour
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
    - web
```

## kubectl Quick Reference

```bash
# List all elastic IPs
kubectl get eip -n default

# Describe an elastic IP and view its assigned address (in resourceID)
kubectl describe arueip web-server-ip -n default
```
