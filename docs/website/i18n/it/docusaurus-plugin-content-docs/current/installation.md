---
sidebar_position: 2
---

## Installazione

### Installazione con Helm

Aggiungi il repository Helm di Aruba Cloud:

```bash
helm repo add arubacloud https://arubacloud.github.io/helm-charts/
helm repo update
```

Installa la chart dell’operatore (esempio single-tenant):

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set config.auth.mode=single \
  --set config.auth.single.clientId=<your-client-id> \
  --set config.auth.single.clientSecret=<your-client-secret>
```

### Verifica

```bash
kubectl get pods -n aruba-system
kubectl get crd | grep arubacloud.com
```

