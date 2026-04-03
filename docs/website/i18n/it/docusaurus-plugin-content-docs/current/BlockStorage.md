---
sidebar_position: 9
---

# BlockStorage

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `BlockStorage` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `blockstorages.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `bs`, `arubs` |

## Descrizione

Un BlockStorage rappresenta un volume di storage a blocchi in Aruba Cloud. I volumi vengono collegati ai CloudServer come **disco di avvio** (che contiene l'immagine del sistema operativo) o come **dischi dati** aggiuntivi. Un CloudServer deve avere esattamente un BlockStorage avviabile referenziato tramite `spec.bootVolumeReference`, e può avere opzionalmente volumi dati aggiuntivi tramite `spec.dataVolumeReferences`.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del Project proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud |
| `zone` | string | Sì | `ITBG-1` | — | Zona di disponibilità all'interno della region. Deve corrispondere alla zona del CloudServer. |
| `sizeGB` | int32 | Sì | `20` | Min: 1, Max: 16384 | Dimensione del volume in gigabyte |
| `billingPeriod` | string | Sì | `Hour` | Enum: `Hour`, `Month` | Ciclo di fatturazione per il volume |
| `type` | string | No | — | Enum: `Standard`, `Performance` | Livello di storage. `Performance` usa SSD; `Standard` usa HDD. |
| `bootable` | bool | No | — | — | Impostare a `true` se questo volume verrà usato come disco di avvio del CloudServer |
| `image` | string | No | — | — | ID dell'immagine del sistema operativo (es. `LU20-001`). Richiesto quando `bootable: true`. |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

:::note
Quando crei un volume avviabile, devi impostare `bootable: true` **e** fornire un `image` ID valido. L'ID immagine identifica il template OS da installare (es. Ubuntu, Debian). Contatta il supporto Aruba Cloud o consulta le immagini disponibili nel tuo account per gli ID immagine validi.
:::

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID BlockStorage Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |

## Riferimenti

**Di proprietà di:** [Project](./Project)

**Referenziato da:**
- [CloudServer](./CloudServer) — tramite `spec.bootVolumeReference` (disco di avvio) o `spec.dataVolumeReferences` (disco dati)

## Ciclo di vita

- **Ordine di creazione**: Il Project deve essere `Active`.
- **Coerenza della zona**: La `zone` del BlockStorage deve corrispondere alla `zone` del CloudServer. L'operatore valida questo prima del provisioning del CloudServer.
- **Comportamento all'eliminazione**: I BlockStorage non hanno figli. Elimina prima il CloudServer che usa questo volume.
- **Comportamento all'aggiornamento**: `sizeGB` può tipicamente essere aumentato (il ridimensionamento verso il basso non è supportato). `type`, `image` e `bootable` sono effettivamente immutabili dopo la creazione.

## Esempi

### Volume avviabile (disco OS)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: web-server-boot
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 40
  billingPeriod: Hour
  type: Performance
  bootable: true
  image: "LU20-001"
  projectReference:
    name: my-project
    namespace: default
  tags:
    - boot-disk
```

### Volume dati (disco aggiuntivo)

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: web-server-data
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 200
  billingPeriod: Month
  type: Standard
  bootable: false
  projectReference:
    name: my-project
    namespace: default
  tags:
    - data-disk
```

## Riferimento rapido kubectl

```bash
# Elenca tutti i volumi di block storage
kubectl get bs -n default

# Descrivi un volume
kubectl describe arubs web-server-boot -n default
```
