---
sidebar_position: 2
---

## Project

- **Kind**: `Project`
- **CRD**: `projects.arubacloud.com`
- **Scope**: Namespaced

Rappresenta un progetto Aruba Cloud. La maggior parte delle altre risorse lo referenzia tramite `spec.projectReference`.

### Campi principali

- **spec.tenant**: account/tenant proprietario
- **status.resourceID**: ID del Project remoto in Aruba Cloud
- **status.phase**: fase di riconciliazione (es. `Pending`, `Creating`, `Active`, `Failed`)

