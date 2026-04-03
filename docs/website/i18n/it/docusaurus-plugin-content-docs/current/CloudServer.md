---
sidebar_position: 10
---

# CloudServer

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `CloudServer` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `cloudservers.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `cs`, `arucs` |

## Descrizione

Un CloudServer è un'istanza di macchina virtuale su Aruba Cloud. È la risorsa con più riferimenti: un CloudServer collega un Project, un VPC, una o più Subnet, uno o più SecurityGroup, un KeyPair per l'accesso SSH, un volume BlockStorage avviabile per il sistema operativo, volumi dati opzionali e un ElasticIP opzionale per un indirizzo pubblico statico. Tutte le risorse referenziate devono essere `Active` prima che il CloudServer possa essere provisionato.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant di tutte le risorse referenziate. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud. Deve corrispondere alla region di tutte le risorse referenziate. |
| `zone` | string | Sì | `ITBG-1` | — | Zona di disponibilità. Deve corrispondere alla zona del volume di avvio. |
| `flavorName` | string | Sì | — | — | Identificatore del flavor/dimensione dell'istanza Aruba Cloud (es. `CSO4A8` per 4 vCPU / 8 GB RAM) |
| `vpcReference` | ResourceReference | Sì | — | — | Riferimento al [VPC](./VPC) per la connettività di rete |
| `subnetReferences` | []ResourceReference | Sì | — | Min elementi: 1 | Una o più [Subnet](./Subnet) a cui collegare il server |
| `securityGroupReferences` | []ResourceReference | Sì | — | Min elementi: 1 | Uno o più [SecurityGroup](./SecurityGroup) che governano il traffico del server |
| `keyPairReference` | ResourceReference | Sì | — | — | Riferimento al [KeyPair](./KeyPair) per l'accesso SSH |
| `bootVolumeReference` | ResourceReference | Sì | — | — | Riferimento a un volume [BlockStorage](./BlockStorage) avviabile (deve avere `bootable: true`) |
| `dataVolumeReferences` | []ResourceReference | No | — | — | Volumi dati [BlockStorage](./BlockStorage) aggiuntivi opzionali da collegare |
| `elasticIPReference` | ResourceReference | No | — | — | [ElasticIP](./ElasticIP) opzionale da assegnare come indirizzo pubblico statico |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID CloudServer Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |
| `vpcID` | string | ID del VPC Aruba Cloud risolto |
| `bootVolumeID` | string | ID del volume di avvio Aruba Cloud risolto |
| `elasticIPID` | string | ID ElasticIP Aruba Cloud risolto (se assegnato) |
| `keyPairID` | string | ID KeyPair Aruba Cloud risolto |
| `subnetIDs` | []string | ID subnet Aruba Cloud risolti |
| `securityGroupIDs` | []string | ID security group Aruba Cloud risolti |
| `dataVolumeIDs` | []string | ID volumi dati Aruba Cloud risolti |
| `volumeIDs` | []string | Tutti gli ID volume collegati al server (avvio + dati) |

## Riferimenti

**Di proprietà di:** [Project](./Project)

**Usa (non di proprietà di):**
- [VPC](./VPC) — ambiente di rete
- [Subnet](./Subnet) — collegamento di rete (uno o più)
- [SecurityGroup](./SecurityGroup) — policy firewall (uno o più)
- [KeyPair](./KeyPair) — accesso SSH
- [BlockStorage](./BlockStorage) — disco di avvio (obbligatorio) e dischi dati (opzionali)
- [ElasticIP](./ElasticIP) — IP pubblico statico (opzionale)

## Ciclo di vita

- **Ordine di creazione**: Il CloudServer è l'ultima risorsa da creare. Tutti i seguenti devono essere `Active` prima: Project, VPC, tutte le Subnet referenziate, tutti i SecurityGroup referenziati, il KeyPair e il BlockStorage di avvio. Anche l'ElasticIP deve essere `Active` se referenziato.
- **Validazione incrociata**: L'operatore verifica che tutte le risorse referenziate condividano lo stesso `tenant` e `region`. Verifica anche che i `projectReference` e `vpcReference` di tutte le Subnet referenziate corrispondano ai propri riferimenti del CloudServer. I mismatch causano `Failed+IntentionValidationFailed`.
- **Coerenza della zona**: La `zone` del CloudServer deve corrispondere alla `zone` del BlockStorage di avvio.
- **Comportamento all'eliminazione**: I CloudServer non hanno figli. Quando elimini un CloudServer, l'operatore chiama l'API Aruba Cloud per spegnerlo ed eliminarlo, poi rimuove il finalizer. Le risorse referenziate (volumi, coppie di chiavi, ecc.) **non** vengono eliminate automaticamente.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. La modifica delle risorse referenziate (aggiunta/rimozione di subnet, cambio del flavor) innesca un ciclo di aggiornamento. Alcuni aggiornamenti potrebbero non essere supportati dall'API Aruba Cloud — in tal caso, la risorsa entra brevemente in `Updating+Failed` e lo spec viene ripristinato per corrispondere allo stato cloud.

## Esempi

### CloudServer completo con ElasticIP e volume dati

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: web-server
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "CSO4A8"
  projectReference:
    name: my-project
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  subnetReferences:
    - name: web-subnet
      namespace: default
  securityGroupReferences:
    - name: web-sg
      namespace: default
  keyPairReference:
    name: my-ssh-key
    namespace: default
  bootVolumeReference:
    name: web-server-boot
    namespace: default
  dataVolumeReferences:
    - name: web-server-data
      namespace: default
  elasticIPReference:
    name: web-server-ip
    namespace: default
  tags:
    - production
    - web
```

### CloudServer minimale (senza ElasticIP, senza volumi dati)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: worker-node
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "CSO2A4"
  projectReference:
    name: my-project
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  subnetReferences:
    - name: web-subnet
      namespace: default
  securityGroupReferences:
    - name: web-sg
      namespace: default
  keyPairReference:
    name: my-ssh-key
    namespace: default
  bootVolumeReference:
    name: worker-boot
    namespace: default
```

## Riferimento rapido kubectl

```bash
# Elenca tutti i cloud server
kubectl get cs -n default

# Descrivi un server e visualizza tutti gli ID risolti nello status
kubectl describe arucs web-server -n default

# Osserva l'avanzamento del provisioning
kubectl get cs web-server -n default -w
```
