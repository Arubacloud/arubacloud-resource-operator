# Arubacloud Resource Operator

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest) [![Tests](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml) [![Release](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml)

## Overview

The Arubacloud Resource Operator is a Kubernetes operator that enables declarative management of Aruba Cloud resources through Kubernetes Custom Resources. This operator allows you to provision and manage Aruba Cloud infrastructure using familiar Kubernetes tools and workflows.

## Installation

Requirements: Kubernetes v1.21+, Helm v3, an Aruba Cloud account with API credentials.

### Install the Chart

Add the arubacloud Helm repository (if not already added):
```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

The CRDs are installed automatically as a chart dependency (`crds.enabled=true`, the default). To manage them yourself, install with `--set crds.enabled=false` and install the `arubacloud-resource-operator-crd` chart separately.

#### Single-Tenant Installation (Default)

For single-tenant deployments with direct OAuth credentials:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

#### Multi-Tenant Installation (Vault-based)

Multi-tenant deployments read per-tenant credentials from HashiCorp Vault via AppRole. `config.auth.multi.setup` selects how Vault is provisioned: `manual` (your own Vault, requires `vault.enabled=false`) or `auto` (the chart installs Vault in dev mode — development/demo only, requires `vault.enabled=true`).

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.setup=manual \
  --set vault.enabled=false \
  --set config.auth.multi.vault.address=<vault-address> \
  --set config.auth.multi.vault.kvMount=<vault-kv-mount> \
  --set config.auth.multi.vault.rolePath=<vault-role-path> \
  --set config.auth.multi.vault.roleId=<vault-role-id> \
  --set config.auth.multi.vault.roleSecret=<vault-role-secret>
```

For all values, Vault AppRole setup, and troubleshooting, see the [installation guide](https://arubacloud.github.io/arubacloud-resource-operator/installation) or the [Helm chart documentation](https://github.com/Arubacloud/helm-charts/tree/main/charts/arubacloud-resource-operator).

#### Verify Installation

Check if the operator is running:
```bash
kubectl get pods -n aruba-system
```

Check if CRDs are installed:
```bash
kubectl get crd | grep arubacloud.com
```

#### Uninstall

To uninstall the operator:
```bash
helm uninstall arubacloud-operator -n aruba-system
```

## Usage

Sample resource definitions can be found in the [config/samples](./config/samples) directory. These examples demonstrate how to create and manage Aruba Cloud resources through Kubernetes manifests.

To apply a sample:
```bash
kubectl apply -f config/samples/arubacloud.com_v1alpha1_cloudserver.yaml
```

## Documentation

Project documentation (EN/IT) is published via GitHub Pages and built from `docs/website/`.

- Local docs: `docs/README.md`

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

**NOTE:** Run `make help` for more information on all potential `make` targets.

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
