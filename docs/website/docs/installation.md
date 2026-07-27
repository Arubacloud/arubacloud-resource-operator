---
sidebar_position: 3
---

# Installation

## Prerequisites

- A Kubernetes cluster (v1.21+)
- Helm v3
- An Aruba Cloud account with API credentials
- For multi-tenant mode: a HashiCorp Vault instance (or let the chart install one — dev/demo only)

## Add the Helm Repository

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

## Install the CRDs

The operator does not work unless the CRDs are present in the cluster.

### Option 1: Automatic CRD installation (recommended)

By default the operator chart installs the CRD chart as a dependency, so a plain install brings both:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace
```

### Option 2: Manual CRD installation

If you manage CRDs separately (or they are already installed), disable the dependency:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace \
  --set crds.enabled=false
```

and install the CRD chart yourself:

```bash
helm install arubacloud-operator-crd arubacloud/arubacloud-resource-operator-crd
```

Verify:

```bash
kubectl get crds | grep arubacloud.com
```

## Install the Operator

The operator supports two authentication modes: **single** (one credential set for every resource) and **multi** (per-tenant credentials from HashiCorp Vault). Choose the mode that matches your environment with `config.auth.mode`.

### Single-Tenant (`config.auth.mode=single`)

All resources use the same Aruba Cloud API credentials. Use `spec.tenant` on each resource to specify the target tenant.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

Pinning a specific operator image:

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

In multi mode the operator retrieves per-tenant credentials from Vault using AppRole authentication. Each unique `spec.tenant` value on your resources triggers a separate Vault lookup at `<kv-mount>/data/<tenant>` (or `<kv-mount>/data/<kv-prefix>/<tenant>` when a prefix is configured).

Two sub-modes control how Vault is provisioned:

| `config.auth.multi.setup` | `vault.enabled` | Description |
|---|---|---|
| `auto` (default) | `true` | The chart installs Vault in dev mode and configures it automatically. Development/demo only. |
| `manual` | `false` | You provide a pre-existing Vault instance. All Vault parameters must be supplied. |

