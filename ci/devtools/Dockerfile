FROM golang:1.24-bookworm

RUN apt-get update && apt-get install -y --no-install-recommends \
        make \
        git \
        curl \
    && rm -rf /var/lib/apt/lists/*

ENV DEVTOOLS_BIN=/devtools/bin
RUN mkdir -p ${DEVTOOLS_BIN}

# --- Precompiled binaries (no build required) ---

# kustomize v5.6.0
RUN curl -sSfL "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv5.6.0/kustomize_v5.6.0_linux_amd64.tar.gz" \
    | tar xz -C ${DEVTOOLS_BIN} kustomize && \
    mv ${DEVTOOLS_BIN}/kustomize ${DEVTOOLS_BIN}/kustomize-v5.6.0 && \
    ln -s kustomize-v5.6.0 ${DEVTOOLS_BIN}/kustomize

# --- Built from source (no precompiled binaries available, or precompiled binary
#     incompatible with the project's Go version) ---

# golangci-lint v2.1.6 (built from source so the binary uses Go 1.24, matching go.mod;
#   v2.x required because .golangci.yml uses the v2 config format;
#   module path includes /v2 per Go module major version conventions)
RUN GOBIN=${DEVTOOLS_BIN} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6 && \
    mv ${DEVTOOLS_BIN}/golangci-lint ${DEVTOOLS_BIN}/golangci-lint-v2.1.6 && \
    ln -s golangci-lint-v2.1.6 ${DEVTOOLS_BIN}/golangci-lint

# controller-gen v0.18.0
RUN GOBIN=${DEVTOOLS_BIN} go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.18.0 && \
    mv ${DEVTOOLS_BIN}/controller-gen ${DEVTOOLS_BIN}/controller-gen-v0.18.0 && \
    ln -s controller-gen-v0.18.0 ${DEVTOOLS_BIN}/controller-gen

# helmify v0.4.19
RUN GOBIN=${DEVTOOLS_BIN} go install github.com/arttor/helmify/cmd/helmify@v0.4.19 && \
    mv ${DEVTOOLS_BIN}/helmify ${DEVTOOLS_BIN}/helmify-v0.4.19 && \
    ln -s helmify-v0.4.19 ${DEVTOOLS_BIN}/helmify

# setup-envtest (release-0.21, matching controller-runtime v0.21.x)
# Also pre-downloads Kubernetes test binaries for K8s v1.33 into DEVTOOLS_BIN
RUN GOBIN=${DEVTOOLS_BIN} go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.21 && \
    mv ${DEVTOOLS_BIN}/setup-envtest ${DEVTOOLS_BIN}/setup-envtest-release-0.21 && \
    ln -s setup-envtest-release-0.21 ${DEVTOOLS_BIN}/setup-envtest && \
    ${DEVTOOLS_BIN}/setup-envtest use 1.33 --bin-dir ${DEVTOOLS_BIN} -p path

# Pre-create cache directories with world-writable permissions so that named
# volumes mounted here are initialized with the correct mode. Without this,
# Docker initializes the volumes as root:root 755, which breaks non-root users
# (e.g. GitHub Actions runners using --user UID:GID with real Docker).
RUN mkdir -p /tmp/.cache/go-build /tmp/.cache/golangci-lint && \
    chmod -R 777 /tmp/.cache /go/pkg/mod

# Mark /workspace as safe for git to avoid "dubious ownership" errors on bind mounts
RUN git config --global --add safe.directory /workspace

WORKDIR /workspace
