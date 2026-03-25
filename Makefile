# Image URL to use all building/pushing image targets
IMG ?= controller:latest

# Dynamic inputs you pass on the CLI:
# make build-installer CLIENT_ID=... CLIENT_SECRET=...
CLIENT_ID     ?=
CLIENT_SECRET ?=

# Paths
CRD_DIR     := config/crd        # <- contains kustomization.yaml for CRDs
OVERLAY     := config/default
MANAGER_DIR := config/manager

# Generated env files (must match kustomization.yaml `envs:` entries)
CFG_ENV_DYNAMIC := $(MANAGER_DIR)/dynamic-config.env
SEC_ENV_DYNAMIC := $(MANAGER_DIR)/dynamic-secret.env

MOCKS_DIR := internal/mocks
# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: $(CONTROLLER_GEN) ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: $(CONTROLLER_GEN) $(MOCKERY) ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."
	$(MOCKERY)

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet $(ENVTEST) ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= aruba-test-e2e
FOCUS ?=

.PHONY: setup-test-e2e
setup-test-e2e: cleanup-test-e2e ## Set up a Kind cluster for e2e tests
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	$(KIND) create cluster --name $(KIND_CLUSTER)

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind. Use FOCUS="test-name" to run specific tests.
	$(KUSTOMIZE) build $(CRD_DIR) | $(KUBECTL) apply -f -
	KIND_CLUSTER=$(KIND_CLUSTER) go test ./test/e2e/ -v -ginkgo.v $(if $(FOCUS),-ginkgo.focus="$(FOCUS)")
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER) 2>/dev/null || true

