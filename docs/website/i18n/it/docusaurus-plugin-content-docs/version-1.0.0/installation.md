---
sidebar_position: 3
---

# Installazione

## Prerequisiti

- Un cluster Kubernetes (v1.21+)
- Helm v3
- Un account Aruba Cloud con credenziali API
- Per la modalità multi-tenant: un'istanza HashiCorp Vault (oppure lascia che sia la chart a installarla — solo per sviluppo/demo)

## Aggiungi il repository Helm

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

## Installa i CRD

L'operatore non funziona se i CRD non sono presenti nel cluster.

### Opzione 1: installazione automatica dei CRD (consigliata)

Per impostazione predefinita la chart dell'operatore installa la chart dei CRD come dipendenza, quindi una normale installazione le porta entrambe:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace
```

### Opzione 2: installazione manuale dei CRD

Se gestisci i CRD separatamente (o sono già installati), disabilita la dipendenza:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace \
  --set crds.enabled=false
```

e installa la chart dei CRD manualmente:

```bash
helm install arubacloud-operator-crd arubacloud/arubacloud-resource-operator-crd
```

Verifica:

```bash
kubectl get crds | grep arubacloud.com
```

## Installa l'operatore

L'operatore supporta due modalità di autenticazione: **single** (un unico set di credenziali per tutte le risorse) e **multi** (credenziali per tenant da HashiCorp Vault). Scegli la modalità adatta al tuo ambiente con `config.auth.mode`.

### Single-Tenant (`config.auth.mode=single`)

Tutte le risorse usano le stesse credenziali API Aruba Cloud. Usa `spec.tenant` su ciascuna risorsa per specificare il tenant di destinazione.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

Con una versione specifica dell'immagine dell'operatore:

```bash
helm upgrade --install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set controller.manager.image.tag=v0.0.1-alpha4 \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

### Multi-Tenant (`config.auth.mode=multi`)

In modalità multi l'operatore recupera le credenziali per tenant da Vault tramite autenticazione AppRole. Ogni valore univoco di `spec.tenant` sulle tue risorse attiva una ricerca Vault separata in `<kv-mount>/data/<tenant>` (o `<kv-mount>/data/<kv-prefix>/<tenant>` se è configurato un prefisso).

Due sotto-modalità controllano come viene fornito Vault:

| `config.auth.multi.setup` | `vault.enabled` | Descrizione |
|---|---|---|
| `auto` (predefinita) | `true` | La chart installa Vault in modalità dev e lo configura automaticamente. Solo sviluppo/demo. |
| `manual` | `false` | Fornisci un'istanza Vault preesistente. Tutti i parametri Vault devono essere specificati. |

:::caution
I due valori devono essere coerenti. `setup=manual` con `vault.enabled=true` (o viceversa) fa fallire l'installazione — vedi [Risoluzione dei problemi](#risoluzione-dei-problemi).
:::

#### `setup=manual` — usa il tuo Vault

```bash
helm upgrade --install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.setup=manual \
  --set config.gateway=<gateway-url> \
  --set config.auth.idp=<idp-url> \
  --set config.auth.realm=<realm-name> \
  --set vault.enabled=false \
  --set config.auth.multi.vault.address=<vault-address> \
  --set config.auth.multi.vault.kvMount=<kv-mount> \
  --set config.auth.multi.vault.kvPrefix=<kv-prefix> \
  --set config.auth.multi.vault.roleNamespace=<vault-namespace> \
  --set config.auth.multi.vault.rolePath=<approle-path> \
  --set config.auth.multi.vault.roleId=<vault-role-id> \
  --set config.auth.multi.vault.roleSecret=<vault-role-secret>
```

`kvPrefix` e `roleNamespace` sono opzionali. Ometti `kvPrefix` se i secret per tenant sono archiviati direttamente sotto `<kv-mount>/<tenant>` senza percorso intermedio. Ometti `roleNamespace` a meno che tu non utilizzi namespace Vault Enterprise.

Vedi [Configurare Vault](#configurare-vault-per-la-modalità-multi-tenant) per ottenere questi valori AppRole.

#### `setup=auto` — Vault gestito dalla chart (solo sviluppo/demo)

La chart installa Vault in modalità dev (in memoria, senza persistenza) e configura l'AppRole automaticamente. **Non adatto alla produzione.**

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.setup=auto \
  --set vault.enabled=true
```

