---
sidebar_position: 2
---

# Project

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `Project` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `projects.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `prj`, `aruprj` |

## Descrizione

Un Project è la risorsa radice nella gerarchia di Aruba Cloud Resource Operator. Rappresenta lo scope di un account/tenant Aruba Cloud e funge da genitore per tutti gli altri tipi di risorse. Ogni VPC, BlockStorage, KeyPair, ElasticIP e CloudServer deve fare riferimento a un Project. L'eliminazione di un Project attiva l'eliminazione a cascata di tutte le risorse che gli appartengono.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | **Immutabile** dopo la creazione | Identificatore del tenant Aruba Cloud (es. `ARU-123456`). Non può essere modificato dopo la creazione del Project. |
| `description` | string | No | — | Max 1000 caratteri | Descrizione leggibile del progetto |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |

:::note
Project è l'unica risorsa in cui `spec.tenant` è immutabile. Tutte le altre risorse consentono di modificare `tenant` (utile per correggere una configurazione errata su una risorsa in stato Failed).
:::

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita: `Pending`, `Creating`, `Active`, `Updating`, `Deleting`, `Deleted` o `Failed` |
| `resourceID` | string | ID del progetto Aruba Cloud, impostato dopo la creazione |
| `message` | string | Descrizione leggibile dello stato corrente |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato dall'operatore |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard con codici reason |

## Riferimenti

**Di proprietà di:** nessuno (Project è la risorsa radice)

**Genitore di:**
- [VPC](./VPC) — tramite `spec.projectReference`
- [BlockStorage](./BlockStorage) — tramite `spec.projectReference`
- [KeyPair](./KeyPair) — tramite `spec.projectReference`
- [ElasticIP](./ElasticIP) — tramite `spec.projectReference`
- [CloudServer](./CloudServer) — tramite `spec.projectReference`

## Ciclo di vita

- **Ordine di creazione**: Nessuna dipendenza. Creare il Project per primo.
- **Comportamento all'eliminazione**: L'operatore blocca l'eliminazione (tramite finalizer) finché esiste qualsiasi risorsa figlia (VPC, BlockStorage, KeyPair, ElasticIP, CloudServer). Elimina prima quei figli, oppure elimina il Project e l'operatore li eliminerà a cascata automaticamente.
- **Comportamento all'aggiornamento**: `description` e `tags` possono essere aggiornati liberamente. `tenant` è immutabile — non può essere modificato dopo che il Project ha raggiunto la fase `Active`.
- **Campi immutabili**: `spec.tenant` (applicato da una regola di validazione CEL sulla CRD)

## Esempio

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: my-project
  namespace: default
spec:
  tenant: ARU-123456
  description: "Progetto infrastruttura di produzione"
  tags:
    - production
    - platform
```

## Riferimento rapido kubectl

```bash
# Elenca tutti i project
kubectl get prj -n default

# Descrivi un project e visualizza la fase corrente
kubectl describe prj my-project -n default

# Osserva i cambiamenti di fase
kubectl get prj my-project -n default -w
```
