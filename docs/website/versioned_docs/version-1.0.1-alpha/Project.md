---
sidebar_position: 2
---

# Project

| Property | Value |
|----------|-------|
| **Kind** | `Project` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `projects.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `prj`, `aruprj` |

## Description

A Project is the root resource in the Aruba Cloud Resource Operator hierarchy. It represents an Aruba Cloud account/tenant scope and acts as the parent for all other resource types. Every VPC, BlockStorage, KeyPair, ElasticIP, and CloudServer must reference a Project. Deleting a Project triggers cascade deletion of all resources that belong to it.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | **Immutable** after creation | Aruba Cloud tenant identifier (e.g., `ARU-123456`). Cannot be changed after the Project is created. |
| `description` | string | No | — | Max 1000 characters | Human-readable description of the project |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |

:::note
Project is the only resource where `spec.tenant` is immutable. All other resources allow `tenant` to be changed (useful when correcting a misconfiguration on a Failed resource).
:::

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase: `Pending`, `Creating`, `Active`, `Updating`, `Deleting`, `Deleted`, or `Failed` |
| `resourceID` | string | Aruba Cloud project ID, set after successful creation |
| `message` | string | Human-readable description of the current state |
| `observedGeneration` | int64 | Last `.metadata.generation` processed by the operator |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions with reason codes |

## References

**Owned by:** nothing (Project is the root resource)

**Parent of:**
- [VPC](./VPC) — via `spec.projectReference`
- [BlockStorage](./BlockStorage) — via `spec.projectReference`
- [KeyPair](./KeyPair) — via `spec.projectReference`
- [ElasticIP](./ElasticIP) — via `spec.projectReference`
- [CloudServer](./CloudServer) — via `spec.projectReference`

## Lifecycle

- **Creation ordering**: No dependencies. Create the Project first.
- **Deletion behaviour**: The operator blocks deletion (via finalizer) while any child resource (VPC, BlockStorage, KeyPair, ElasticIP, CloudServer) still exists. Delete those children first, or simply delete the Project and the operator will cascade-delete them automatically.
- **Update behaviour**: `description` and `tags` can be updated freely. `tenant` is immutable — it cannot be changed after the Project reaches `Active` phase.
- **Immutable fields**: `spec.tenant` (enforced by a CEL validation rule on the CRD)

## Example

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: my-project
  namespace: default
spec:
  tenant: ARU-123456
  description: "Production infrastructure project"
  tags:
    - production
    - platform
```

## kubectl Quick Reference

```bash
# List all projects
kubectl get prj -n default

# Describe a project and view its current phase
kubectl describe prj my-project -n default

# Watch phase changes
kubectl get prj my-project -n default -w
```