.PHONY: lint
lint: $(GOLANGCI_LINT) ## Run golangci-lint linter
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint linter and perform fixes
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: ## Verify golangci-lint linter configuration
	$(GOLANGCI_LINT) config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	- $(CONTAINER_TOOL) buildx create --name aruba-builder
	$(CONTAINER_TOOL) buildx use aruba-builder
	- $(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .
	- $(CONTAINER_TOOL) buildx rm aruba-builder
	rm Dockerfile.cross

HELMIFY ?= $(LOCALBIN)/helmify

.PHONY: helmify
helmify: $(HELMIFY) ## Download helmify locally if necessary.
$(HELMIFY): $(LOCALBIN)
	$(call go-install-tool,$(HELMIFY),github.com/arttor/helmify/cmd/helmify,$(HELMIFY_VERSION))

helm-operator: manifests $(KUSTOMIZE) $(HELMIFY)
	$(KUSTOMIZE) build config/default | $(HELMIFY) config/charts/arubacloud-resource-operator

helm-crd: manifests $(KUSTOMIZE) $(HELMIFY)
	$(KUSTOMIZE) build config/crd | $(HELMIFY) config/charts/arubacloud-resource-operator-crd

.PHONY: _ensure_dynamic_env
_ensure_dynamic_env:
	@mkdir -p "$(MANAGER_DIR)"
	@printf "client-id=%s\n" "$${CLIENT_ID:-dummy}"         >  "$(CFG_ENV_DYNAMIC)"
	@printf "client-secret=%s\n" "$${CLIENT_SECRET:-dummy}" >  "$(SEC_ENV_DYNAMIC)"

.PHONY: install uninstall deploy undeploy

install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build $(CRD_DIR) | $(KUBECTL) apply --namespace=aruba-system -f -

uninstall: manifests kustomize ## Uninstall CRDs. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	$(KUSTOMIZE) build $(CRD_DIR) | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	@# Set controller image
	cd $(MANAGER_DIR) && $(KUSTOMIZE) edit set image controller=$(IMG)

	# Apply overlay
	$(KUSTOMIZE) build $(OVERLAY) | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@# ensure files exist so kustomize can build, even if vars aren't set now
	@$(MAKE) --no-print-directory _ensure_dynamic_env

	$(KUSTOMIZE) build $(OVERLAY) | $(KUBECTL) delete --ignore-not-found=$(ignore-not-found) -f -

	@# cleanup
	@rm -f "$(CFG_ENV_DYNAMIC)" "$(SEC_ENV_DYNAMIC)"

clean-installer:
	@rm -f dist/install.yaml "$(CFG_ENV_DYNAMIC)" "$(SEC_ENV_DYNAMIC)"

# Clean the generated files
clean:
	@echo "Cleaning generated files..."
	@rm -f $(MOCKS_DIR)/*.go

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
MOCKERY ?= $(LOCALBIN)/mockery

## Tool Versions
KUSTOMIZE_VERSION ?= v5.6.0
CONTROLLER_TOOLS_VERSION ?= v0.18.0
#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell go list -m -f "{{ .Version }}" sigs.k8s.io/controller-runtime | awk -F'[v.]' '{printf "release-%d.%d", $$2, $$3}')
#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell go list -m -f "{{ .Version }}" k8s.io/api | awk -F'[v.]' '{printf "1.%d", $$3}')
GOLANGCI_LINT_VERSION ?= v2.1.6
MOCKERY_VERSION ?= v2.53.5
HELMIFY_VERSION ?= v0.4.19

# go-install-all-tools installs all development tools to $(LOCALBIN) using 'go install'.
# Run this once to set up a host development environment.
# In the containerized workflow (make <target>-ctzd) tools are pre-installed in the image.
.PHONY: go-install-all-tools
go-install-all-tools: $(LOCALBIN) ## Install all development tools to $(LOCALBIN) via go install.
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))
	$(call go-install-tool,$(MOCKERY),github.com/vektra/mockery/v2,$(MOCKERY_VERSION))
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))
	$(call go-install-tool,$(HELMIFY),github.com/arttor/helmify/cmd/helmify,$(HELMIFY_VERSION))
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

# Individual tool install targets (kept for convenience; also called by go-install-all-tools).
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Install kustomize to $(LOCALBIN).
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Install controller-gen to $(LOCALBIN).
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download envtest K8s binaries to $(LOCALBIN).
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@$(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Install setup-envtest to $(LOCALBIN).
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Install golangci-lint to $(LOCALBIN).
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: mockery
mockery: $(MOCKERY) ## Install mockery to $(LOCALBIN).
$(MOCKERY): $(LOCALBIN)
	$(call go-install-tool,$(MOCKERY),github.com/vektra/mockery/v2,$(MOCKERY_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f $(1) || true ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
mv $(1) $(1)-$(3) ;\
} ;\
ln -sf $(1)-$(3) $(1) || true
endef

##@ Containerized Development

DEVTOOLS_IMAGE      ?= arubacloud-resource-operator-devtools:local
DEVTOOLS_DOCKERFILE := ci/devtools/Dockerfile
DEVTOOLS_BIN        := /devtools/bin

# Allocate a TTY when stdin is a terminal (needed for make sh-ctzd); use -i only in CI
_CTZD_INTERACTIVE := $(shell [ -t 0 ] && echo "-it" || echo "-i")

# Auto-detect podman-docker: if the docker command is actually podman, use --userns=keep-id
# (rootless podman requires keep-id for bind mounts to be readable by the in-container process).
# Real Docker does not support --userns=keep-id, so we use --user UID:GID there instead.
_DOCKER_IS_PODMAN := $(shell docker --version 2>/dev/null | grep -qi podman && echo 1)
ifdef _DOCKER_IS_PODMAN
  _CTZD_USER_FLAGS := --userns=keep-id
else
  _CTZD_USER_FLAGS := --user $$(id -u):$$(id -g)
endif

# Stamp file: tracks whether the devtools image needs rebuilding when the Dockerfile changes
_DEVTOOLS_STAMP := $(LOCALBIN)/.devtools-image.stamp

$(_DEVTOOLS_STAMP): $(DEVTOOLS_DOCKERFILE)
	@mkdir -p $(dir $@)
	$(CONTAINER_TOOL) build -t $(DEVTOOLS_IMAGE) -f $(DEVTOOLS_DOCKERFILE) .
	@touch $@

.PHONY: devtools-image
devtools-image: $(_DEVTOOLS_STAMP) ## Build the devtools container image.

.PHONY: devtools-image-clean
devtools-image-clean: ## Remove the devtools container image and its named caches.
	$(CONTAINER_TOOL) rmi $(DEVTOOLS_IMAGE) 2>/dev/null || true
	$(CONTAINER_TOOL) volume rm devtools-gomodcache devtools-gobuildcache devtools-lintcache 2>/dev/null || true
	@rm -f $(_DEVTOOLS_STAMP)

# Pattern rule: make <target>-ctzd runs make <target> inside the devtools container.
# Example: make lint-ctzd  ->  docker run ... make lint
# UID/GID mapping is auto-detected: --userns=keep-id for podman-docker, --user UID:GID for real Docker.
%-ctzd: $(_DEVTOOLS_STAMP)
	$(CONTAINER_TOOL) run --rm \
		$(_CTZD_INTERACTIVE) \
		$(_CTZD_USER_FLAGS) \
		--security-opt label=disable \
		-e HOME=/tmp \
		-e LOCALBIN=$(DEVTOOLS_BIN) \
		-v $(CURDIR):/workspace \
		-v devtools-gomodcache:/go/pkg/mod \
		-v devtools-gobuildcache:/tmp/.cache/go-build \
		-v devtools-lintcache:/tmp/.cache/golangci-lint \
		-w /workspace \
		$(DEVTOOLS_IMAGE) \
		make $*

.PHONY: sh
sh: ## Open an interactive shell (use as: make sh-ctzd).
	bash

# Local development targets
.PHONY: dev-setup
dev-setup: ## Setup local development environment
	@echo "Setting up local development environment..."
	chmod +x hack/local-test.sh
	chmod +x hack/test-workflow.sh
	@echo "✅ Development environment ready"

.PHONY: test-local
test-local: dev-setup ## Run local testing workflow
	@echo "🧪 Running local tests..."
	./hack/test-workflow.sh

.PHONY: run-dev
run-dev: install ## Run controller in development mode
	@echo "🚀 Starting controller in development mode..."
	./hack/local-test.sh

.PHONY: logs
logs: ## Show controller logs (if running in cluster)
	kubectl logs -f deployment/aruba-operator-controller-manager -n aruba-operator-system -c manager

.PHONY: debug-project
debug-project: ## Debug specific ArubaProject
	@read -p "Enter project name: " name; \
	kubectl describe arubaproject $$name; \
	echo "--- Project YAML ---"; \
	kubectl get arubaproject $$name -o yaml

##@ Helm Charts Release

HELM_CHARTS_REPO ?= https://github.com/Arubacloud/helm-charts.git
HELM_CHARTS_DIR ?= /tmp/helm-charts
CHART_VERSION ?= $(shell echo $(IMG) | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' || echo "0.1.0")

.PHONY: push-charts
push-charts: helm-operator helm-crd ## Generate and push Helm charts to helm-charts repository
	@echo "📦 Preparing to push Helm charts to $(HELM_CHARTS_REPO)..."
	@echo "Chart version: $(CHART_VERSION)"
	
	# Clean and clone helm-charts repository
	rm -rf $(HELM_CHARTS_DIR)
	git clone $(HELM_CHARTS_REPO) $(HELM_CHARTS_DIR)
	
	# Update chart versions
	@echo "Updating Chart.yaml versions..."
	@if [ "$$(uname)" = "Darwin" ]; then \
		sed -i '' 's/^version: .*/version: $(CHART_VERSION)/' config/charts/arubacloud-resource-operator/Chart.yaml; \
		sed -i '' 's/^appVersion: .*/appVersion: "$(CHART_VERSION)"/' config/charts/arubacloud-resource-operator/Chart.yaml; \
		sed -i '' 's/^version: .*/version: $(CHART_VERSION)/' config/charts/arubacloud-resource-operator-crd/Chart.yaml; \
		sed -i '' 's/^appVersion: .*/appVersion: "$(CHART_VERSION)"/' config/charts/arubacloud-resource-operator-crd/Chart.yaml; \
	else \
		sed -i 's/^version: .*/version: $(CHART_VERSION)/' config/charts/arubacloud-resource-operator/Chart.yaml; \
		sed -i 's/^appVersion: .*/appVersion: "$(CHART_VERSION)"/' config/charts/arubacloud-resource-operator/Chart.yaml; \
		sed -i 's/^version: .*/version: $(CHART_VERSION)/' config/charts/arubacloud-resource-operator-crd/Chart.yaml; \
		sed -i 's/^appVersion: .*/appVersion: "$(CHART_VERSION)"/' config/charts/arubacloud-resource-operator-crd/Chart.yaml; \
	fi
	
	# Copy charts to helm-charts repo
	@echo "Copying charts..."
	mkdir -p $(HELM_CHARTS_DIR)/charts/arubacloud-resource-operator
	mkdir -p $(HELM_CHARTS_DIR)/charts/arubacloud-resource-operator-crd
	cp -R config/charts/arubacloud-resource-operator/* $(HELM_CHARTS_DIR)/charts/arubacloud-resource-operator/
	cp -R config/charts/arubacloud-resource-operator-crd/* $(HELM_CHARTS_DIR)/charts/arubacloud-resource-operator-crd/
	
	# Create branch, commit and push
	cd $(HELM_CHARTS_DIR) && \
		git checkout -b update-arubacloud-resource-operator-$$(date +%s) && \
		git add . && \
		git commit -m "Update chart arubacloud-resource-operator $(CHART_VERSION)" && \
		git push origin HEAD
	
	@echo "✅ Charts pushed successfully!"
