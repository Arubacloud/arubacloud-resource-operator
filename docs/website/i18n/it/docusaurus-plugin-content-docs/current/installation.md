---
sidebar_position: 3
---

# Installazione

## Prerequisiti

- Un cluster Kubernetes (v1.26+)
- Helm v3
- Un account Aruba Cloud con credenziali API

## Aggiungi il repository Helm

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

## Installa la chart dei CRD

I CRD sono distribuiti come chart Helm separata. Installala per prima:

```bash
helm install arubacloud-crd arubacloud/arubacloud-resource-operator-crd \
  --namespace aruba-system \
  --create-namespace
```

## Installa l'operatore

L'operatore supporta due modalità di autenticazione: **Diretta** (singolo set di credenziali) e **Vault** (credenziali per tenant da HashiCorp Vault). Scegli la modalità adatta al tuo ambiente.

### Modalità Diretta (Single-Tenant)

In modalità diretta, tutte le risorse usano le stesse credenziali API Aruba Cloud. Usa `spec.tenant` su ciascuna risorsa per specificare il tenant di destinazione.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system
```

Dopo l'installazione, crea il Secret necessario con le tue credenziali API:

```bash
kubectl create secret generic aruba-controller-manager \
  --namespace aruba-system \
  --from-literal=client-id=<your-client-id> \
  --from-literal=client-secret=<your-client-secret> \
  --from-literal=role-id=placeholder \
  --from-literal=secret-id=placeholder \
  --dry-run=client -o yaml | kubectl apply -f -
```

:::note
La chart Helm crea un Secret con i campi `role-id` e `secret-id` (usati in modalità Vault). Per la modalità diretta, è necessario aggiungere `client-id` e `client-secret` al Secret. Il comando sopra sostituisce il Secret con tutti e quattro i campi.
:::

L'operatore legge la configurazione da una ConfigMap denominata `aruba-controller-manager` nello stesso namespace. La chart Helm la crea automaticamente con valori predefiniti ragionevoli:

| Chiave ConfigMap | Predefinito | Descrizione |
|------------------|------------|-------------|
| `api-gateway` | `https://api.arubacloud.com` | Endpoint API Aruba Cloud |
| `keycloak-url` | `https://login.aruba.it/auth` | Emettitore token OAuth2 |
| `realm-api` | `cmp-new-apikey` | Realm Keycloak |

### Modalità Vault (Multi-Tenant)

In modalità Vault, l'operatore recupera le credenziali per tenant da un'istanza HashiCorp Vault usando l'autenticazione AppRole. Ogni valore univoco di `spec.tenant` sulle tue risorse attiva una ricerca Vault separata.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --set controllerManager.vaultAddress=https://vault.example.com:8200 \
  --set controllerManager.kvMount=secret \
  --set controllerManager.rolePath=approle \
  --set controllerManager.roleId=<your-vault-role-id> \
  --set controllerManager.roleSecretID=<your-vault-secret-id>
```

Dopo l'installazione, abilita la modalità Vault modificando la ConfigMap:

```bash
kubectl patch configmap aruba-controller-manager \
  --namespace aruba-system \
  --type merge \
  -p '{"data":{"vault-enabled":"true"}}'
```

Poi riavvia l'operatore per applicare la modifica:

```bash
kubectl rollout restart deployment -n aruba-system -l app.kubernetes.io/name=operator
```

L'insieme completo dei campi di configurazione relativi a Vault:

| Chiave ConfigMap | Valore Helm | Descrizione |
|------------------|-------------|-------------|
| `vault-enabled` | *(da patchare manualmente)* | Impostare a `"true"` per abilitare la modalità Vault |
| `vault-address` | `controllerManager.vaultAddress` | URL del server Vault |
| `kv-mount` | `controllerManager.kvMount` | Percorso di mount del motore KV Vault |
| `role-path` | `controllerManager.rolePath` | Percorso di mount dell'autenticazione AppRole Vault |

| Chiave Secret | Valore Helm | Descrizione |
|---------------|-------------|-------------|
| `role-id` | `controllerManager.roleId` | ID del ruolo AppRole Vault |
| `secret-id` | `controllerManager.roleSecretID` | ID del secret AppRole Vault |

:::tip
In modalità Vault, l'operatore si aspetta credenziali per tenant memorizzate in `<kv-mount>/data/<tenant>` in Vault. Ogni secret del tenant dovrebbe contenere le chiavi `client-id` e `client-secret`.
:::

## Verifica l'installazione

```bash
# Controlla che l'operatore sia in esecuzione
kubectl get pods -n aruba-system

# Verifica che i CRD siano installati
kubectl get crd | grep arubacloud.com
```

Dovresti vedere il pod dell'operatore in stato `Running` e 9 CRD registrati:

```
blockstorages.arubacloud.com
cloudservers.arubacloud.com
elasticips.arubacloud.com
keypairs.arubacloud.com
projects.arubacloud.com
securitygroups.arubacloud.com
securityrules.arubacloud.com
subnets.arubacloud.com
vpcs.arubacloud.com
```

## Riferimento configurazione

L'operatore legge tutta la configurazione da una **ConfigMap** e un **Secret** nel suo namespace (nomi predefiniti: `aruba-controller-manager`).

### Campi ConfigMap

| Chiave | Obbligatorio (Diretta) | Obbligatorio (Vault) | Predefinito | Descrizione |
|--------|----------------------|---------------------|------------|-------------|
| `api-gateway` | Sì | Sì | `https://api.arubacloud.com` | URL base API Aruba Cloud |
| `keycloak-url` | Sì | Sì | `https://login.aruba.it/auth` | URL emettitore token OAuth2 |
| `realm-api` | Sì | Sì | `cmp-new-apikey` | Nome del realm Keycloak |
| `vault-enabled` | No | Sì (`"true"`) | *(non impostato)* | Abilita la risoluzione credenziali basata su Vault |
| `vault-address` | No | Sì | — | URL del server Vault |
| `role-path` | No | Sì | `approle` | Percorso di mount auth AppRole Vault |
| `kv-mount` | No | Sì | `kw` | Percorso di mount motore KV Vault |
| `role-namespace` | No | No | — | Namespace Vault (se usi Vault Enterprise) |

### Campi Secret

| Chiave | Obbligatorio (Diretta) | Obbligatorio (Vault) | Descrizione |
|--------|----------------------|---------------------|-------------|
| `client-id` | Sì | No | ID client OAuth2 Aruba Cloud |
| `client-secret` | Sì | No | Secret client OAuth2 Aruba Cloud |
| `role-id` | No | Sì | ID del ruolo AppRole Vault |
| `secret-id` | No | Sì | ID del secret AppRole Vault |

## Prossimi passi

- Consulta le [CRD](./crds) per comprendere i tipi di risorse disponibili
- Segui gli [Esempi](./examples) per una guida end-to-end
