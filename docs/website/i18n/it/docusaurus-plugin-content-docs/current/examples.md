---
sidebar_position: 11
---

# Esempi

Questa pagina fornisce esempi pratici per lavorare con Aruba Cloud Resource Operator. Inizia con il [tutorial end-to-end](#end-to-end-distribuire-un-cloud-server) per una guida completa, oppure vai direttamente a una [ricetta standalone](#ricette-standalone) per uno scenario specifico.

---

## End-to-end: Distribuire un Cloud Server

Questo tutorial provisionerebbe uno stack completo: un Project, risorse di rete, storage, una chiave SSH e infine un CloudServer con un IP pubblico statico.

### Prerequisiti

- Aruba Cloud Resource Operator è installato (vedi [Installazione](./installation))
- Hai un identificatore tenant Aruba Cloud valido (es. `ARU-123456`)
- `kubectl` è configurato per accedere al tuo cluster Kubernetes

Sostituisci `ARU-123456` con il tuo tenant effettivo in tutti gli esempi seguenti.

---

### Passo 1: Crea il Project

Il Project è la risorsa radice. Tutte le altre risorse lo referenzieranno.

```yaml
# project.yaml
apiVersion: arubacloud.com/v1alpha1
kind: Project
metadata:
  name: my-project
  namespace: default
spec:
  tenant: ARU-123456
  description: "La mia infrastruttura web"
```

```bash
kubectl apply -f project.yaml

# Attendi che sia Active
kubectl wait project my-project --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Passo 2: Crea il VPC

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

### Passo 3: Crea risorse di rete, storage e accesso

Queste possono essere applicate tutte contemporaneamente; l'operatore attenderà che ogni genitore sia pronto.

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

# Attendi tutti
kubectl wait subnet web-subnet --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait securitygroup web-sg --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait keypair my-ssh-key --for=jsonpath='{.status.phase}'=Active --timeout=120s
kubectl wait blockstorage web-server-boot --for=jsonpath='{.status.phase}'=Active --timeout=120s
```

---

### Passo 4: Crea una SecurityRule per consentire SSH

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

### Passo 5: Riserva un ElasticIP

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

### Passo 6: Crea il CloudServer

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

### Passo 7: Verifica

```bash
# Visualizza il server e il suo ID risorsa assegnato
kubectl get cs web-server -n default

# Visualizza lo status completo inclusi tutti gli ID risolti
kubectl describe cs web-server -n default

# Ottieni l'indirizzo ElasticIP (memorizzato in status.resourceID dell'oggetto ElasticIP)
kubectl get eip web-server-ip -n default -o jsonpath='{.status.resourceID}'
```

---

### Passo 8: Pulizia

Elimina le risorse in ordine inverso, oppure elimina semplicemente il Project per eliminare a cascata tutto:

```bash
# Opzione A: elimina tutto eliminando il Project
kubectl delete project my-project -n default

# Opzione B: ordine inverso manuale
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

## Ricette standalone

### Solo storage: Project + BlockStorage (volume dati)

Provisiona un volume dati standalone senza un CloudServer. Utile per pre-creare volumi prima di collegarli ai server.

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

### Solo rete: Project + VPC + Subnet + SecurityGroup + SecurityRule

Configura uno stack di rete completo senza un server.

```bash
# Puoi applicare tutto in una volta — l'operatore risolve automaticamente l'ordine delle dipendenze
kubectl apply -f project.yaml -f vpc.yaml -f subnet.yaml -f security-group.yaml -f security-rule.yaml
```

---

### Configurazione chiave SSH: Project + KeyPair

Registra una chiave SSH pronta per essere referenziata da futuri CloudServer.

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

## Manifest di esempio

Il repository include manifest di esempio in `config/samples/`. Questi usano variabili placeholder che devono essere sostituite prima dell'applicazione:

| Placeholder | Descrizione |
|-------------|-------------|
| `__TENANT__` | Il tuo ID tenant Aruba Cloud (es. `ARU-123456`) |
| `__NAME__` | Un nome per la risorsa |
| `__NAMESPACE__` | Il namespace Kubernetes |

Lo script `test/scripts/test_runner.sh` nel repository sostituisce automaticamente questi placeholder:

```bash
cd test/scripts/
NN=1 QNT=00 ACTION=apply TENANT=ARU-123456 NAME=my-resource ./test_runner.sh
```

---

## Operazioni comuni

```bash
# Elenca tutte le risorse gestite dall'operatore in un namespace
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver -n default

# Controlla la fase di tutte le risorse contemporaneamente
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver \
  -n default -o custom-columns=KIND:.kind,NAME:.metadata.name,PHASE:.status.phase

# Trova tutte le risorse per un tenant specifico
kubectl get project,vpc,subnet,securitygroup,securityrule,keypair,elasticip,blockstorage,cloudserver \
  -n default -o json | jq '.items[] | select(.spec.tenant=="ARU-123456") | {kind:.kind, name:.metadata.name, phase:.status.phase}'

# Osserva la fase di una risorsa finché non raggiunge Active
kubectl get cloudserver web-server -n default -w

# Visualizza le condizioni complete (utile per il debug di una risorsa in Failed)
kubectl get cloudserver web-server -n default -o jsonpath='{.status.conditions}' | jq .
```
