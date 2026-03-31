---
sidebar_position: 2
---

## Examples

Sample manifests live under `config/samples/`.

### Quick workflow

Apply a sample:

```bash
kubectl apply -f config/samples/arubacloud.com_v1alpha1_project.yaml
```

Then create dependent resources (examples):

- `config/samples/arubacloud.com_v1alpha1_vpc.yaml`
- `config/samples/arubacloud.com_v1alpha1_subnet.yaml`
- `config/samples/arubacloud.com_v1alpha1_securitygroup.yaml`
- `config/samples/arubacloud.com_v1alpha1_securityrule.yaml`
- `config/samples/arubacloud.com_v1alpha1_keypair.yaml`
- `config/samples/arubacloud.com_v1alpha1_blockstorage.yaml`
- `config/samples/arubacloud.com_v1alpha1_elasticip.yaml`
- `config/samples/arubacloud.com_v1alpha1_cloudserver.yaml`

### Placeholders

Many samples use placeholders like `__NAME__`, `__NAMESPACE__`, `__TENANT__`. Replace them before applying.

