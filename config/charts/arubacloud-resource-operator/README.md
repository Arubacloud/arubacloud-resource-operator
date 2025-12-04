# Arubacloud Resource Operator

> Kubernetes operator for managing Aruba Cloud resources through CRDs.

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest)

⚠️ **Development Status**: Not production-ready yet. APIs may change.

## Installation

```bash
# Add Helm repository
helm repo add arubacloud https://arubacloud.github.io/helm-charts

# Install CRDs first
helm install arubacloud-operator-crd arubacloud/arubacloud-resource-operator-crd

# Install operator
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace
```

## Quick Start

Configure operator secrets:

```bash
# Create secret with Vault AppRole credentials
kubectl create secret generic controller-manager \
  --from-literal=role-id=YOUR_ROLE_ID \
  --from-literal=role-secret=YOUR_ROLE_SECRET \
  --namespace aruba-system

# Create configmap with API endpoints
kubectl create configmap controller-manager \
  --from-literal=api-gateway=https://api.arubacloud.com \
  --from-literal=keycloak-url=https://login.aruba.it/auth \
  --from-literal=realm-api=cmp-new-apikey \
  --from-literal=vault-address=http://vault0.default.svc.cluster.local:8200 \
  --from-literal=role-path=approle \
  --from-literal=kv-mount=kw \
  --namespace aruba-system
```

Create a VPC:

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Vpc
metadata:
  name: my-vpc
  namespace: default
spec:
  tenant: my-tenant
  location:
    value: ITBG-Bergamo
  projectReference:
    name: my-project
    namespace: default
```

## Available Resources

- **Infrastructure**: BlockStorage, CloudServer, ElasticIP, KeyPair, Project
- **Network**: VPC, Subnet, SecurityGroup, SecurityRule

## Documentation

Full documentation and examples: [GitHub Repository](https://github.com/Arubacloud/arubacloud-resource-operator)

## License

Copyright 2024 Aruba S.p.A. - Licensed under Apache License 2.0
