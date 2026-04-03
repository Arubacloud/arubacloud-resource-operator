---
sidebar_position: 9
---

# BlockStorage

| Property | Value |
|----------|-------|
| **Kind** | `BlockStorage` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **CRD Name** | `blockstorages.arubacloud.com` |
| **Scope** | Namespaced |
| **Short Names** | `bs`, `arubs` |

## Description

A BlockStorage represents a block storage volume in Aruba Cloud. Volumes are attached to CloudServers either as the **boot disk** (which contains the operating system image) or as additional **data disks**. A CloudServer must have exactly one bootable BlockStorage referenced via `spec.bootVolumeReference`, and may optionally have additional data volumes via `spec.dataVolumeReferences`.

## Spec Fields

| Field | Type | Required | Default | Validation | Description |
|-------|------|----------|---------|------------|-------------|
| `tenant` | string | No | — | — | Aruba Cloud tenant identifier. Must match the owning Project's tenant. |
| `region` | string | Yes | `ITBG-Bergamo` | — | Aruba Cloud region |
| `zone` | string | Yes | `ITBG-1` | — | Availability zone within the region. Must match the CloudServer's zone. |
| `sizeGB` | int32 | Yes | `20` | Min: 1, Max: 16384 | Volume size in gigabytes |
| `billingPeriod` | string | Yes | `Hour` | Enum: `Hour`, `Month` | Billing cycle for the volume |
| `type` | string | No | — | Enum: `Standard`, `Performance` | Storage tier. `Performance` uses SSDs; `Standard` uses HDDs. |
| `bootable` | bool | No | — | — | Set to `true` if this volume will be used as a CloudServer boot disk |
| `image` | string | No | — | — | Operating system image ID (e.g., `LU20-001`). Required when `bootable: true`. |
| `tags` | []string | No | — | — | Labels propagated to the Aruba Cloud resource |
| `projectReference` | ResourceReference | Yes | — | — | Reference to the owning [Project](./Project) |

:::note
When creating a bootable volume, you must set `bootable: true` **and** provide a valid `image` ID. The image ID identifies the OS template to install (e.g., Ubuntu, Debian). Contact Aruba Cloud support or refer to your account's available images for valid image IDs.
:::

## Status Fields

| Field | Type | Description |
|-------|------|-------------|
| `phase` | string | Current lifecycle phase |
| `resourceID` | string | Aruba Cloud BlockStorage ID, set after creation |
| `message` | string | Human-readable status message |
| `observedGeneration` | int64 | Last `.metadata.generation` processed |
| `phaseStartTime` | timestamp | When the current phase began |
| `conditions` | []Condition | Standard Kubernetes conditions |
| `projectID` | string | Resolved Aruba Cloud project ID |

## References

**Owned by:** [Project](./Project)

**Referenced by:**
- [CloudServer](./CloudServer) — via `spec.bootVolumeReference` (boot disk) or `spec.dataVolumeReferences` (data disk)

## Lifecycle

- **Creation ordering**: The Project must be `Active`.
- **Zone consistency**: The BlockStorage's `zone` must match the CloudServer's `zone`. The operator validates this before provisioning the CloudServer.
- **Deletion behaviour**: BlockStorages have no children. Delete the CloudServer that uses this volume before deleting it.
- **Update behaviour**: `sizeGB` can typically be increased (resizing down is not supported). `type`, `image`, and `bootable` are effectively immutable after creation.

## Examples

### Bootable volume (OS disk)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: web-server-boot
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 40
  billingPeriod: Hour
  type: Performance
  bootable: true
  image: "LU20-001"
  projectReference:
    name: my-project
    namespace: default
  tags:
    - boot-disk
```

### Data volume (additional disk)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: web-server-data
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 200
  billingPeriod: Month
  type: Standard
  bootable: false
  projectReference:
    name: my-project
    namespace: default
  tags:
    - data-disk
```

## kubectl Quick Reference

```bash
# List all block storage volumes
kubectl get bs -n default

# Describe a volume
kubectl describe arubs web-server-boot -n default
```
