# Arubacloud Resource Operator

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest) [![Tests](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml) [![Release](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml)

> ** Development Status**: This operator is currently under active development and is **not production-ready yet**. APIs and resource schemas may change. Use at your own risk in production environments.

## Overview

The Arubacloud Resource Operator is a Kubernetes operator that enables declarative management of Aruba Cloud resources through Kubernetes Custom Resources. This operator allows you to provision and manage Aruba Cloud infrastructure using familiar Kubernetes tools and workflows.

## Installation

### Install via Helm Chart

The recommended way to install the Arubacloud Resource Operator is via Helm chart.

#### Prerequisites

- Kubernetes v1.11.3+ cluster
- Helm 3.x
- kubectl configured to communicate with your cluster

#### Install the Chart

Add the Arubacloud Helm repository (if not already added):
```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

Install the operator with automatic CRD installation (default):
```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace
```

Or install without CRDs (if managing them separately):
```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace \
  --set crds.enabled=false
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

## Development

### Prerequisites

- Go version v1.24.0+
- Docker version 17.03+
- kubectl version v1.11.3+
- Access to a Kubernetes v1.11.3+ cluster

### Building from Source

Build and push your image:
```sh
make docker-build docker-push IMG=<some-registry>/aruba:tag
```

**NOTE:** Ensure you have proper permissions to push to the registry.

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

**NOTE:** Run `make help` for more information on all potential `make` targets.

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
