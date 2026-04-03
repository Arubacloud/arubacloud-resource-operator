---
sidebar_position: 11
---

# Examples

This page provides practical examples for working with the Aruba Cloud Resource Operator. Start with the [end-to-end tutorial](#end-to-end-deploy-a-cloud-server) for a complete walkthrough, or jump to a [standalone recipe](#standalone-recipes) for a specific scenario.

---

## End-to-End: Deploy a Cloud Server

This tutorial provisions a complete stack: a Project, network resources, storage, an SSH key, and finally a CloudServer with a static public IP.

### Prerequisites

- The Aruba Cloud Resource Operator is installed (see [Installation](./installation))
- You have a valid Aruba Cloud tenant identifier (e.g., `ARU-123456`)
- `kubectl` is configured to access your Kubernetes cluster

Replace `ARU-123456` with your actual tenant in all examples below.

---

### Step 1: Create the Project

The Project is the root resource. All other resources will reference it.

```yaml
# project.yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: my-project
  namespace: default
spec:
  tenant: ARU-123456
  description: "My web infrastructure"
```

```bash
kubectl apply -f project.yaml

# Wait until Active
kubectl wait project my-project --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Step 2: Create the VPC

```yaml
# vpc.yaml
apiVersion: arubacloud.com/v1alpha1
kind: VPC
metadata:
  name: web-vpc
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  projectReference:
    name: my-project
    namespace: default
```

```bash
kubectl apply -f vpc.yaml
kubectl wait vpc web-vpc --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Step 3: Create network, storage, and access resources

These can all be applied at the same time; the operator will wait for each parent to be ready.

```yaml
# subnet.yaml
apiVersion: arubacloud.com/v1alpha1
kind: Subnet
metadata:
  name: web-subnet
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  type: Advanced
  cidr: "10.0.1.0/24"
  dhcp:
    enabled: true
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

```yaml
# security-group.yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityGroup
metadata:
  name: web-sg
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

```yaml
# keypair.yaml
apiVersion: arubacloud.com/v1alpha1
kind: KeyPair
metadata:
  name: my-ssh-key
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  value: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyReplaceWithYourPublicKey user@host"
  projectReference:
    name: my-project
    namespace: default
```

```yaml
# boot-volume.yaml
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: web-server-boot
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 40
  billingPeriod: Hour
  type: Performance
  bootable: true
  image: "LU20-001"
  projectReference:
    name: my-project
    namespace: default
```

```bash
kubectl apply -f subnet.yaml -f security-group.yaml -f keypair.yaml -f boot-volume.yaml

# Wait for all of them
kubectl wait subnet web-subnet --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait securitygroup web-sg --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait keypair my-ssh-key --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait blockstorage web-server-boot --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Step 4: Create a SecurityRule to allow SSH

```yaml
# security-rule.yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: allow-ssh
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  protocol: TCP
  port: "22"
  direction: Ingress
  target:
    type: Ip
    value: "0.0.0.0/0"
  securityGroupReference:
    name: web-sg
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  projectReference:
    name: my-project
    namespace: default
```

```bash
kubectl apply -f security-rule.yaml
kubectl wait securityrule allow-ssh --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Step 5: Reserve an ElasticIP

```yaml
# elastic-ip.yaml
apiVersion: arubacloud.com/v1alpha1
kind: ElasticIP
metadata:
  name: web-server-ip
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  billingPeriod: Hour
  projectReference:
    name: my-project
    namespace: default
```

```bash
kubectl apply -f elastic-ip.yaml
kubectl wait elasticip web-server-ip --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Step 6: Create the CloudServer

```yaml
# cloud-server.yaml
apiVersion: arubacloud.com/v1alpha1
kind: CloudServer
metadata:
  name: web-server
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  flavorName: "CSO4A8"
  projectReference:
    name: my-project
    namespace: default
  vpcReference:
    name: web-vpc
    namespace: default
  subnetReferences:
    - name: web-subnet
      namespace: default
  securityGroupReferences:
    - name: web-sg
      namespace: default
  keyPairReference:
    name: my-ssh-key
    namespace: default
  bootVolumeReference:
    name: web-server-boot
    namespace: default
  elasticIPReference:
    name: web-server-ip
    namespace: default
```

```bash
kubectl apply -f cloud-server.yaml
kubectl wait cloudserver web-server --for=jsonpath='{.status.phase}'=Active --timeout=300s
```

---

### Step 7: Verify

```bash
# View the server and its assigned resource ID
kubectl get cs web-server -n default

# View full status including all resolved IDs
kubectl describe cs web-server -n default

# Get the ElasticIP address (stored in status.resourceID of the ElasticIP object)
kubectl get eip web-server-ip -n default -o jsonpath='{.status.resourceID}'
```

---

### Step 8: Cleanup

Delete resources in reverse order, or simply delete the Project to cascade-delete everything:

```bash
# Option A: delete everything by deleting the Project
kubectl delete project my-project -n default

# Option B: manual reverse order
kubectl delete cloudserver web-server -n default
kubectl delete elasticip web-server-ip -n default
kubectl delete securityrule allow-ssh -n default
kubectl delete keypair my-ssh-key -n default
kubectl delete blockstorage web-server-boot -n default
kubectl delete securitygroup web-sg -n default
kubectl delete subnet web-subnet -n default
kubectl delete vpc web-vpc -n default
kubectl delete project my-project -n default
```

---

## Standalone Recipes

### Storage Only: Project + BlockStorage (data volume)

Provision a standalone data volume without a CloudServer. Useful for pre-creating volumes before attaching them to servers.

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: storage-project
  namespace: default
spec:
  tenant: ARU-123456
---
apiVersion: arubacloud.com/v1alpha1
kind: BlockStorage
metadata:
  name: data-volume
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  zone: ITBG-1
  sizeGB: 100
  billingPeriod: Month
  type: Standard
  bootable: false
  projectReference:
    name: storage-project
    namespace: default
```

```bash
kubectl apply -f storage.yaml
kubectl wait blockstorage data-volume --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Network Only: Project + VPC + Subnet + SecurityGroup + SecurityRule

Set up a complete network stack without a server.

```bash
# Apply in order (or let the operator handle the sequencing automatically)
kubectl apply -f project.yaml
kubectl apply -f vpc.yaml
kubectl apply -f subnet.yaml -f security-group.yaml
kubectl apply -f security-rule.yaml
```

All resources can be applied at once — the operator resolves the dependency order automatically.

```bash
kubectl apply -f project.yaml -f vpc.yaml -f subnet.yaml -f security-group.yaml -f security-rule.yaml
```

---

### SSH Key Setup: Project + KeyPair

Register an SSH key ready to be referenced by future CloudServers.

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: infra-project
  namespace: default
spec:
  tenant: ARU-123456
---
apiVersion: arubacloud.com/v1alpha1
kind: KeyPair
metadata:
  name: team-ssh-key
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  value: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExampleKeyReplaceWithYourPublicKey team@company.com"
  projectReference:
    name: infra-project
    namespace: default
```

```bash
kubectl apply -f keypair-setup.yaml
kubectl wait keypair team-ssh-key --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

## Sample Manifests

The repository ships with sample manifests under `config/samples/`. These use placeholder variables that must be substituted before applying:

| Placeholder | Description |
|-------------|-------------|
| `__TENANT__` | Your Aruba Cloud tenant ID (e.g., `ARU-123456`) |
| `__NAME__` | A name for the resource |
| `__NAMESPACE__` | The Kubernetes namespace |

The `test/scripts/test_runner.sh` script in the repository substitutes these placeholders automatically:

```bash
cd test/scripts/
NN=1 QNT=00 ACTION=apply TENANT=ARU-123456 NAME=my-resource ./test_runner.sh
```

---

## Common Operations

```bash
# List all operator-managed resources in a namespace
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver -n default

# Check the phase of all resources at once
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver \
  -n default -o custom-columns=KIND:.kind,NAME:.metadata.name,PHASE:.status.phase

# Find all resources for a specific tenant
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver \
  -n default -o json | jq '.items[] | select(.spec.tenant=="ARU-123456") | {kind:.kind, name:.metadata.name, phase:.status.phase}'

# Watch a resource's phase until it reaches Active
kubectl get cloudserver web-server -n default -w

# View the full conditions (useful when debugging a Failed resource)
kubectl get cloudserver web-server -n default -o jsonpath='{.status.conditions}' | jq .
```
