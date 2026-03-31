---
sidebar_position: 2
---

## Esempi

I manifest di esempio si trovano in `config/samples/`.

### Workflow rapido

Applica un esempio:

```bash
kubectl apply -f config/samples/arubacloud.com_v1alpha1_project.yaml
```

Poi crea le risorse dipendenti (esempi):

- `config/samples/arubacloud.com_v1alpha1_vpc.yaml`
- `config/samples/arubacloud.com_v1alpha1_subnet.yaml`
- `config/samples/arubacloud.com_v1alpha1_securitygroup.yaml`
- `config/samples/arubacloud.com_v1alpha1_securityrule.yaml`
- `config/samples/arubacloud.com_v1alpha1_keypair.yaml`
- `config/samples/arubacloud.com_v1alpha1_blockstorage.yaml`
- `config/samples/arubacloud.com_v1alpha1_elasticip.yaml`
- `config/samples/arubacloud.com_v1alpha1_cloudserver.yaml`

### Placeholder

Molti esempi usano placeholder come `__NAME__`, `__NAMESPACE__`, `__TENANT__`. Sostituiscili prima di applicare.

