---
sidebar_position: 7
---

# KeyPair

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `KeyPair` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `keypairs.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `kp`, `arukp` |

## Descrizione

Un KeyPair registra una chiave pubblica SSH in Aruba Cloud. I CloudServer referenziano un KeyPair per consentire l'accesso SSH: quando viene provisionato un CloudServer, la chiave pubblica viene iniettata nel server, abilitando il login senza password con la corrispondente chiave privata.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del Project proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud |
| `value` | string | Sì | — | Lunghezza minima: 1 | La stringa della **chiave pubblica** SSH (es. `ssh-ed25519 AAAA...`) |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID KeyPair Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |

## Riferimenti

**Di proprietà di:** [Project](./Project)

**Referenziato da:**
- [CloudServer](./CloudServer) — tramite `spec.keyPairReference`

## Ciclo di vita

- **Ordine di creazione**: Il Project deve essere `Active`.
- **Comportamento all'eliminazione**: I KeyPair non hanno figli. L'eliminazione chiama l'API Aruba Cloud e rimuove il finalizer. Nota: eliminare un KeyPair attualmente referenziato da un CloudServer potrebbe causare problemi lato cloud; elimina prima il CloudServer.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. `value` (la chiave pubblica) è effettivamente immutabile — per modificarla è necessario ricreare il KeyPair.

## Esempio

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: KeyPair
metadata:
  name: my-ssh-key
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  value: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyReplaceWithYourPublicKey user@host"
  projectReference:
    name: my-project
    namespace: default
  tags:
    - platform-team
```

## Riferimento rapido kubectl

```bash
# Elenca tutte le coppie di chiavi
kubectl get kp -n default

# Descrivi una coppia di chiavi
kubectl describe arukp my-ssh-key -n default
```
