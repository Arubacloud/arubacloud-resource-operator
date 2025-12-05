# Arubacloud Resource Operator CRDs

> Custom Resource Definitions for the Arubacloud Resource Operator.

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest)

⚠️ **Development Status**: Not production-ready yet. APIs may change.

## Installation

```bash
# Add Helm repository
helm repo add arubacloud https://arubacloud.github.io/helm-charts

# Install CRDs
helm install arubacloud-operator-crd arubacloud/arubacloud-resource-operator-crd
```

## Installed CRDs

### Infrastructure Resources

- `blockstorages.arubacloud.com` - Persistent block storage volumes (Kind: BlockStorage)
- `cloudservers.arubacloud.com` - Virtual machine instances (Kind: CloudServer)
- `elasticips.arubacloud.com` - Static public IP addresses (Kind: ElasticIP)
- `keypairs.arubacloud.com` - SSH key pairs (Kind: KeyPair)
- `projects.arubacloud.com` - Aruba Cloud projects (Kind: Project)

### Network Resources

- `vpcs.arubacloud.com` - Virtual Private Cloud networks (Kind: Vpc)
- `subnets.arubacloud.com` - Network subnets (Kind: Subnet)
- `securitygroups.arubacloud.com` - Network security groups (Kind: SecurityGroup)
- `securityrules.arubacloud.com` - Security group rules (Kind: SecurityRule)

## Verification

```bash
kubectl get crds | grep arubacloud.com
```

## Next Steps

Install the operator controller:

```bash
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system --create-namespace
```

## Uninstalling

```bash
helm uninstall arubacloud-operator-crd
```

⚠️ **Warning**: Uninstalling will delete all associated custom resources. Backup first!

## Documentation

Full documentation and examples: [GitHub Repository](https://github.com/Arubacloud/arubacloud-resource-operator)

## License

Copyright 2024 Aruba S.p.A. - Licensed under Apache License 2.0