:::caution
The two values must agree. `setup=manual` with `vault.enabled=true` (or the reverse) fails the install — see [Troubleshooting](#troubleshooting).
:::

#### `setup=manual` — bring your own Vault

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.setup=manual \
  --set vault.enabled=false \
  --set config.auth.multi.vault.address=<vault-address> \
  --set config.auth.multi.vault.kvMount=<kv-mount> \
  --set config.auth.multi.vault.kvPrefix=<kv-prefix> \
  --set config.auth.multi.vault.rolePath=<approle-path> \
  --set config.auth.multi.vault.roleId=<vault-role-id> \
  --set config.auth.multi.vault.roleSecret=<vault-role-secret>
```

See [Configuring Vault](#configuring-vault-for-multi-tenant-mode) for how to produce those AppRole values.

#### `setup=auto` — chart-managed Vault (dev/demo only)

The chart installs Vault in dev mode (in-memory, no persistence) and configures the AppRole for you. **Not for production.**

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.setup=auto \
  --set vault.enabled=true
```

#### Using an existing Secret for the AppRole credentials

Instead of passing the AppRole credentials as values, you can reference an existing Kubernetes Secret — useful with external secret management tools.

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

Or with a values file:

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

## Configuring Vault for Multi-Tenant Mode

Only needed with `setup=manual`. Requirements:

- Vault is running and reachable from the cluster
- A root token (or a token with the capabilities below) is available
- The KV engine is enabled, or can be enabled

Export the connection details:

```bash
export VAULT_ADDRESS=http://localhost:8200
export VAULT_TOKEN=hvs.xxxxxxxxxxxxxxxxxxxx
```

1. Enable AppRole auth:

   ```bash
   vault auth enable approle
   ```

2. Write a policy granting read access to your KV path — `operator-policy.hcl`:

   ```hcl
   path "kv/data/*" {
     capabilities = ["read"]
   }
   ```

   ```bash
   vault policy write operator-policy operator-policy.hcl
   ```

3. Create the AppRole and assign the policy:

   ```bash
   vault write auth/approle/role/operator-role \
     token_policies="operator-policy" \
     secret_id_ttl=0 \
     secret_id_num_uses=0 \
     token_ttl=1h \
     token_max_ttl=4h
   ```

4. Read the Role ID (`config.auth.multi.vault.roleId`):

   ```bash
   vault read auth/approle/role/operator-role/role-id
   ```

   ```
   Key        Value
   ---        -----
   role_id    c7f48cd1-e464-7c80-b919-88b5a668e8f9
   ```

5. Generate the Secret ID (`config.auth.multi.vault.roleSecret`):

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

6. Enable KV v2 at your chosen path (`config.auth.multi.vault.kvMount`):

   ```bash
   vault secrets enable -path=kv kv-v2
   ```

7. Store the Aruba Cloud credentials for each tenant used in your CRs:

   ```bash
   vault kv put kv/my-tenant client-id="cmp-12345667" client-secret="xxxxxxxxxxxxxxxxxx"
   ```

:::tip
The tenant path must match the `spec.tenant` value on your resources, and each tenant secret must contain the `client-id` and `client-secret` keys.
:::

## Verify the Installation

```bash
# Check the operator is running
kubectl get pods -n aruba-system

# Verify CRDs are installed
kubectl get crd | grep arubacloud.com

# Follow the operator logs
kubectl logs -n aruba-system -l control-plane=controller-manager -f
```

You should see the operator pod in `Running` state and 9 CRDs registered:

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

## Configuration Reference

### Chart Values

| Name | Description | Default |
|------|-------------|---------|
| `crds.enabled` | Install the CRD chart as a dependency (`false` if you manage CRDs separately) | `true` |
| `kubernetesClusterDomain` | Kubernetes cluster domain | `cluster.local` |
| `config.gateway` | Aruba Cloud API gateway endpoint | `https://api.arubacloud.com` |
| `config.auth.idp` | Keycloak/IDP authentication URL | `https://login.aruba.it/auth` |
| `config.auth.realm` | API realm name | `cmp-new-apikey` |
| `config.auth.mode` | Authentication mode: `single` or `multi` | `single` |
| `config.auth.single.clientId` | OAuth client ID (required when mode is `single`) | `""` |
| `config.auth.single.clientSecret` | OAuth client secret (required when mode is `single`) | `""` |
| `config.auth.multi.setup` | Vault provisioning: `manual` (your Vault) or `auto` (chart-installed, dev/demo only) | `auto` |
| `config.auth.multi.vault.address` | Vault server address (required when mode is `multi`) | `http://vault:8200` |
| `config.auth.multi.vault.kvMount` | Vault KV mount path | `kv` |
| `config.auth.multi.vault.rolePath` | Vault AppRole auth mount path | `approle` |
| `config.auth.multi.vault.roleId` | Vault AppRole role ID (required unless `roleIdFrom` is set) | `""` |
| `config.auth.multi.vault.roleSecret` | Vault AppRole secret ID (required unless `roleSecretFrom` is set) | `""` |
| `config.auth.multi.vault.roleIdFrom.secretKeyRef` | Existing Secret reference for the AppRole role ID | — |
| `config.auth.multi.vault.roleSecretFrom.secretKeyRef` | Existing Secret reference for the AppRole secret ID | — |
| `config.auth.multi.vault.auto.namespace` | Namespace where Vault is deployed (`setup=auto`) | `vault` |
| `config.auth.multi.vault.auto.helmChartVersion` | Vault Helm chart version to install (`setup=auto`) | `0.32.0` |
| `config.auth.multi.vault.auto.devRootToken` | Vault dev root token used for initial configuration (`setup=auto`) | `root` |
| `controller.replicas` | Number of operator replicas | `1` |
| `controller.manager.image.tag` | Operator image tag | `latest` |
| `vault.enabled` | Install Vault as a sub-chart — `true` for `setup=auto`, `false` for `setup=manual` | `true` |
| `vault.server.dev.enabled` | Run the sub-chart Vault in dev mode (in-memory, **not for production**) | `true` |
| `vault.server.dev.devRootToken` | Dev root token | `root` |

Resources, node selector, tolerations, security contexts, service account and metrics service values are documented in the [chart's `values.yaml`](https://github.com/Arubacloud/helm-charts/tree/main/charts/arubacloud-resource-operator).

### ConfigMap and Secret Fields

The chart renders the values above into a **ConfigMap** and a **Secret** named `aruba-controller-manager` in the operator namespace. The operator reads only these; the table is useful when debugging a deployment or configuring the operator without Helm.

| ConfigMap Key | Required (single) | Required (multi) | Default | Description |
|-----|-------------------|-------------------|---------|-------------|
| `api-gateway` | Yes | Yes | `https://api.arubacloud.com` | Aruba Cloud API base URL |
| `keycloak-url` | Yes | Yes | `https://login.aruba.it/auth` | OAuth2 token issuer URL |
| `realm-api` | Yes | Yes | `cmp-new-apikey` | Keycloak realm name |
| `vault-enabled` | No | Yes (`"true"`) | *(not set)* | Enable Vault-based credential resolution |
| `vault-address` | No | Yes | — | Vault server URL |
| `role-path` | No | Yes | `approle` | Vault AppRole auth mount path |
| `kv-mount` | No | Yes | `kv` | Vault KV secrets engine mount path |
| `kv-prefix` | No | No | — | Optional path prefix prepended to the tenant: `<kv-mount>/<kv-prefix>/<tenant>` |
| `role-namespace` | No | No | — | Vault namespace (Vault Enterprise) |

| Secret Key | Required (single) | Required (multi) | Description |
|------------|-------------------|-------------------|-------------|
| `client-id` | Yes | No | Aruba Cloud OAuth2 client ID |
| `client-secret` | Yes | No | Aruba Cloud OAuth2 client secret |
| `role-id` | No | Yes | Vault AppRole role ID |
| `secret-id` | No | Yes | Vault AppRole secret ID |

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| Operator pod not starting | Check the CRDs are installed and the namespace exists. With `crds.enabled=true`, make sure Helm can reach the chart repository. |
| Missing CRDs | With `crds.enabled=false` you must install the `arubacloud-resource-operator-crd` chart yourself. |
| CRD version mismatch | CRDs installed separately must match the version expected by the operator. |
| Authentication errors | Verify the credentials (single mode) or the Vault AppRole values and Vault reachability (multi mode). |
| Resource creation failures | Check the operator logs; verify `config.gateway` and `config.auth.idp` are correct and reachable. |
| `vault.enabled=true conflicts with config.auth.multi.setup=manual` | You set `setup=manual` but left `vault.enabled=true`. Add `--set vault.enabled=false`. |
| `vault.enabled=false conflicts with config.auth.multi.setup=auto` | You set `vault.enabled=false` but left `setup=auto`. Either `--set vault.enabled=true` or switch to `setup=manual` and supply your own Vault parameters. |
| Operator pod stuck in `Init:0/1` (`setup=auto`) | The `wait-for-vault-credentials` initContainer is waiting for the vault-config Job to patch the operator Secret: `kubectl logs -n aruba-system -l app=vault-config`. |

## Uninstall

```bash
helm uninstall arubacloud-operator --namespace aruba-system
```

If the CRDs were installed as a dependency (`crds.enabled=true`) they are removed with the operator. If they were installed separately:

```bash
helm uninstall arubacloud-operator-crd
```

:::warning
Removing the CRDs deletes every Aruba Cloud resource defined in the cluster. Back up or migrate anything you still need first.
:::

```bash
kubectl delete namespace aruba-system
```

## Next Steps

- Browse the [CRDs](./crds) to understand the available resource types
- Follow the [Examples](./examples) for an end-to-end walkthrough
