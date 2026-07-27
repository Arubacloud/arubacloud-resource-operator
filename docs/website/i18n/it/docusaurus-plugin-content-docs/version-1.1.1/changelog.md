---
title: Changelog
sidebar_position: 12
---

# Changelog

Tutte le modifiche rilevanti a questo progetto saranno documentate in questo file.

Il formato è basato su [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
Le versioni seguono lo schema `MAJOR.MINOR.PATCH`; MINOR viene incrementato per i rilasci di funzionalità e PATCH per le correzioni.

## [Unreleased]

## [1.1.2] - 2026-07-27

### Aggiunto

- **Vault KV prefix** (`config.auth.multi.vault.kvPrefix`) — un prefisso di percorso opzionale anteposto al tenant nel mount KV di Vault, che consente a un singolo backend KV di servire più ambienti: `<kv-mount>/<kv-prefix>/<tenant>`; sdk-go aggiornato a v1.0.8.

## [1.1.1] - 2026-07-22

### Corretto

- Corretto l'URL dell'emittente OAuth2 utilizzato per l'autenticazione Keycloak (#50); sdk-go aggiornato a v1.0.7.
- Ripristinata la suite di test end-to-end dopo la migrazione a sdk-go v1.0.4 (#49).

## [1.1.0] - 2026-07-20

### Modificato

- Aggiornato sdk-go da v0.1.24 a v1.0.4 per allinearsi alla linea di rilascio stabile dell'SDK.

## [1.0.0] - 2026-04-03

### Aggiunto

- **Tutti i 9 controller CRD** — `Project`, `VPC`, `Subnet`, `SecurityGroup`, `SecurityRule`, `KeyPair`, `ElasticIP`, `BlockStorage`, `CloudServer`; ciascuno gestito attraverso un ciclo di vita standard `Pending → Creating → Active → Deleting → Deleted` con diramazioni `Updating` e `Failed`.
- **Framework di riconciliazione a macchina a stati generica** (`internal/reconciler/`) — `TransitionSet` ordinato valutato per ogni ciclo di riconciliazione; ogni `AbstractTransition` dichiara condizioni K8s e CMP indipendenti, azioni e strategie di requeue.
- **Timeout per le fasi transitorie** — le risorse bloccate in `Creating`, `Updating` o `Deleting` per più di 10 minuti passano automaticamente a `Failed`; un'uscita di sicurezza `ValidationFailedAndDeleting` permette di eliminare comunque le risorse bloccate.
- **Validazione di consistenza tra risorse** — motore a due livelli (`ivs` per gli intenti K8s prima delle chiamate CMP, `vs` per il rilevamento di drift post-creazione) che verifica la coerenza di `tenant`, `project`, `region`, `zone` e `vpcReference` tra tutte le risorse referenziate.
- **Eliminazione a cascata** — modello di proprietà a due livelli personalizzato (annotazione + label) in sostituzione dei OwnerReferences Kubernetes standard, che abilita la cascata genitore–figlio cross-namespace; l'eliminazione del genitore attende che tutti i figli raggiungano `Deleted` prima di chiamare l'API CMP.
- **Supporto multi-tenant** — client Aruba Cloud per tenant risolti al momento della riconciliazione; le credenziali vengono recuperate da HashiCorp Vault AppRole (`kv-mount/tenant`).
- **Blocco dei campi immutabili** — le modifiche a `spec.tenant` sono bloccate; gli aggiornamenti allo spec di `SecurityRule` sono bloccati (il CMP non dispone di API di aggiornamento per le security rule).
- **Metriche di riconciliazione** — istogramma Prometheus `aruba_reconcile_step_duration_seconds` esposto su `:9080/metrics`; etichettato per `resource_kind`, `result`, `phase` e `reason`.
- **Log JSON strutturato** — logr con `log/slog`; ogni controller arricchisce il logger con il campo `tenant` per la correlazione dei log per tenant.
- **Helm chart** — modalità di installazione single-tenant (credenziali dirette) e multi-tenant (Vault AppRole); i CRD installabili come sub-chart separato.
- **Sito di documentazione Docusaurus** (EN + IT) — copre tutti i 9 CRD con campi `spec`, campi `status`, ciclo di vita, riferimento rapido `kubectl` e guida all'installazione.
- **Ambiente di sviluppo containerizzato** — immagine devtools per eseguire i target `make lint` e `make test` senza un'installazione locale di Go.
- **CI GitHub Actions** — pipeline di lint, test e rilascio usando l'immagine devtools.

### Corretto

- Workaround per il problema del filtro API di rete nel CMP (DEV-66643).
- Workaround per i fallimenti di creazione del CloudServer quando Subnet e BlockStorage non sono ancora pronti.
- Campo `location` mancante nelle richieste di creazione Subnet.
- Panic da nil dereference nella riconciliazione degli aggiornamenti di Project e BlockStorage.
- Errori di conflitto sullo `status` durante aggiornamenti concorrenti, risolti con `retry.RetryOnConflict`.

[Unreleased]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.2...HEAD
[1.1.2]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.1...v1.1.2
[1.1.1]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/Arubacloud/arubacloud-resource-operator/releases/tag/v1.0.0
