---
sidebar_position: 1
---

## CRD

Questo operatore espone le risorse Aruba Cloud come Custom Resources Kubernetes sotto:

- **API group**: `arubacloud.com`
- **versione**: `v1alpha1`
- **scope**: principalmente `Namespaced`

### Pattern comuni

- **Riferimenti tra risorse**: le risorse si collegano tra loro tramite oggetti `spec.<...>Reference(s)` con `name` e `namespace`.
- **Stato**: le risorse espongono `.status.phase`, `.status.resourceID` e `.status.conditions` per descrivere l’avanzamento della riconciliazione.

### Risorse disponibili

- [Project](./Project)
- [VPC](./VPC)
- [Subnet](./Subnet)
- [SecurityGroup](./SecurityGroup)
- [SecurityRule](./SecurityRule)
- [KeyPair](./KeyPair)
- [ElasticIP](./ElasticIP)
- [BlockStorage](./BlockStorage)
- [CloudServer](./CloudServer)

