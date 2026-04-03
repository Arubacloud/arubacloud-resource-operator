---
sidebar_position: 6
---

# SecurityRule

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `SecurityRule` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `securityrules.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `sr`, `arusr` |

## Descrizione

Una SecurityRule definisce una singola regola di traffico in ingresso o in uscita all'interno di un SecurityGroup. Le regole specificano il protocollo, l'intervallo di porte, la direzione e il target (un IP/CIDR o un altro SecurityGroup). I CloudServer che referenziano il SecurityGroup genitore sono governati da tutte le sue SecurityRule.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del SecurityGroup proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud. Deve corrispondere alla region del SecurityGroup genitore. |
| `protocol` | string | Sì | — | Enum: `TCP`, `UDP`, `ICMP`, `ALL` | Protocollo di rete a cui si applica questa regola |
| `port` | string | Sì | — | — | Porta o intervallo di porte: `"80"`, `"8080-8090"` o `"ALL"` |
| `direction` | string | Sì | — | Enum: `Ingress`, `Egress` | Se questa regola si applica al traffico in entrata o in uscita |
| `target.type` | string | Sì | — | Enum: `Ip`, `SecurityGroup` | Se il target è un IP/CIDR o un altro SecurityGroup |
| `target.value` | string | Sì | — | — | L'indirizzo IP, il blocco CIDR (es. `0.0.0.0/0`) o l'ID del SecurityGroup |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `securityGroupReference` | ResourceReference | Sì | — | — | Riferimento al [SecurityGroup](./SecurityGroup) proprietario |
| `vpcReference` | ResourceReference | Sì | — | — | Riferimento al [VPC](./VPC) proprietario |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID SecurityRule Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |
| `vpcID` | string | ID del VPC Aruba Cloud risolto |
| `securityGroupID` | string | ID del SecurityGroup Aruba Cloud risolto |

:::tip
SecurityRule espone colonne extra `kubectl get` per `Protocol` e `Direction`, rendendo facile vedere i dettagli della regola a colpo d'occhio:
```bash
kubectl get sr -n default
# NAME          PROTOCOL   DIRECTION   PHASE    AGE
# allow-ssh     TCP        Ingress     Active   5m
```
:::

## Riferimenti

**Di proprietà di:** [SecurityGroup](./SecurityGroup) (e [VPC](./VPC), [Project](./Project))

**Nessun figlio.**

## Ciclo di vita

- **Ordine di creazione**: Il SecurityGroup (e i suoi genitori VPC e Project) devono essere `Active`.
- **Comportamento all'eliminazione**: Le SecurityRule non hanno figli. L'eliminazione chiama l'API Aruba Cloud e rimuove il finalizer.
- **Comportamento all'aggiornamento**: `tags` può essere aggiornato. Tutti gli altri campi (protocol, port, direction, target) sono effettivamente immutabili — ricrea la regola per modificarli.

## Esempi

### Regola Ingress: consenti SSH da qualsiasi parte

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: allow-ssh-ingress
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

### Regola Egress: consenti tutto il traffico in uscita

```yaml
apiVersion: arubacloud.com/v1alpha1
kind: SecurityRule
metadata:
  name: allow-all-egress
  namespace: default
spec:
  tenant: ARU-123456
  region: ITBG-Bergamo
  protocol: ALL
  port: "ALL"
  direction: Egress
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

## Riferimento rapido kubectl

```bash
# Elenca tutte le security rule con le colonne protocol e direction
kubectl get sr -n default

# Descrivi una security rule
kubectl describe arusr allow-ssh-ingress -n default
```
