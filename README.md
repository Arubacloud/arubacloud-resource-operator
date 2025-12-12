# Arubacloud Resource Operator

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest) [![Tests](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml) [![Release](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml)

> ** Development Status**: This operator is currently under active development and is **not production-ready yet**. APIs and resource schemas may change. Use at your own risk in production environments.

## Overview

The Arubacloud Resource Operator is a Kubernetes operator that enables declarative management of Aruba Cloud resources through Kubernetes Custom Resources. This operator allows you to provision and manage Aruba Cloud infrastructure using familiar Kubernetes tools and workflows.

## Installation

### Install the Chart

Add the arubacloud Helm repository (if not already added):
```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

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

For multi-tenant deployments using HashiCorp Vault:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=multi \
  --set config.auth.multi.vault.address=<vault-address> \
  --set config.auth.multi.vault.rolePath=<vault-role-path> \
  --set config.auth.multi.vault.roleId=<vault-role-id> \
  --set config.auth.multi.vault.roleSecret=<vault-role-secret> \ 
  --set config.auth.multi.vault.kvMount=<vault-role-kvMount>
```

For detailed configuration options, values, and advanced usage, please refer to the [Helm chart documentation](https://github.com/Arubacloud/helm-charts/tree/main/charts/arubacloud-resource-operator).

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

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

**NOTE:** Run `make help` for more information on all potential `make` targets.

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
