---
sidebar_position: 6
---

# SecurityRule

| Property | Value |
|----------|-------|
| **Kind** | `SecurityRule` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `securityrules.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `sr`, `arusr` |

## Description

A SecurityRule defines a single ingress or egress traffic rule within a SecurityGroup. Rules specify the protocol, port range, direction, and target (an IP/CIDR or another SecurityGroup). CloudServers that reference the parent SecurityGroup are governed by all its SecurityRules.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning SecurityGroup's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region. Must match the parent SecurityGroup's region. |
| `protocol` | string | Yes | — | Enum: `TCP`, `UDP`, `ICMP`, `ALL` | Network protocol this rule applies to |
| `port` | string | Yes | — | — | Port or port range: `"80"`, `"8080-8090"`, or `"ALL"` |
| `direction` | string | Yes | — | Enum: `Ingress`, `Egress` | Whether this rule applies to incoming or outgoing traffic |
| `target.type` | string | Yes | — | Enum: `Ip`, `SecurityGroup` | Whether the target is an IP/CIDR or another SecurityGroup |
| `target.value` | string | Yes | — | — | The IP address, CIDR block (e.g., `0.0.0.0/0`), or SecurityGroup ID |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `securityGroupReference` | ResourceReference | Yes | — | — | Reference to the owning [SecurityGroup](./SecurityGroup) |
| `vpcReference` | ResourceReference | Yes | — | — | Reference to the owning [VPC](./VPC) |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud SecurityRule ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |
| `vpcID` | string | Resolved Aruba Cloud VPC ID |
| `securityGroupID` | string | Resolved Aruba Cloud SecurityGroup ID |

:::tip
SecurityRule exposes extra `kubectl get` columns for `Protocol` and `Direction`, making it easy to see rule details at a glance:
```bash
kubectl get sr -n default
# NAME          PROTOCOL   DIRECTION   PHASE    AGE
# allow-ssh     TCP        Ingress     Active   5m
```
:::

## References

**Owned by:** [SecurityGroup](./SecurityGroup) (and [VPC](./VPC), [Project](./Project))

**No children.**

## Lifecycle

- **Creation ordering**: The SecurityGroup (and its parents VPC and Project) must be `Active`.
- **Deletion behaviour**: SecurityRules have no children. Deletion calls the Aruba Cloud API and removes the finalizer.
- **Update behaviour**: `tags` can be updated. All other fields (protocol, port, direction, target) are effectively immutable — recreate the rule to change them.

## Examples

### Ingress rule: allow SSH from anywhere

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: allow-ssh-ingress
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  protocol: TCP
  port: "22"
  direction: Ingress
  target:
    type: Ip
    value: "0.0.0.0/0"
  securityGroupReference:
    name: web-sg
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

### Egress rule: allow all outbound traffic

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: allow-all-egress
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  protocol: ALL
  port: "ALL"
  direction: Egress
  target:
    type: Ip
    value: "0.0.0.0/0"
  securityGroupReference:
    name: web-sg
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

## kubectl Quick Reference

```bash
# List all security rules with protocol and direction columns
kubectl get sr -n default

# Describe a security rule
kubectl describe arusr allow-ssh-ingress -n default
```
