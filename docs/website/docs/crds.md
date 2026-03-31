---
sidebar_position: 1
---

## CRDs

This operator exposes Aruba Cloud resources as Kubernetes Custom Resources under:

- **API group**: `arubacloud.com`
- **version**: `v1alpha1`
- **scope**: mostly `Namespaced`

### Common patterns

- **Cross-resource references**: resources link to each other via `spec.<...>Reference(s)` objects with `name` and `namespace`.
- **Status reporting**: resources expose a `.status.phase`, `.status.resourceID`, and `.status.conditions` to describe reconciliation progress.

### Available resources

- [Project](./Project)
- [VPC](./VPC)
- [Subnet](./Subnet)
- [SecurityGroup](./SecurityGroup)
- [SecurityRule](./SecurityRule)
- [KeyPair](./KeyPair)
- [ElasticIP](./ElasticIP)
- [BlockStorage](./BlockStorage)
- [CloudServer](./CloudServer)

