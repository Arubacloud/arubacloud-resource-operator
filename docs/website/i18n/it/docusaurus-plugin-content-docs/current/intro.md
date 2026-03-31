---
sidebar_position: 1
---

## Panoramica

**Arubacloud Resource Operator** è un operatore Kubernetes che abilita la gestione dichiarativa delle risorse Aruba Cloud tramite Custom Resources Kubernetes. Questo operatore ti consente di effettuare provisioning e gestire infrastruttura Aruba Cloud usando strumenti e workflow Kubernetes familiari.

### Cosa puoi gestire

L’operatore installa CRD nel gruppo API `arubacloud.com` (versione `v1alpha1`) e riconcilia le risorse verso Aruba Cloud.

### Modalità di tenancy

Puoi eseguire l’operatore in:

- **Modalità single-tenant**: l’operatore crea risorse Aruba Cloud per uno **specifico ARU Tenant**.
- **Modalità multi-tenant**: l’operatore può effettuare provisioning e gestire risorse Aruba Cloud su **più ARU Tenant**.

### Prossimi passi

- Leggi [Architettura](./architecture)
- Segui [Installazione](./installation)
- Parti dall’[Introduzione](./crds) delle CRD

