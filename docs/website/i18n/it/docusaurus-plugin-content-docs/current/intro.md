---
sidebar_position: 1
---

# Panoramica

**Aruba Cloud Resource Operator** è un operatore Kubernetes che consente la gestione dichiarativa dell'infrastruttura Aruba Cloud tramite Custom Resources (CRD) Kubernetes. Invece di effettuare il provisioning di server, reti e storage tramite la console Aruba Cloud o le API direttamente, descrivi l'infrastruttura desiderata in file YAML e la applichi al tuo cluster Kubernetes. L'operatore riconcilia continuamente lo stato dichiarato con l'ambiente Aruba Cloud live, gestendo automaticamente creazione, aggiornamenti ed eliminazione.

## Come funziona

L'operatore installa un insieme di CRD nel gruppo API `arubacloud.com/v1alpha1`. Ogni CRD corrisponde a un tipo di risorsa Aruba Cloud (Project, VPC, CloudServer, ecc.). Quando crei, aggiorni o elimini uno di questi oggetti in Kubernetes, l'operatore rileva la modifica e chiama l'API Aruba Cloud per rispecchiarla. L'avanzamento viene riportato tramite `status.phase`, `status.resourceID` e `status.conditions`.

Le risorse sono organizzate in una **gerarchia padre-figlio** — ad esempio, un VPC appartiene a un Project e una Subnet appartiene a un VPC. L'operatore gestisce le dipendenze automaticamente: non creerà un figlio finché il suo genitore non è stato provisioning, ed eliminerà a cascata i figli quando un genitore viene rimosso. Consulta la pagina [Architettura](./architecture) per il diagramma completo della gerarchia e i dettagli del ciclo di vita.

## Modalità di tenancy

- **Modalità single-tenant**: tutte le risorse vengono create sotto un singolo tenant Aruba Cloud. Imposta `spec.tenant` su ciascuna risorsa con lo stesso identificatore ARU tenant.
- **Modalità multi-tenant**: l'operatore gestisce risorse su più tenant Aruba Cloud. Ogni risorsa può riferirsi a un `spec.tenant` diverso, e l'operatore risolve le credenziali per tenant (opzionalmente tramite HashiCorp Vault).

## Concetti chiave

| Concetto | Descrizione |
|----------|-------------|
| **CRD** | Una definizione di risorsa Kubernetes personalizzata che corrisponde a un tipo di risorsa Aruba Cloud |
| **Phase** | Lo stato corrente del ciclo di vita di una risorsa (`Pending`, `Creating`, `Active`, `Failed`, ecc.) |
| **Reference** | Un puntatore `name`/`namespace` da una CRD a un'altra (es. `spec.projectReference`) |
| **ResourceID** | L'identificatore lato Aruba Cloud assegnato dopo la creazione della risorsa, memorizzato in `status.resourceID` |
| **Riconciliazione** | Il ciclo continuo dell'operatore che confronta lo spec dichiarato con lo stato cloud live e li allinea |

## Prossimi passi

- Leggi [Architettura](./architecture) — diagramma di proprietà delle risorse, fasi del ciclo di vita, eliminazione a cascata
- Segui la guida di [Installazione](./installation) per distribuire l'operatore
- Consulta le [CRD](./crds) — tipi di risorse disponibili, ordine di creazione, pattern comuni
- Segui un [esempio end-to-end](./examples)
