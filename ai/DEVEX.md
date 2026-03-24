# Developer Experience

## Commands

```bash
# Build
make build                        # Build manager binary to bin/manager
make manifests                    # Regenerate CRDs, RBAC, webhook manifests (run after API changes)
make generate                     # Regenerate DeepCopy methods and mocks (run after interface/type changes)

# Test
make test                         # Run unit tests (also runs manifests, generate, fmt, vet)
make test-e2e FOCUS="test-name"   # Run e2e tests against a Kind cluster (optional FOCUS filter)
make setup-test-e2e               # Create Kind cluster for e2e
make cleanup-test-e2e             # Tear down Kind cluster

# Run a single test
go test ./internal/controller/... -run "TestBlockStorage" -v

# Lint
make lint                         # Run golangci-lint
make lint-fix                     # Run golangci-lint with auto-fix

# Run locally
make run-dev                      # Run controller locally (installs CRDs first)
```

## Verifying metrics

When running the operator locally, metrics are available at `http://localhost:8080/metrics`:

```bash
curl http://localhost:8080/metrics | grep aruba_
```

## Manual Testing with test_runner.sh

`test/scripts/test_runner.sh` applies or deletes a set of Kubernetes resources against a live cluster for manual integration testing.

### How it works

The script reads a **fixture file** from `test/scripts/fixtures/` identified by `NN` (test number) and `QNT` (variant suffix). Each fixture file is a plain text list of sample filenames from `config/samples/`. For each file listed, the script substitutes the placeholders `__TENANT__`, `__NAME__`, and `__NAMESPACE__` and runs `kubectl $ACTION` on the result.

### Usage

Run from the `test/scripts/` directory:

```bash
# Apply resources
NN=1 QNT=01 ACTION=apply TENANT=my-tenant NAME=my-resource ./test_runner.sh

# Delete the same resources
NN=1 QNT=01 ACTION=delete TENANT=my-tenant NAME=my-resource ./test_runner.sh
```

**Variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `NN` | `1` | Test set number (selects `fixtures/Test<NN>_<QNT>`) |
| `QNT` | `00` | Variant suffix; falls back to `_00` if the file is not found |
| `ACTION` | `apply` | Any valid `kubectl` subcommand (`apply`, `delete`, `get`, …) |
| `TENANT` | `ARU-329997` | Replaces `__TENANT__` in sample manifests |
| `NAME` | `aruba-resource` | Replaces `__NAME__` in sample manifests |
| `NAMESPACE` | `default` | Replaces `__NAMESPACE__` in sample manifests |

### Existing fixture sets

| Fixture | Resources applied |
|---------|------------------|
| `Test1_00` | Project |
| `Test1_01` | Project + BlockStorage |
| `Test2_00` | Project + VPC |
| `Test3_00` | Project + (see file) |
| `Test6_00` | Project + VPC + SecurityGroup + SecurityRule + Subnet + BlockStorage |
| `Test6_02..06` | Same as Test6_00 + additional BlockStorages |
| `Test7_00` | Test6_00 + ElasticIP |
| `Test8_00` | Test7_00 + KeyPair |
| `Test9_00` | Test8_00 + CloudServer |
| `Test10` | Full stack with data-volume CloudServer |
| `Test11` | Full stack (different ordering — ElasticIP before SecurityGroup) |

### Creating a new fixture file

1. Choose a name following the convention `Test<NN>_<QNT>` (e.g. `Test1_02` for a new variant of test 1).
2. Create the file in `test/scripts/fixtures/` listing one sample filename per line (relative to `config/samples/`):

   ```
   arubacloud.com_v1alpha1_project.yaml
   arubacloud.com_v1alpha1_blockstorage.yaml
   ```

3. If the required resource shape does not exist yet, add a new sample file in `config/samples/` using `__TENANT__`, `__NAME__`, and `__NAMESPACE__` as placeholders where the real values should go.

4. Run the fixture:

   ```bash
   NN=1 QNT=02 ACTION=apply TENANT=test-tenant NAME=my-test ./test_runner.sh
   ```