#### Usare un Secret esistente per le credenziali AppRole

Invece di passare le credenziali AppRole come valori, puoi referenziare un Secret Kubernetes esistente — utile con strumenti esterni di gestione dei segreti.

```bash
kubectl create secret generic vault-approle-credentials \
  --namespace aruba-system \
  --from-literal=role-id=<your-role-id> \
  --from-literal=secret-id=<your-secret-id>
```

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.vault.address=<vault-address> \
  --set-json 'config.auth.multi.vault.roleIdFrom={"secretKeyRef":{"name":"vault-approle-credentials","key":"role-id"}}' \
  --set-json 'config.auth.multi.vault.roleSecretFrom={"secretKeyRef":{"name":"vault-approle-credentials","key":"secret-id"}}'
```

Oppure con un file di valori:

```yaml
config:
  auth:
    mode: multi
    multi:
      vault:
        address: http://vault0.default.svc.cluster.local:8200
        roleIdFrom:
          secretKeyRef:
            name: vault-approle-credentials
            key: role-id
        roleSecretFrom:
          secretKeyRef:
            name: vault-approle-credentials
            key: secret-id
```

## Configurare Vault per la modalità multi-tenant

Necessario solo con `setup=manual`. Requisiti:

- Vault è in esecuzione e raggiungibile dal cluster
- È disponibile un root token (o un token con le capability elencate sotto)
- Il motore KV è abilitato, o può essere abilitato

Esporta i dati di connessione:

```bash
export VAULT_ADDRESS=http://localhost:8200
export VAULT_TOKEN=hvs.xxxxxxxxxxxxxxxxxxxx
```

1. Abilita l'autenticazione AppRole:

   ```bash
   vault auth enable approle
   ```

2. Scrivi una policy che consenta la lettura del tuo percorso KV — `operator-policy.hcl`:

   ```hcl
   path "kv/data/*" {
     capabilities = ["read"]
   }
   ```

   ```bash
   vault policy write operator-policy operator-policy.hcl
   ```

3. Crea l'AppRole e assegna la policy:

   ```bash
   vault write auth/approle/role/operator-role \
     token_policies="operator-policy" \
     secret_id_ttl=0 \
     secret_id_num_uses=0 \
     token_ttl=1h \
     token_max_ttl=4h
   ```

4. Leggi il Role ID (`config.auth.multi.vault.roleId`):

   ```bash
   vault read auth/approle/role/operator-role/role-id
   ```

   ```
   Key        Value
   ---        -----
   role_id    c7f48cd1-e464-7c80-b919-88b5a668e8f9
   ```

5. Genera il Secret ID (`config.auth.multi.vault.roleSecret`):

   ```bash
   vault write -f auth/approle/role/operator-role/secret-id
   ```

   ```
   Key                   Value
   ---                   -----
   secret_id             1aee83c8-fafa-6cf9-cc84-fe1decd6625b
   secret_id_accessor    5d14319e-052c-fec6-42c0-9b6a643d0664
   secret_id_num_uses    0
   secret_id_ttl         0s
   ```

6. Abilita KV v2 sul percorso scelto (`config.auth.multi.vault.kvMount`):

   ```bash
   vault secrets enable -path=kv kv-v2
   ```

7. Memorizza le credenziali Aruba Cloud per ogni tenant usato nelle tue CR:

   ```bash
   vault kv put kv/my-tenant client-id="cmp-12345667" client-secret="xxxxxxxxxxxxxxxxxx"
   ```

:::tip
Il percorso del tenant deve corrispondere al valore `spec.tenant` delle tue risorse e ogni secret del tenant deve contenere le chiavi `client-id` e `client-secret`.
:::

## Verifica l'installazione

```bash
# Controlla che l'operatore sia in esecuzione
kubectl get pods -n aruba-system

