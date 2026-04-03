---
sidebar_position: 4
---

# Introduzione alle CRD

L'operatore espone le risorse Aruba Cloud come Custom Resources Kubernetes sotto:

- **API group**: `arubacloud.com`
- **Versione**: `v1alpha1`
- **Scope**: Namespaced (tutte le risorse)

Per una panoramica visiva di come le risorse si relazionano tra loro, consulta il [diagramma di gerarchia delle risorse](./architecture#gerarchia-delle-risorse) nella pagina Architettura.

## Risorse disponibili

| Risorsa | Descrizione |
|---------|-------------|
| [Project](./Project) | Risorsa radice. Rappresenta un progetto/account Aruba Cloud. Tutte le altre risorse appartengono a un Project. |
| [VPC](./VPC) | Virtual Private Cloud. Contenitore di rete per Subnet, SecurityGroup e CloudServer. |
| [Subnet](./Subnet) | Una subnet all'interno di un VPC. I CloudServer si connettono a una o più Subnet. |
| [SecurityGroup](./SecurityGroup) | Un gruppo firewall all'interno di un VPC. Contiene SecurityRule. |
| [SecurityRule](./SecurityRule) | Una regola firewall di ingresso o uscita all'interno di un SecurityGroup. |
| [KeyPair](./KeyPair) | Una chiave pubblica SSH registrata in Aruba Cloud. Usata dai CloudServer per l'accesso. |
| [ElasticIP](./ElasticIP) | Un indirizzo IP pubblico statico. Opzionalmente collegato a un CloudServer. |
| [BlockStorage](./BlockStorage) | Un volume di storage a blocchi. Può essere usato come disco di avvio o volume dati per un CloudServer. |
| [CloudServer](./CloudServer) | Una macchina virtuale. Fa riferimento alla maggior parte degli altri tipi di risorse. |

## Ordine di creazione

Le risorse devono essere create nell'ordine delle dipendenze. L'operatore mantiene una risorsa in fase `Pending` finché tutte le sue dipendenze non sono `Active`.

1. **Project** — nessuna dipendenza; da creare per primo
2. **VPC** — richiede che il Project sia Active
3. **(in parallelo)** I seguenti possono essere creati contemporaneamente, purché il loro genitore sia Active:
   - **Subnet** — richiede VPC Active
   - **SecurityGroup** — richiede VPC Active
   - **BlockStorage** — richiede Project Active
   - **KeyPair** — richiede Project Active
   - **ElasticIP** — richiede Project Active
4. **SecurityRule** — richiede SecurityGroup Active
5. **CloudServer** — richiede Project, VPC, tutte le Subnet referenziate, SecurityGroup, KeyPair e il BlockStorage di avvio Active

## Ordine di eliminazione

Quando elimini le risorse manualmente, eliminale nell'ordine inverso di creazione per evitare conflitti di dipendenza:

1. **CloudServer** — eliminare per primo
2. **SecurityRule**, **ElasticIP** (se collegato) — eliminare prima dei loro genitori
3. **Subnet**, **SecurityGroup**, **KeyPair**, **BlockStorage** — eliminare prima di VPC/Project
4. **VPC** — eliminare dopo che tutti i figli sono stati eliminati
5. **Project** — eliminare per ultimo

:::tip
Se vuoi smontare tutto in una volta, elimina semplicemente il **Project**. L'operatore eliminerà a cascata tutti i figli automaticamente nell'ordine corretto.
:::

## Pattern comuni

### Riferimenti tra risorse

Tutti i riferimenti usano un oggetto `ResourceReference` con `name` e `namespace`:

```yaml
projectReference:
  name: my-project
  namespace: default
```

I riferimenti possono attraversare namespace. L'operatore legge `status.resourceID` dall'oggetto referenziato per risolvere l'ID lato Aruba Cloud.

### Campi comuni

Ogni risorsa ha questi campi:

| Campo | Tipo | Obbligatorio | Descrizione |
|-------|------|-------------|-------------|
| `spec.tenant` | string | No | Identificatore del tenant Aruba Cloud (ARU-XXXXXX). Mutabile, eccetto su Project dove è immutabile. |
| `spec.tags` | []string | No | Etichette propagate alla risorsa Aruba Cloud |

Ogni risorsa **eccetto Project** ha anche:

| Campo | Tipo | Obbligatorio | Predefinito | Descrizione |
|-------|------|-------------|------------|-------------|
| `spec.region` | string | Sì | `ITBG-Bergamo` | Regione Aruba Cloud |
| `spec.projectReference` | ResourceReference | Sì | — | Project proprietario |

### Reporting dello stato

Tutte le risorse espongono uno stato standard:

| Campo | Descrizione |
|-------|-------------|
| `status.phase` | Fase corrente del ciclo di vita: `Pending`, `Creating`, `Active`, `Updating`, `Deleting`, `Deleted` o `Failed` |
| `status.resourceID` | Identificatore lato Aruba Cloud, impostato dopo la creazione della risorsa |
| `status.message` | Descrizione leggibile dello stato corrente |
| `status.conditions` | Condizioni standard Kubernetes con codici reason dettagliati |

Consulta la pagina [Architettura](./architecture#condizioni) per l'elenco completo dei codici reason delle condizioni.
