---
sidebar_position: 3
---

# Installation

## Prerequisites

- A Kubernetes cluster (v1.26+)
- Helm v3
- An Aruba Cloud account with API credentials

## Add the Helm Repository

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

## Install the CRD Chart

The CRDs are distributed as a separate Helm chart. Install them first:

```bash
helm install arubacloud-crd arubacloud/arubacloud-resource-operator-crd \
  --namespace aruba-system \
  --create-namespace
```

## Install the Operator

The operator supports two authentication modes: **Direct** (single credential set) and **Vault** (per-tenant credentials from HashiCorp Vault). Choose the mode that matches your environment.

### Direct Mode (Single-Tenant)

In Direct mode, all resources use the same Aruba Cloud API credentials. Use `spec.tenant` on each resource to specify the target tenant.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system
```

After installation, create the required Secret with your API credentials:

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
The Helm chart creates a Secret with `role-id` and `secret-id` fields (used in Vault mode). For Direct mode, you need to add `client-id` and `client-secret` to that Secret. The command above replaces the Secret with all four fields.
:::

The operator reads its configuration from a ConfigMap named `aruba-controller-manager` in the same namespace. The Helm chart creates it automatically with sensible defaults:

| ConfigMap Key | Default | Description |
|---------------|---------|-------------|
| `api-gateway` | `https://api.arubacloud.com` | Aruba Cloud API endpoint |
| `keycloak-url` | `https://login.aruba.it/auth` | OAuth2 token issuer |
| `realm-api` | `cmp-new-apikey` | Keycloak realm |

### Vault Mode (Multi-Tenant)

In Vault mode, the operator retrieves per-tenant credentials from a HashiCorp Vault instance using AppRole authentication. Each unique `spec.tenant` value on your resources triggers a separate Vault lookup.

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --set controllerManager.vaultAddress=https://vault.example.com:8200 \
  --set controllerManager.kvMount=secret \
  --set controllerManager.rolePath=approle \
  --set controllerManager.roleId=<your-vault-role-id> \
  --set controllerManager.roleSecretID=<your-vault-secret-id>
```

After installation, enable Vault mode by patching the ConfigMap:

```bash
kubectl patch configmap aruba-controller-manager \
  --namespace aruba-system \
  --type merge \
  -p '{"data":{"vault-enabled":"true"}}'
```

Then restart the operator to pick up the change:

```bash
kubectl rollout restart deployment -n aruba-system -l app.kubernetes.io/name=operator
```

The full set of Vault-related configuration fields:

| ConfigMap Key | Helm Value | Description |
|---------------|------------|-------------|
| `vault-enabled` | *(must be patched manually)* | Set to `"true"` to enable Vault mode |
| `vault-address` | `controllerManager.vaultAddress` | Vault server URL |
| `kv-mount` | `controllerManager.kvMount` | Vault KV secrets engine mount path |
| `role-path` | `controllerManager.rolePath` | Vault AppRole auth mount path |

| Secret Key | Helm Value | Description |
|------------|------------|-------------|
| `role-id` | `controllerManager.roleId` | Vault AppRole role ID |
| `secret-id` | `controllerManager.roleSecretID` | Vault AppRole secret ID |

:::tip
In Vault mode, the operator expects per-tenant credentials stored at `<kv-mount>/data/<tenant>` in Vault. Each tenant's secret should contain `client-id` and `client-secret` keys.
:::

## Verify the Installation

```bash
# Check the operator is running
kubectl get pods -n aruba-system

# Verify CRDs are installed
kubectl get crd | grep arubacloud.com
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

The operator reads all configuration from a **ConfigMap** and a **Secret** in its namespace (default names: `aruba-controller-manager`).

### ConfigMap Fields

| Key | Required (Direct) | Required (Vault) | Default | Description |
|-----|-------------------|-------------------|---------|-------------|
| `api-gateway` | Yes | Yes | `https://api.arubacloud.com` | Aruba Cloud API base URL |
| `keycloak-url` | Yes | Yes | `https://login.aruba.it/auth` | OAuth2 token issuer URL |
| `realm-api` | Yes | Yes | `cmp-new-apikey` | Keycloak realm name |
| `vault-enabled` | No | Yes (`"true"`) | *(not set)* | Enable Vault-based credential resolution |
| `vault-address` | No | Yes | — | Vault server URL |
| `role-path` | No | Yes | `approle` | Vault AppRole auth mount path |
| `kv-mount` | No | Yes | `kw` | Vault KV secrets engine mount path |
| `role-namespace` | No | No | — | Vault namespace (if using Vault Enterprise) |

### Secret Fields

| Key | Required (Direct) | Required (Vault) | Description |
|-----|-------------------|-------------------|-------------|
| `client-id` | Yes | No | Aruba Cloud OAuth2 client ID |
| `client-secret` | Yes | No | Aruba Cloud OAuth2 client secret |
| `role-id` | No | Yes | Vault AppRole role ID |
| `secret-id` | No | Yes | Vault AppRole secret ID |

## Next Steps

- Browse the [CRDs](./crds) to understand the available resource types
- Follow the [Examples](./examples) for an end-to-end walkthrough
