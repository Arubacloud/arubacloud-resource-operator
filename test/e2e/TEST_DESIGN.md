# Test Design Document

## Aruba Cloud Resource Operator - Test Coverage Strategy

### Data: 23 Dicembre 2025

## Obiettivo

Documentare la suite di test end-to-end (e2e) implementata che copre tutti i reconciler dell'operator Aruba Cloud attraverso casi d'uso realistici e incrementali.

## Stato Implementazione

✅ **Completa** - Tutti i test sono implementati e funzionanti nella directory `test/e2e/`

### Statistiche Codebase

- **10 file di test** totali
- **8 test suite** (escluso il test manager di base)
- **100% copertura** di tutti i 9 reconciler

### Risultati Test Eseguiti

| Test ID | Nome Test                 | Esito   | Durata  |
| ------- | ------------------------- | ------- | ------- |
| 01      | 01-ProjectOnly            | ✅ PASS | 73.82s  |
| 02      | 02-NetworkBasic           | ✅ PASS | 279.37s |
| 03      | 03-NetworkWithSecurity    | ✅ PASS | 333.45s |
| 04      | 04-StorageBasic           | ✅ PASS | 195.65s |
| 05      | 05-StorageMulti           | ✅ PASS | 200.08s |
| 06      | 06-NetworkComplete        | ✅ PASS | 364.81s |
| 07      | 07-Compute                | ✅ PASS | 464.69s |
| 08      | 08-ComputeWithDataVolumes | ✅ PASS | 369.94s |

**Note**:

- I test sono stati eseguiti individualmente con focus specifico tramite `make test-e2e FOCUS="<test-id>"`.
- La durata include setup del cluster Kind, deploy del controller manager e cleanup finale.

## Analisi delle Risorse

### Risorse Disponibili

L'operator gestisce 9 tipi di risorse Kubernetes custom:

1. **Project** - Risorsa base per tutte le altre
2. **Vpc** - Virtual Private Cloud (dipende da Project)
3. **SecurityGroup** - Gruppo di sicurezza (dipende da Project/Vpc)
4. **SecurityRule** - Regola di sicurezza (dipende da SecurityGroup)
5. **Subnet** - Sottorete (dipende da Vpc e Project)
6. **BlockStorage** - Volume storage (dipende da Project)
7. **ElasticIP** - IP pubblico elastico (dipende da Project)
8. **KeyPair** - Coppia chiavi SSH (dipende da Project)
9. **CloudServer** - Server virtuale (dipende da tutte le precedenti)

### Grafo delle Dipendenze

```
Project (base)
├── Vpc
│   ├── SecurityGroup
│   │   └── SecurityRule
│   └── Subnet
├── BlockStorage (boot/data)
├── ElasticIP
└── KeyPair

CloudServer (dipende da: Project, Vpc, Subnet, SecurityGroup, BlockStorage, KeyPair, [ElasticIP])
```

## Architettura dei Test

### Framework e Tecnologie

- **Testing Framework**: Ginkgo v2 (BDD-style)
- **Assertion Library**: Gomega
- **Container Runtime**: Kind (Kubernetes in Docker)
- **Execution**: Make target `test-e2e`
- **CI/CD Ready**: GitHub Actions compatible

### Struttura File

```
test/e2e/
├── e2e_suite_test.go                     # Setup suite principale
├── 00_manager_test.go                    # Test controller manager
├── 01_project_only_test.go               # Layer 1: Base
├── 02_network_basic_test.go              # Layer 2: Network
├── 03_network_with_security_test.go      # Layer 2: Network + Security
├── 04_storage_basic_test.go              # Layer 3: Storage
├── 05_storage_multi_test.go              # Layer 3: Multi-Storage
├── 06_network_complete_test.go           # Layer 4: Network + IP
├── 07_compute_test.go                    # Layer 5: Compute
└── 08_compute_with_data_volumes_test.go  # Layer 5: Compute + Data Volumes
```

## Test Suite Implementate

### Layer 0: Infrastructure

#### 00_manager_test.go

- **Scopo**: Validare deployment e health del controller manager
- **Test**:
  - Deployment controller in namespace `aruba-system`
  - Health check del controller pod
  - Validazione RBAC e ServiceAccount
  - Test metriche Prometheus
  - Cleanup automatico
