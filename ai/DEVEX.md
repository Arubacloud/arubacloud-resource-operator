# Developer Experience

## Commands

```bash
# Host setup: install all development tools to bin/ via go install (one-time)
make go-install-all-tools

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

> **Note:** Make targets no longer auto-install tools as a side effect. Either run `make go-install-all-tools` once for host development, or use the containerized workflow below.

## Containerized Development

Any Make target can be run inside a purpose-built container so that every developer uses identical tool versions regardless of host OS. The pattern is `make <target>-ctzd`.

```bash
# First-time setup: build the devtools image (takes a few minutes; cached after that)
make devtools-image

# Run any target inside the container
make fmt-ctzd
make lint-ctzd
make vet-ctzd
make generate-ctzd
make manifests-ctzd
make test-ctzd
make build-ctzd
make helm-operator-ctzd
make helm-crd-ctzd

# Open an interactive shell in the container
make sh-ctzd

# Remove the image and all caches
make devtools-image-clean
```

The devtools image (`devex/build/Dockerfile`) is based on `golang:1.24-bookworm` and includes all development tools at pinned versions:

| Tool | Version | Install method |
|------|---------|----------------|
| golangci-lint | v2.1.6 | `go install` (built with Go 1.24; v2 required for `.golangci.yml` v2 config format) |
| mockery | v2.53.5 | Precompiled binary |
| kustomize | v5.6.0 | Precompiled binary |
| controller-gen | v0.18.0 | `go install` |
| helmify | v0.4.19 | `go install` |
| setup-envtest | release-0.21 | `go install` |

**How it works:**

- The repo is bind-mounted at `/workspace` inside the container; generated files (mocks, CRDs, binaries) are written directly to the host filesystem with the caller's UID/GID.
- Tools are pre-installed in the image at `/devtools/bin` — they are never installed on the host. `LOCALBIN=/devtools/bin` is injected as a container environment variable so it applies to both the initial `make` call and any commands typed in `make sh-ctzd`.
- Go module cache, build cache, and golangci-lint cache are stored in named Docker/Podman volumes and reused across runs.
- The image is rebuilt automatically when `devex/build/Dockerfile` changes (tracked via a stamp file in `bin/`).

**Podman / podman-docker compatibility:** The Makefile auto-detects whether the `docker` command is actually podman-docker (by inspecting `docker --version`). When podman-docker is detected, `--userns=keep-id` is used (required for rootless podman bind mount access); otherwise `--user $(id -u):$(id -g)` is used for real Docker. `--security-opt label=disable` is always passed to prevent SELinux from blocking bind mount access on enforcing systems (e.g. Fedora). No manual configuration is needed.

**Targets NOT suitable for containerization** (require host Docker daemon, live Kubernetes cluster, or Kind): `docker-build`, `docker-push`, `install`, `deploy`, `undeploy`, `run`, `run-dev`, `test-e2e`, `setup-test-e2e`, `cleanup-test-e2e`, `logs`, `push-charts`.

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