# Verifica che i CRD siano installati
kubectl get crd | grep arubacloud.com

# Segui i log dell'operatore
kubectl logs -n aruba-system -l control-plane=controller-manager -f
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

### Valori della chart

| Nome | Descrizione | Predefinito |
|------|-------------|-------------|
| `crds.enabled` | Installa la chart dei CRD come dipendenza (`false` se gestisci i CRD separatamente) | `true` |
| `kubernetesClusterDomain` | Dominio del cluster Kubernetes | `cluster.local` |
| `config.gateway` | Endpoint del gateway API Aruba Cloud | `https://api.arubacloud.com` |
| `config.auth.idp` | URL di autenticazione Keycloak/IDP | `https://login.aruba.it/auth` |
| `config.auth.realm` | Nome del realm API | `cmp-new-apikey` |
| `config.auth.mode` | Modalità di autenticazione: `single` o `multi` | `single` |
| `config.auth.single.clientId` | Client ID OAuth (obbligatorio in modalità `single`) | `""` |
| `config.auth.single.clientSecret` | Client secret OAuth (obbligatorio in modalità `single`) | `""` |
| `config.auth.multi.setup` | Provisioning di Vault: `manual` (il tuo Vault) o `auto` (installato dalla chart, solo dev/demo) | `auto` |
| `config.auth.multi.vault.address` | Indirizzo del server Vault (obbligatorio in modalità `multi`) | `http://vault:8200` |
| `config.auth.multi.vault.kvMount` | Percorso di mount del motore KV Vault | `kv` |
| `config.auth.multi.vault.kvPrefix` | Prefisso di percorso opzionale anteposto al tenant nel mount KV: `<kv-mount>/<kv-prefix>/<tenant>` | `""` |
| `config.auth.multi.vault.rolePath` | Percorso di mount dell'autenticazione AppRole Vault | `approle` |
| `config.auth.multi.vault.roleNamespace` | Namespace Vault per l'autenticazione AppRole (solo Vault Enterprise) | `""` |
| `config.auth.multi.vault.roleId` | Role ID dell'AppRole Vault (obbligatorio se `roleIdFrom` non è impostato) | `""` |
| `config.auth.multi.vault.roleSecret` | Secret ID dell'AppRole Vault (obbligatorio se `roleSecretFrom` non è impostato) | `""` |
| `config.auth.multi.vault.roleIdFrom.secretKeyRef` | Riferimento a un Secret esistente per il role ID AppRole | — |
| `config.auth.multi.vault.roleSecretFrom.secretKeyRef` | Riferimento a un Secret esistente per il secret ID AppRole | — |
| `config.auth.multi.vault.auto.namespace` | Namespace in cui viene distribuito Vault (`setup=auto`) | `vault` |
| `config.auth.multi.vault.auto.helmChartVersion` | Versione della chart Helm di Vault da installare (`setup=auto`) | `0.32.0` |
| `config.auth.multi.vault.auto.devRootToken` | Root token dev di Vault usato per la configurazione iniziale (`setup=auto`) | `root` |
| `controller.replicas` | Numero di repliche dell'operatore | `1` |
| `controller.manager.image.tag` | Tag dell'immagine dell'operatore | `latest` |
| `vault.enabled` | Installa Vault come sub-chart — `true` per `setup=auto`, `false` per `setup=manual` | `true` |
| `vault.server.dev.enabled` | Esegue il Vault della sub-chart in modalità dev (in memoria, **non adatto alla produzione**) | `true` |
| `vault.server.dev.devRootToken` | Root token dev | `root` |

