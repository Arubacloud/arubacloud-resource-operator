---
sidebar_position: 8
---

# ElasticIP

| Proprietà | Valore |
|-----------|--------|
| **Kind** | `ElasticIP` |
| **API Group/Version** | `arubacloud.com/v1alpha1` |
| **Nome CRD** | `elasticips.arubacloud.com` |
| **Scope** | Namespaced |
| **Nomi brevi** | `eip`, `arueip` |

## Descrizione

Un ElasticIP è un indirizzo IP pubblico statico che può essere assegnato a un CloudServer. A differenza degli IP effimeri che Aruba Cloud può assegnare di default, un ElasticIP persiste indipendentemente da qualsiasi server — puoi riservarlo, collegarlo a un server e in seguito riassegnarlo a un server diverso senza perdere l'indirizzo IP. Gli ElasticIP sono opzionali: un CloudServer può essere creato senza uno.

## Campi Spec

| Campo | Tipo | Obbligatorio | Predefinito | Validazione | Descrizione |
|-------|------|-------------|------------|-------------|-------------|
| `tenant` | string | No | — | — | Identificatore del tenant Aruba Cloud. Deve corrispondere al tenant del Project proprietario. |
| `region` | string | Sì | `ITBG-Bergamo` | — | Regione Aruba Cloud in cui viene riservato l'IP |
| `billingPeriod` | string | Sì | `Hour` | Enum: `Hour`, `Month` | Ciclo di fatturazione per l'indirizzo IP riservato. `Month` è tipicamente più conveniente per carichi di lavoro longevi. |
| `tags` | []string | No | — | — | Etichette propagate alla risorsa Aruba Cloud |
| `projectReference` | ResourceReference | Sì | — | — | Riferimento al [Project](./Project) proprietario |

:::note
Il `billingPeriod` influisce sul costo dal momento in cui viene creato l'ElasticIP, indipendentemente dal fatto che sia collegato a un CloudServer. Riserva gli ElasticIP solo quando necessario.
:::

## Campi Status

| Campo | Tipo | Descrizione |
|-------|------|-------------|
| `phase` | string | Fase corrente del ciclo di vita |
| `resourceID` | string | ID ElasticIP Aruba Cloud, impostato dopo la creazione |
| `message` | string | Messaggio di stato leggibile |
| `observedGeneration` | int64 | Ultimo `.metadata.generation` elaborato |
| `phaseStartTime` | timestamp | Quando è iniziata la fase corrente |
| `conditions` | []Condition | Condizioni Kubernetes standard |
| `projectID` | string | ID del progetto Aruba Cloud risolto |

## Riferimenti

**Di proprietà di:** [Project](./Project)

**Referenziato da:**
- [CloudServer](./CloudServer) — tramite `spec.elasticIPReference` (opzionale)

## Ciclo di vita

- **Ordine di creazione**: Il Project deve essere `Active`. Un ElasticIP può essere creato prima di un CloudServer e collegato in seguito.
- **Comportamento all'eliminazione**: Gli ElasticIP non hanno figli. Elimina prima il CloudServer che referenzia questo ElasticIP (o rimuovi il riferimento) prima di eliminare l'ElasticIP.
- **Comportamento all'aggiornamento**: `tags` e `billingPeriod` possono potenzialmente essere aggiornati. L'indirizzo IP stesso viene assegnato da Aruba Cloud e non può essere scelto.

## Esempio

```yaml
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
  tags:
    - production
    - web
```

## Riferimento rapido kubectl

```bash
# Elenca tutti gli Elastic IP
kubectl get eip -n default

# Descrivi un Elastic IP e visualizza l'indirizzo assegnato (in resourceID)
kubectl describe arueip web-server-ip -n default
```
