# Test Runner

Questo script (`test_runner.sh`) permette di eseguire test automatici sui manifest Kubernetes con placeholder personalizzabili.

## Utilizzo

```sh
NN=10 TENANT=ARU-329997 NAME=aruba-resource ACTION=apply ./test_runner.sh
```

- `NN`: numero del set di test da eseguire (es. 1, 2, ... 10)
- `TENANT`: valore che sostituisce il placeholder `__TENANT__` nei manifest
- `NAME`: valore che sostituisce il placeholder `__NAME__` nei manifest
- `ACTION`: azione kubectl (`apply`, `delete`, ...)

## Funzionamento

1. Lo script legge i file elencati in `fixtures/TestNN`.
2. Per ogni file, sostituisce i placeholder `__TENANT__` e `__NAME__` con i valori forniti.
3. Applica i manifest modificati tramite `kubectl $ACTION -f ...`.

## Esempio

Per applicare i manifest del test set 10 con tenant e nome personalizzati:

```sh
NN=10 TENANT=mio-tenant NAME=mio-nome ACTION=apply ./test_runner.sh
```

## Note

- I file di test devono essere elencati in `test/scripts/fixtures/TestNN`.
- I manifest originali devono trovarsi in `config/samples`.
- Puoi aggiungere altri placeholder e variabili modificando lo script.
