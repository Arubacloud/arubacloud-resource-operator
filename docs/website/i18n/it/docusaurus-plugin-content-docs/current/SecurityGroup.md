---
sidebar_position: 5
---

# SecurityGroup

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `SecurityGroup` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `securitygroups.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `sg`, `arusg` |

## Descrizione

Un SecurityGroup è una policy firewall nominata all'interno di un VPC. Funge da contenitore per le SecurityRule, che definiscono quale traffico di rete è consentito in entrata (ingress) o in uscita (egress) per i CloudServer che referenziano questo gruppo. Un CloudServer può referenziare più SecurityGroup, e ogni SecurityGroup può contenere più SecurityRule.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del VPC proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud. Deve corrispondere alla region del VPC genitore. |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `vpcReference` | ResourceReference | Sì | — | — | Riferimento al [VPC](./VPC) proprietario |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID SecurityGroup Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |
| `vpcID` | string | ID del VPC Aruba Cloud risolto |

## Riferimenti

**Di proprietà di:** [VPC](./VPC) (e [Project](./Project))

**Genitore di:**
- [SecurityRule](./SecurityRule) — tramite `spec.securityGroupReference`

**Referenziato da:**
- [CloudServer](./CloudServer) — tramite `spec.securityGroupReferences` (uno o più SecurityGroup possono essere collegati)

## Ciclo di vita

- **Ordine di creazione**: Sia il VPC che il Project devono essere `Active` prima che il SecurityGroup possa essere creato.
- **Comportamento all'eliminazione**: L'operatore blocca l'eliminazione del SecurityGroup finché esistono SecurityRule ad esso appartenenti. Elimina prima le SecurityRule, oppure elimina il SecurityGroup e lascia che l'operatore le elimini a cascata.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. `region` e i riferimenti ai genitori sono effettivamente immutabili.
- **Validazione tenant e region**: L'operatore verifica che `spec.tenant` e `spec.region` corrispondano al VPC genitore. I mismatch causano `Failed+IntentionValidationFailed`.

## Esempio

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityGroup
metadata:
  name: web-sg
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
    - web
```

## Riferimento rapido kubectl

```bash
# Elenca tutti i security group
kubectl get sg -n default

# Descrivi un security group e visualizza le sue regole
kubectl describe arusg web-sg -n default
```