Risorse, node selector, tolerations, security context, service account e servizio delle metriche sono documentati nel [`values.yaml` della chart](https://github.com/Arubacloud/helm-charts/tree/main/charts/arubacloud-resource-operator).

### Campi ConfigMap e Secret

La chart traduce i valori sopra in una **ConfigMap** e un **Secret** denominati `aruba-controller-manager` nel namespace dell'operatore. L'operatore legge solo questi: la tabella è utile per il debug di un deployment o per configurare l'operatore senza Helm.

| Chiave ConfigMap | Obbligatorio (single) | Obbligatorio (multi) | Predefinito | Descrizione |
|--------|----------------------|---------------------|------------|-------------|
| `api-gateway` | Sì | Sì | `https://api.arubacloud.com` | URL base API Aruba Cloud |
| `keycloak-url` | Sì | Sì | `https://login.aruba.it/auth` | URL emettitore token OAuth2 |
| `realm-api` | Sì | Sì | `cmp-new-apikey` | Nome del realm Keycloak |
| `vault-enabled` | No | Sì (`"true"`) | *(non impostato)* | Abilita la risoluzione credenziali basata su Vault |
| `vault-address` | No | Sì | — | URL del server Vault |
| `role-path` | No | Sì | `approle` | Percorso di mount auth AppRole Vault |
| `kv-mount` | No | Sì | `kv` | Percorso di mount motore KV Vault |
| `kv-prefix` | No | No | — | Prefisso di percorso opzionale anteposto al tenant: `<kv-mount>/<kv-prefix>/<tenant>` |
| `role-namespace` | No | No | — | Namespace Vault (Vault Enterprise) |

| Chiave Secret | Obbligatorio (single) | Obbligatorio (multi) | Descrizione |
|--------|----------------------|---------------------|-------------|
| `client-id` | Sì | No | ID client OAuth2 Aruba Cloud |
| `client-secret` | Sì | No | Secret client OAuth2 Aruba Cloud |
| `role-id` | No | Sì | Role ID dell'AppRole Vault |
| `secret-id` | No | Sì | Secret ID dell'AppRole Vault |

## Risoluzione dei problemi

| Sintomo | Causa / soluzione |
|---|---|
| Il pod dell'operatore non parte | Verifica che i CRD siano installati e che il namespace esista. Con `crds.enabled=true`, assicurati che Helm raggiunga il repository della chart. |
| CRD mancanti | Con `crds.enabled=false` devi installare tu la chart `arubacloud-resource-operator-crd`. |
| Versione dei CRD non allineata | I CRD installati separatamente devono corrispondere alla versione attesa dall'operatore. |
| Errori di autenticazione | Verifica le credenziali (modalità single) oppure i valori AppRole e la raggiungibilità di Vault (modalità multi). |
| Creazione delle risorse fallita | Controlla i log dell'operatore; verifica che `config.gateway` e `config.auth.idp` siano corretti e raggiungibili. |
| `vault.enabled=true conflicts with config.auth.multi.setup=manual` | Hai impostato `setup=manual` lasciando `vault.enabled=true`. Aggiungi `--set vault.enabled=false`. |
| `vault.enabled=false conflicts with config.auth.multi.setup=auto` | Hai impostato `vault.enabled=false` lasciando `setup=auto`. Usa `--set vault.enabled=true` oppure passa a `setup=manual` fornendo i parametri del tuo Vault. |
| Pod dell'operatore bloccato in `Init:0/1` (`setup=auto`) | L'initContainer `wait-for-vault-credentials` attende che il Job vault-config aggiorni il Secret dell'operatore: `kubectl logs -n aruba-system -l app=vault-config`. |

## Disinstallazione

```bash
helm uninstall arubacloud-operator --namespace aruba-system
```

Se i CRD sono stati installati come dipendenza (`crds.enabled=true`) vengono rimossi insieme all'operatore. Se invece erano stati installati separatamente:

```bash
helm uninstall arubacloud-operator-crd
```

:::warning
La rimozione dei CRD elimina tutte le risorse Aruba Cloud definite nel cluster. Esegui un backup o migra ciò che ti serve prima di procedere.
:::

```bash
kubectl delete namespace aruba-system
```

## Prossimi passi

- Consulta le [CRD](./crds) per comprendere i tipi di risorse disponibili
- Segui gli [Esempi](./examples) per una guida end-to-end
