---
sidebar_position: 99
---

# Contribuire

I contributi all'Aruba Cloud Resource Operator sono benvenuti. Questo documento spiega come configurare un ambiente di sviluppo, aggiungere un nuovo controller e inviare una pull request.

## Prima di iniziare

- Apri una issue per discutere la modifica che vuoi apportare (a meno che non sia già nella roadmap).
- Verifica che nessun altro stia già lavorando alla stessa funzionalità o correzione.

## Configurazione dell'ambiente di sviluppo

Prerequisiti:

- Go 1.23+
- Docker (per i target devtools containerizzati)
- Un cluster Kubernetes attivo (opzionale — `envtest` fornisce un API server simulato per i test unitari)

Elenca tutti i target make disponibili:

```bash
make help
```

Esegui lint e test con l'ambiente containerizzato (Go locale non necessario):

```bash
make lint-ctzd
make test-ctzd
```

## Aggiungere un nuovo controller

### 1. Definire il tipo CRD

Aggiungi `api/v1alpha1/<resource>_types.go` seguendo una risorsa esistente come template (es. `vpc_types.go`). Poi rigenera i manifest e il codice deep-copy:

```bash
make manifests generate
```

### 2. Implementare il controller

Crea `internal/controller/<resource>_controller.go`. Segui la pipeline `HandleReconcile` a 8 stadi documentata in `ai/ARCHITECTURE.md` e il layout canonico a 14 sezioni in `ai/CONVENTIONS.md`.

### 3. Registrare il controller

Aggiungi il controller a `cmd/main.go` seguendo il pattern esistente.

### 4. Aggiungere la documentazione

Crea `docs/website/docs/<Resource>.md` e `docs/website/i18n/it/docusaurus-plugin-content-docs/current/<Resource>.md` usando il template delle pagine CRD in `ai/DOCS.md`. Aggiungi la nuova pagina a `docs/website/sidebars.js`.

### 5. Aggiungere i manifest di esempio

Aggiungi uno o più CR di esempio in `config/samples/` per l'uso nei test di integrazione e nella pagina Esempi.

## Stile del codice

- Formatta con `gofmt` / `goimports` prima di fare commit.
- Segui le convenzioni di naming, gestione degli errori, logging e testing in `ai/CONVENTIONS.md`.
- Non inserire in modo fisso ID tenant, nomi di regione o nomi di zona nel codice di produzione — leggili sempre dallo spec del CRD.
- Tutte le scritture di status devono passare attraverso `retry.RetryOnConflict` usando gli helper in `internal/reconciler/transition_actions.go`.

## Checklist per le pull request

- [ ] `make lint-ctzd` passa senza nuovi errori
- [ ] `make test-ctzd` passa senza nuovi fallimenti
- [ ] `make manifests generate` è stato rieseguito se è cambiato un tipo in `api/v1alpha1/`
- [ ] La documentazione in `docs/website/docs/` è stata aggiornata se i campi CRD o il comportamento dell'operatore sono cambiati
- [ ] La traduzione italiana in `docs/website/i18n/it/` rispecchia le modifiche in inglese
- [ ] La nuova pagina CRD è stata aggiunta a `docs/website/sidebars.js`

## Eseguire i docs in locale

```bash
make docs-install    # installa le dipendenze npm (una volta sola)
make docs            # avvia i docs in inglese su http://localhost:3000
make docs-serve-it   # avvia i docs in italiano
make docs-build      # build di produzione
make docs-test       # build con validazione dei link
```

## Maggiori informazioni

- [Documentazione di Kubebuilder](https://book.kubebuilder.io/introduction.html)
- [Documentazione di controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime)

