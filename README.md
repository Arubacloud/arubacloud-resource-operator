# Arubacloud Resource Operator

[![GitHub release](https://img.shields.io/github/tag/arubacloud/arubacloud-resource-operator.svg?label=release)](https://github.com/arubacloud/arubacloud-resource-operator/releases/latest) [![Tests](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/test.yml) [![Release](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml/badge.svg)](https://github.com/arubacloud/arubacloud-resource-operator/actions/workflows/release.yml)

> **⚠️ Development Status**: This operator is currently under active development and is **not production-ready yet**. APIs and resource schemas may change. Use at your own risk in production environments.

## Overview

The Arubacloud Resource Operator is a Kubernetes operator that enables declarative management of Aruba Cloud infrastructure resources using Kubernetes Custom Resources. Built with the [Operator SDK](https://sdk.operatorframework.io/), it allows you to provision and manage cloud resources such as virtual machines, networks, storage, and security configurations directly from your Kubernetes cluster.

### Managed Resources

**Infrastructure Resources:**

- **CloudServer** - Virtual machine instances
- **BlockStorage** - Persistent block storage volumes
- **ElasticIP** - Static public IP addresses
- **KeyPair** - SSH key pairs for server access
- **Project** - Aruba Cloud project management

**Network Resources:**

- **VPC** - Virtual Private Cloud networks
- **Subnet** - Network subnets within VPCs
- **SecurityGroup** - Network security groups
- **SecurityRule** - Firewall rules for security groups

## Prerequisites

- **Go** version v1.24.0+
- **Docker** version 17.03+
- **kubectl** version v1.11.3+
- **Kubernetes cluster** v1.11.3+
- **Aruba Cloud account** with API access
- **HashiCorp Vault** with AppRole authentication enabled and a KV secrets engine containing:
  - Entries keyed by Aruba Tenant ID (e.g., `ARU-329997`)
  - Each entry must contain:
    - `client-id` - Aruba CMP API client ID
    - `client-secret` - Aruba CMP API client secret

## Local Development

### Setup Development Environment

1. **Clone the repository:**

```bash
git clone https://github.com/Arubacloud/arubacloud-resource-operator.git
cd arubacloud-resource-operator
```

2. **Install development dependencies:**

```bash
# Install CRDs into the cluster
make install

# Generate code (DeepCopy, mocks, etc.)
make generate

# Generate manifests (CRDs, RBAC, etc.)
make manifests
```

3. **Configure credentials:**

Create configuration files in `config/manager/`:

```bash
# config/manager/config.env
API_GATEWAY=https://api.arubacloud.com
KEYCLOAK_URL=https://login.aruba.it/auth
REALM_API=cmp-new-apikey
VAULT_ADDRESS=http://vault:8200
ROLE_PATH=approle
KV_MOUNT=kv

# config/manager/secrets.env
ROLE_ID=your-vault-role-id
ROLE_SECRET=your-vault-secret
```

### Build and Run Locally

**Run the operator locally (outside cluster):**

```bash
make run
```

**Build the binary:**

```bash
make build
```

**Run tests:**

```bash
# Unit tests
make test

# End-to-end tests
make test-e2e
```

### Code Quality

**Format code:**

```bash
make fmt
```

**Run linter:**

```bash
make lint

# Fix linting issues automatically
make lint-fix
```

**Verify linter configuration:**

```bash
make lint-config
```

### Generate Mocks

Regenerate mocks for testing:

```bash
make generate
```

This will update mocks in `internal/mocks/` using mockery.

## Testing

The project includes comprehensive testing infrastructure for both unit and end-to-end tests.

### Unit Tests

Run unit tests with coverage:

```bash
make test
```

This executes all unit tests using the `setup-envtest` tool to provide a test control plane.

### End-to-End Tests

E2E tests run against a real Kubernetes cluster using Kind.

**Setup and run E2E tests:**

```bash
# Creates Kind cluster, builds image, and runs E2E tests
make test-e2e
```

**Cleanup test environment:**

```bash
make cleanup-test-e2e
```

### Test Runner for Fixtures

The project includes a test runner script for testing manifests with customizable placeholders.

**Location:** `test/scripts/test_runner.sh`

**Usage:**

```bash
# Apply test fixtures
NN=10 TENANT=ARU-329997 NAME=aruba-resource ACTION=apply ./test/scripts/test_runner.sh

# Delete test fixtures
NN=10 TENANT=ARU-329997 NAME=aruba-resource ACTION=delete ./test/scripts/test_runner.sh
```

**Environment Variables:**

- `NN` - Test set number (corresponds to `fixtures/TestNN`)
- `TENANT` - Replaces `__TENANT__` placeholder in manifests
- `NAME` - Replaces `__NAME__` placeholder in manifests
- `ACTION` - kubectl action (`apply`, `delete`, etc.)

**Test Fixtures:**

Test manifests are listed in `test/scripts/fixtures/TestNN` files and reference samples from `config/samples/`.

For more details, see [test/scripts/README.md](test/scripts/README.md).

## Deployment

### Option 1: Deploy with Helm (Recommended)

Helm charts provide the easiest way to deploy the operator with proper configuration management.

#### Install from Helm Repository

```bash
# Add Aruba Cloud Helm repository
helm repo add arubacloud https://arubacloud.github.io/helm-charts
helm repo update

# Option 1: Install operator with automatic CRD installation (default)
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace

# Option 2: Install CRDs and operator separately
helm install arubacloud-operator-crd arubacloud/arubacloud-resource-operator-crd

helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set crds.enabled=false

# Option 3: Install with custom image
helm install arubacloud-operator arubacloud/arubacloud-resource-operator \
  --namespace aruba-system \
  --create-namespace \
  --set image.repository=ghcr.io/arubacloud/arubacloud-resource-operator \
  --set image.tag=latest
```

> **Note:** For detailed configuration options, values, and advanced usage, refer to the [Helm chart documentation](https://github.com/Arubacloud/helm-charts/tree/main/charts/arubacloud-resource-operator).

#### Install from Local Charts

```bash
# Generate Helm charts from manifests
make helm-crd helm-operator

# Install CRDs
helm install arubacloud-crd config/charts/arubacloud-resource-operator-crd/

# Install operator
helm install arubacloud-operator config/charts/arubacloud-resource-operator/ \
  --namespace aruba-system \
  --create-namespace
```

#### Configure the Operator

After installation, configure credentials:

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
  --from-literal=vault-address=http://vault:8200 \
  --from-literal=role-path=approle \
  --from-literal=kv-mount=kv \
  --namespace aruba-system
```

#### Verify Installation

```bash
# Check if the operator is running
kubectl get pods -n aruba-system

# Check if CRDs are installed
kubectl get crd | grep arubacloud.com

# View operator logs
kubectl logs -n aruba-system -l control-plane=controller-manager -f

# Test with sample resources
kubectl apply -f config/samples/arubacloud.com_v1alpha1_vpc.yaml
```

#### Uninstall

```bash
# Remove sample resources
kubectl delete -k config/samples/

# Uninstall operator
helm uninstall arubacloud-operator -n aruba-system

# Uninstall CRDs (⚠️ this will delete all custom resources)
helm uninstall arubacloud-crd
```

### Option 2: Deploy with Kustomize

Use Kustomize for more control over manifest customization.

**Build and push your image:**

```bash
make docker-build docker-push IMG=<your-registry>/arubacloud-operator:tag
```

**Deploy to cluster:**

```bash
# Install CRDs
make install

# Deploy operator
make deploy IMG=<your-registry>/arubacloud-operator:tag
```

**Create sample resources:**

```bash
kubectl apply -k config/samples/
```

**Uninstall:**

```bash
# Remove samples
kubectl delete -k config/samples/

# Remove operator
make undeploy

# Remove CRDs
make uninstall
```

## Usage Examples

### Create a VPC

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Vpc
metadata:
  name: my-vpc
  namespace: default
spec:
  tenant: my-tenant
  tags:
    - production
    - network
  location:
    value: ITBG-Bergamo
  projectReference:
    name: my-project
    namespace: default
```

### Create a Subnet

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Subnet
metadata:
  name: my-subnet
  namespace: default
spec:
  tenant: my-tenant
  tags:
    - public
  type: Advanced
  default: false
  network:
    address: 192.168.1.0/25
  dhcp:
    enabled: true
  vpcReference:
    name: my-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

### Create a CloudServer

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: web-server
  namespace: default
spec:
  tenant: my-tenant
  tags:
    - webserver
  location:
    value: ITBG-Bergamo
  dataCenter: ITBG-1
  flavorName: CSO4A8
  vpcReference:
    name: my-vpc
    namespace: default
  subnetReferences:
    - name: my-subnet
      namespace: default
  keyPairReference:
    name: my-keypair
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

More examples are available in [config/samples/](config/samples/).

### Apply Sample Resources

To create resources using the provided samples:

```bash
# Apply a single resource
kubectl apply -f config/samples/arubacloud.com_v1alpha1_vpc.yaml

# Apply all samples
kubectl apply -k config/samples/

# Check resource status
kubectl get vpc,subnet,cloudserver -A

# Describe a specific resource
kubectl describe cloudserver web-server

# Delete samples
kubectl delete -k config/samples/
```

## Project Structure

```
arubacloud-resource-operator/
├── api/v1alpha1/              # API definitions for CRDs
├── cmd/                       # Main application entry point
├── config/                    # Kubernetes manifests
│   ├── charts/               # Helm charts
│   │   ├── arubacloud-resource-operator/
│   │   └── arubacloud-resource-operator-crd/
│   ├── crd/                  # CRD base manifests
│   ├── default/              # Kustomize default overlay
│   ├── manager/              # Operator deployment config
│   ├── rbac/                 # RBAC manifests
│   └── samples/              # Example resource manifests
├── internal/                  # Internal packages
│   ├── client/               # Aruba Cloud API clients
│   ├── controller/           # Kubernetes controllers
│   ├── reconciler/           # Reconciliation logic
│   ├── config/               # Configuration management
│   ├── mocks/                # Generated mocks for testing
│   └── util/                 # Utility functions
├── test/                      # Test infrastructure
│   ├── e2e/                  # End-to-end tests
│   ├── scripts/              # Test runner scripts
│   └── utils/                # Test utilities
├── Makefile                   # Build and deployment automation
└── README.md                  # This file
```

## Makefile Targets

### Development

- `make help` - Display available targets
- `make manifests` - Generate manifests (CRDs, RBAC, etc.)
- `make generate` - Generate code (DeepCopy, mocks)
- `make fmt` - Format code with gofmt
- `make vet` - Run go vet
- `make lint` - Run golangci-lint
- `make lint-fix` - Fix linting issues

### Testing

- `make test` - Run unit tests
- `make test-e2e` - Run end-to-end tests
- `make setup-test-e2e` - Setup Kind cluster for E2E
- `make cleanup-test-e2e` - Cleanup test environment

### Build

- `make build` - Build manager binary
- `make run` - Run controller locally
- `make docker-build` - Build Docker image
- `make docker-push` - Push Docker image

### Deployment

- `make install` - Install CRDs to cluster
- `make uninstall` - Uninstall CRDs from cluster
- `make deploy` - Deploy operator to cluster
- `make undeploy` - Remove operator from cluster

### Helm

- `make helm-crd` - Generate CRD Helm chart
- `make helm-operator` - Generate operator Helm chart

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

**NOTE:** Run `make help` for more information on all potential `make` targets.

More information can be found via the [Kubebuilder Documentation](https://book.kubebuilder.io/introduction.html)

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.