- **Pattern**: `Ordered` test suite con BeforeAll/AfterAll hooks

### Layer 1: Base (Project)

#### 01_project_only_test.go

- **Reconciler testati**: `ProjectReconciler`
- **Risorse**: 1 Project
- **Test**:
  - Creazione progetto
  - Wait fino a fase `Created`
  - Validazione `resourceID` in status
  - Deletion e cleanup
- **Timeout**: 20 minuti

### Layer 2: Network Foundation

#### 02_network_basic_test.go

- **Reconciler testati**: `ProjectReconciler`, `VpcReconciler`, `SubnetReconciler`
- **Risorse**: Project → VPC → Subnet
- **Test**:
  - Creazione sequenziale con dependency resolution
  - Validazione propagazione ProjectID
  - Verificare tutti i `resourceID`
  - Cleanup in ordine inverso
- **Pattern**: Single It block con step multipli
- **Timeout**: 20 minuti

#### 03_network_with_security_test.go

- **Reconciler testati**: Project, VPC, SecurityGroup, SecurityRule, Subnet
- **Risorse**: 5 risorse con dipendenze complesse
- **Test**:
  - SecurityGroup dipende da VPC
  - SecurityRule dipende da SecurityGroup
  - Validazione catena di dipendenze
- **Timeout**: 20 minuti

### Layer 3: Storage

#### 04_storage_basic_test.go

- **Reconciler testati**: `ProjectReconciler`, `BlockStorageReconciler`
- **Risorse**: Project + 1 BlockStorage (boot)
- **Test**:
  - Creazione volume boot
  - Validazione tipo e size
  - Status resourceID
- **Timeout**: 10 minuti

#### 05_storage_multi_test.go

- **Reconciler testati**: Project, BlockStorage (multipli)
- **Risorse**: 1 Project + 3 BlockStorage (1 boot + 2 data)
- **Test**:
  - Gestione volumi multipli
  - Differenziazione boot vs data volume
  - Validazione IDs distinti
- **Pattern**: Test parallelo creazione volumi
- **Timeout**: 15 minuti

### Layer 4: Network Complete

#### 06_network_complete_test.go

- **Reconciler testati**: Project, VPC, SecurityGroup, SecurityRule, Subnet, ElasticIP
- **Risorse**: Stack network completo (6 risorse)
- **Test**:
  - Network completa con IP pubblico
  - ElasticIP assignment
  - Validazione integrazione completa
- **Timeout**: 20 minuti

### Layer 5: Compute Integration

#### 07_compute_test.go

- **Reconciler testati**: Project, VPC, SecurityGroup, SecurityRule, Subnet, BlockStorage, KeyPair, CloudServer
- **Risorse**: Stack completo per compute (8 risorse)
- **Test**:
  - Applicazione di tutti i manifest in una volta
  - CloudServer con boot volume
  - Wait solo per CloudServer fino a `Created`
  - Validazione completa di tutti i resourceID nello status del CloudServer
- **Pattern**: Single It block con apply combinato
- **Complessità**: Test completo per compute base
- **Timeout**: 20 minuti

#### 08_compute_with_data_volumes_test.go

- **Reconciler testati**: Tutti con multiple BlockStorage
- **Risorse**: CloudServer + 1 boot + 1 data volume
- **Test**:
  - Applicazione di tutti i manifest in una volta
  - CloudServer con volumi dati
  - Attachment volumi dati a server running
  - Wait solo per CloudServer fino a `Created`
  - Validazione dataVolumeIDs nello status
  - Gestione multiple dependencies
- **Pattern**: Single It block con apply combinato
- **Complessità**: Test più completo per storage multiplo
- **Timeout**: 20 minuti
  - Validazione volume mounting
- **Pattern**: Complex dependency graph
- **Timeout**: 20 minuti

## Riconciler Coverage Summary

