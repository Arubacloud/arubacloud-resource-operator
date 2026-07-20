---
sidebar_position: 4
---

# CRDs Introduction

The operator exposes Aruba Cloud resources as Kubernetes Custom Resources under:

- **API group**: `arubacloud.com`
- **Version**: `v1alpha1`
- **Scope**: Namespaced (all resources)

For a visual overview of how resources relate to each other, see the [Resource Ownership diagram](./architecture#resource-ownership) on the Architecture page.

## Available Resources

| Resource | Description |
|----------|-------------|
| [Project](./Project) | Root resource. Represents an Aruba Cloud project/account. All other resources belong to a Project. |
| [VPC](./VPC) | Virtual Private Cloud. Network container for Subnets, SecurityGroups, and CloudServers. |
| [Subnet](./Subnet) | A subnet within a VPC. CloudServers attach to one or more Subnets. |
| [SecurityGroup](./SecurityGroup) | A firewall group within a VPC. Contains SecurityRules. |
| [SecurityRule](./SecurityRule) | An ingress or egress firewall rule within a SecurityGroup. |
| [KeyPair](./KeyPair) | An SSH public key registered in Aruba Cloud. Used by CloudServers for access. |
| [ElasticIP](./ElasticIP) | A static public IP address. Optionally attached to a CloudServer. |
| [BlockStorage](./BlockStorage) | A block storage volume. Can be used as a boot disk or data volume for a CloudServer. |
| [CloudServer](./CloudServer) | A virtual machine. References most other resource types. |

## Creation Ordering

Resources must be created in dependency order. The operator holds a resource in `Pending` phase until all its dependencies are `Active`.

1. **Project** — no dependencies; create first
2. **VPC** — requires Project to be Active
3. **(parallel)** The following can all be created at the same time, as long as their parent is Active:
   - **Subnet** — requires VPC Active
   - **SecurityGroup** — requires VPC Active
   - **BlockStorage** — requires Project Active
   - **KeyPair** — requires Project Active
   - **ElasticIP** — requires Project Active
4. **SecurityRule** — requires SecurityGroup Active
5. **CloudServer** — requires Project, VPC, all referenced Subnets, SecurityGroups, KeyPair, and boot BlockStorage to be Active

## Deletion Ordering

When you delete resources manually, delete them in reverse creation order to avoid dependency conflicts:

1. **CloudServer** — delete first
2. **SecurityRule**, **ElasticIP** (if attached) — delete before their parents
3. **Subnet**, **SecurityGroup**, **KeyPair**, **BlockStorage** — delete before VPC/Project
4. **VPC** — delete after all children are gone
5. **Project** — delete last

:::tip
If you want to tear down everything at once, just delete the **Project**. The operator will cascade-delete all children automatically in the correct order.
:::

## Common Patterns

### Cross-Resource References

All references use a `ResourceReference` object with `name` and `namespace`:

```yaml
projectReference:
  name: my-project
  namespace: default
```

References can span namespaces. The operator reads `status.resourceID` from the referenced object to resolve the Aruba Cloud-side ID.

### Common Fields

Every resource has these fields:

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `spec.tenant` | string | Optional | Aruba Cloud tenant identifier (ARU-XXXXXX). Mutable, except on Project where it is immutable. |
| `spec.tags` | []string | Optional | Labels propagated to the Aruba Cloud resource |

Every resource **except Project** also has:

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `spec.region` | string | Yes | `ITBG-Bergamo` | Aruba Cloud region |
| `spec.projectReference` | ResourceReference | Yes | — | Owning Project |

### Status Reporting

All resources expose a standard status:

| Field | Description |
|-------|-------------|
| `status.phase` | Current lifecycle phase: `Pending`, `Creating`, `Active`, `Updating`, `Deleting`, `Deleted`, or `Failed` |
| `status.resourceID` | Aruba Cloud-side identifier, set after the resource is created |
| `status.message` | Human-readable description of the current state |
| `status.conditions` | Kubernetes-standard conditions with fine-grained reason codes |

See the [Architecture](./architecture#conditions) page for a full list of condition reason codes.
