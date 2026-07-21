---
sidebar_position: 3
---

# VPC

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `VPC` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `vpcs.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `vpc`, `aruvpc` |

## Descrizione

Un VPC (Virtual Private Cloud) definisce un ambiente di rete isolato all'interno di un progetto Aruba Cloud. È il contenitore di rete per Subnet e SecurityGroup, e i CloudServer vengono collegati a un VPC per la connettività di rete. Un VPC deve essere creato prima che qualsiasi risorsa dipendente dalla rete (Subnet, SecurityGroup, CloudServer) possa essere provisionata.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del Project proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud in cui viene creato il VPC |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID del VPC Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto dal Project referenziato |

## Riferimenti

**Di proprietà di:** [Project](./Project)

**Genitore di:**
- [Subnet](./Subnet) — tramite `spec.vpcReference`
- [SecurityGroup](./SecurityGroup) — tramite `spec.vpcReference`

**Referenziato da:**
- [CloudServer](./CloudServer) — tramite `spec.vpcReference` (riferimento d'uso, non di proprietà)

## Ciclo di vita

- **Ordine di creazione**: Il Project referenziato deve essere `Active` prima che il VPC possa essere creato.
- **Comportamento all'eliminazione**: L'operatore blocca l'eliminazione del VPC finché esistono Subnet o SecurityGroup ad esso appartenenti. Elimina prima quei figli, oppure elimina il VPC e lascia che l'operatore li elimini a cascata.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. `region` e `projectReference` sono effettivamente immutabili (cambiare la region richiederebbe di ricreare il VPC nel cloud).
- **Validazione tenant**: L'operatore verifica che `spec.tenant` corrisponda al `spec.tenant` del Project genitore. Un mismatch causa `Failed+IntentionValidationFailed`.

## Esempio

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: VPC
metadata:
  name: web-vpc
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
```

## Riferimento rapido kubectl

```bash
# Elenca tutti i VPC
kubectl get vpc -n default

# Descrivi un VPC
kubectl describe aruvpc web-vpc -n default

# Osserva i cambiamenti di fase
kubectl get vpc web-vpc -n default -w
```
