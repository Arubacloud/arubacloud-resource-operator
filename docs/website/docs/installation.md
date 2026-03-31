---
sidebar_position: 2
---

## Installation

### Install with Helm

Add the Aruba Cloud Helm repository:

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

Install the operator chart (single-tenant example):

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

### Verify

```bash
kubectl get pods -n aruba-system
kubectl get crd | grep arubacloud.com
```