| Reconciler        | Tested In                  | Test Count |
| ----------------- | -------------------------- | ---------- |
| ProjectReconciler | 01, 02, 03, 04, 05, 06, 08 | 7          |
| VpcReconciler     | 02, 03, 06, 08             | 4          |
| SecurityGroupRec. | 03, 06, 08                 | 3          |
| SecurityRuleRec.  | 03, 06, 08                 | 3          |
| SubnetReconciler  | 02, 03, 06, 08             | 4          |
| BlockStorageRec.  | 04, 05, 08                 | 3          |
| ElasticIPRec.     | 06, 07, 08                 | 3          |
| KeyPairReconciler | 07, 08                     | 2          |
| CloudServerRec.   | 07, 08                     | 2          |

## Aspetti Testati per Reconciler

### Stati del Ciclo di Vita

Ogni reconciler implementa una state machine che viene validata nei test:

1. **Init**: Inizializzazione e aggiunta finalizer
   - Test: Verifica presenza finalizer dopo creazione
2. **Creating**: Chiamata API Aruba Cloud per creare la risorsa
   - Test: Verifica transizione da Init a Creating
3. **Provisioning**: Attesa che la risorsa remota sia ready
   - Test: Polling status fino a completamento
4. **Created**: Risorsa creata e pronta all'uso
   - Test: Verifica `status.phase == "Created"`
   - Test: Verifica presenza `status.resourceID`
5. **Updating**: Aggiornamento risorsa esistente (se supportato)
   - Test: Modifica spec e verifica propagazione
6. **Deleting**: Rimozione risorsa e cleanup
   - Test: Delete e wait fino a scomparsa da K8s
   - Test: Cleanup finalizer

### Dependency Resolution

I test validano la gestione delle dipendenze tra risorse:

#### ResourceReference Resolution

```go
// Esempio da VPC → Project
spec:
  projectRef:
    name: my-project  # Risolto a projectID
```

**Test validano**:

- Resolution di `ResourceReference` tra risorse
- Wait automatico se risorsa dipendente non è `Created`
- Propagazione di ID tra risorse (es. `ProjectID` → VPC → Subnet)
- Error handling per riferimenti mancanti o invalidi

#### Dependency Chains

I test validano catene complete:

```
Project (resourceID: PRJ-123)
  ↓
VPC (projectID: PRJ-123, resourceID: VPC-456)
  ↓
Subnet (projectID: PRJ-123, vpcID: VPC-456, resourceID: SUB-789)
```

**Pattern testato**:

1. Creazione Project
2. Wait fino a `status.resourceID` popolato
3. Creazione VPC (reference a Project)
4. VPC reconciler risolve projectRef → projectID
5. Wait VPC Created
6. Creazione Subnet (reference a Project e VPC)
7. Validazione propagazione IDs completa

### Error Handling e Resilienza

I test validano comportamenti in caso di errori:

1. **Retry Logic**
   - Transient API errors → exponential backoff
   - Test: Simulazione failures temporanei
2. **Resource Not Found**
   - Dipendenza cancellata durante reconciliation
   - Test: Delete dependency e verifica blocking
3. **API Errors**
   - Invalid parameters
   - Quota exceeded
   - Test: Verifica error reporting in status
4. **Finalizer Cleanup**
   - Test: Delete risorse con dipendenti ancora attivi
   - Validazione: Finalizer blocca deletion fino a cleanup

## Esecuzione dei Test

### Comandi di Esecuzione

#### Eseguire tutti i test (full suite)

```bash
make test-e2e
```

Questo comando:

1. Crea automaticamente un cluster Kind (`aruba-test-e2e`)
2. Genera i manifest necessari
3. Applica i CRD
4. Esegue tutti i test e2e
5. Pulisce il cluster al termine

#### Eseguire test specifici

Per eseguire solo alcuni test, usa la variabile `FOCUS`:

```bash
# Esegui solo il test del manager
make test-e2e FOCUS="00-Manager"

# Esegui solo i test di network
make test-e2e FOCUS="Network"

# Esegui solo un test specifico
make test-e2e FOCUS="01-ProjectOnly"

# Esegui test multipli usando regex
make test-e2e FOCUS="02-NetworkBasic|03-NetworkWithSecurity"

# Esegui tutti i test di compute
make test-e2e FOCUS="Compute"
```

#### Cleanup manuale del cluster

Se necessario pulire manualmente il cluster di test:

```bash
make cleanup-test-e2e
```
