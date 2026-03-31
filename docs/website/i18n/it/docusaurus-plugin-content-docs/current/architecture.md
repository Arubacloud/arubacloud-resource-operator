---
sidebar_position: 3
---

## Architettura

In sintesi:

- **CRD**: dichiari le risorse Aruba Cloud come oggetti Kubernetes (`apiVersion: arubacloud.com/v1alpha1`).
- **Controller / Reconciler**: osserva le CRD e porta continuamente lo stato remoto su Aruba Cloud a combaciare con lo `spec`.
- **Riferimenti**: le risorse sono collegate tra loro tramite campi `spec.*Reference(s)` (`name` + `namespace`).
- **Status**: l’avanzamento è visibile tramite `.status.phase`, `.status.resourceID` e `.status.conditions`.

