---
sidebar_position: 4
---

# Subnet

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `Subnet` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `subnets.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `sn`, `arusn` |

## Descrizione

Una Subnet definisce un segmento di rete all'interno di un VPC. I CloudServer si collegano a una o più Subnet per ricevere connettività di rete. Ogni Subnet appartiene esattamente a un VPC e a un Project. Le Subnet supportano la configurazione DHCP e possono essere di tipo `Advanced` (funzionalità complete, con supporto DHCP) o `Basic`.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del VPC proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud. Deve corrispondere alla region del VPC genitore. |
| `type` | string | Sì | — | Enum: `Advanced`, `Basic` | Tipo di subnet. `Advanced` supporta il DHCP; `Basic` è una variante più semplice. |
| `cidr` | string | Sì | — | Pattern CIDR IPv4 (es. `10.0.1.0/24`) | L'intervallo di indirizzi IP per questa subnet |
| `dhcp.enabled` | bool | Sì | — | — | Se il DHCP è abilitato per questa subnet |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `vpcReference` | ResourceReference | Sì | — | — | Riferimento al [VPC](./VPC) proprietario |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID Subnet Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |
| `vpcID` | string | ID del VPC Aruba Cloud risolto |

## Riferimenti

**Di proprietà di:** [VPC](./VPC) (e [Project](./Project))

**Referenziata da:**
- [CloudServer](./CloudServer) — tramite `spec.subnetReferences` (una o più Subnet possono essere collegate)

## Ciclo di vita

- **Ordine di creazione**: Sia il VPC che il Project devono essere `Active` prima che la Subnet possa essere creata.
- **Comportamento all'eliminazione**: Le Subnet non hanno figli. L'eliminazione procede direttamente: l'operatore chiama l'API Aruba Cloud per eliminare la subnet, poi rimuove il finalizer.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. `cidr`, `type`, `dhcp.enabled` e i riferimenti ai genitori sono effettivamente immutabili (cambiare il CIDR di una subnet richiede di ricrearla).
- **Validazione tenant e region**: L'operatore verifica che `spec.tenant` e `spec.region` corrispondano al VPC genitore. I mismatch causano `Failed+IntentionValidationFailed`.

## Esempio

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Subnet
metadata:
  name: web-subnet
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  type: Advanced
  cidr: "10.0.1.0/24"
  dhcp:
    enabled: true
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
  tags:
    - production
```

## Riferimento rapido kubectl

```bash
# Elenca tutte le subnet
kubectl get sn -n default

# Descrivi una subnet
kubectl describe arusn web-subnet -n default
```
