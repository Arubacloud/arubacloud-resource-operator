---
title: Changelog
sidebar_position: 12
---

# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).
Versions follow a `MAJOR.MINOR.PATCH` scheme; MINOR increments on feature releases and PATCH on fixes.

## [Unreleased]

## [1.1.2] - 2026-07-27

### Added

- **Vault KV prefix** (`config.auth.multi.vault.kvPrefix`) — an optional path prefix prepended to the tenant in the Vault KV mount, enabling a single KV backend to serve multiple environments: `<kv-mount>/<kv-prefix>/<tenant>`; sdk-go bumped to v1.0.8.

## [1.1.1] - 2026-07-22

### Fixed

- Corrected the OAuth2 token issuer URL used for Keycloak authentication (#50); sdk-go bumped to v1.0.7.
- Repaired the end-to-end test suite after the sdk-go v1.0.4 migration (#49).

## [1.1.0] - 2026-07-20

### Changed

- Bumped sdk-go from v0.1.24 to v1.0.4 to track the stable SDK release line.

## [1.0.0] - 2026-04-03

### Added

- **All 9 CRD controllers** — `Project`, `VPC`, `Subnet`, `SecurityGroup`, `SecurityRule`, `KeyPair`, `ElasticIP`, `BlockStorage`, `CloudServer`; each managed through a standard `Pending → Creating → Active → Deleting → Deleted` lifecycle with `Updating` and `Failed` branches.
- **Generic state-machine reconciliation framework** (`internal/reconciler/`) — ordered `TransitionSet` evaluated per reconciliation cycle; each `AbstractTransition` declares independent K8s and CMP conditions, actions, and requeue strategies.
- **Transitory-phase timeout** — resources stuck in `Creating`, `Updating`, or `Deleting` for longer than 10 minutes automatically move to `Failed`; a `ValidationFailedAndDeleting` escape hatch lets stuck resources still be deleted.
- **Cross-resource consistency validation** — two-tier engine (`ivs` for K8s-only intent before CMP calls, `vs` for post-creation CMP drift) enforces `tenant`, `project`, `region`, `zone`, and `vpcReference` consistency across all referenced resources.
- **Cascade delete** — custom two-layer ownership model (annotation + label) replaces standard Kubernetes OwnerReferences, enabling cross-namespace parent–child cascade; parent deletion waits for all children to reach `Deleted` before calling the CMP delete API.
- **Multi-tenant credential support** — per-tenant Aruba Cloud clients resolved at reconcile time; credentials fetched from HashiCorp Vault AppRole (`kv-mount/tenant`).
- **Immutable fields enforcement** — `spec.tenant` changes are blocked; `SecurityRule` spec updates are blocked (the CMP has no update API for security rules).
- **Reconciliation metrics** — `aruba_reconcile_step_duration_seconds` Prometheus histogram exposed on `:9080/metrics`; labelled by `resource_kind`, `result`, `phase`, and `reason`.
- **Structured JSON logging** — logr backed by `log/slog`; each controller enriches the logger with the `tenant` field for per-tenant log correlation.
- **Helm chart** — single-tenant (direct credentials) and multi-tenant (Vault AppRole) installation modes; CRDs installable as a standalone sub-chart.
- **Docusaurus documentation site** (EN + IT) — covers all 9 CRDs with spec fields, status fields, lifecycle, kubectl quick reference, and installation guide.
- **Containerised development environment** — devtools image for running `make lint` and `make test` targets without a local Go installation.
- **GitHub Actions CI** — lint, test, and release pipeline using the devtools image.

### Fixed

- Workaround for network API filter issue in the CMP (DEV-66643).
- Workaround for CloudServer creation failures when Subnet and BlockStorage are not yet ready.
- Missing `location` field on Subnet creation requests.
- Nil dereference panics in Project and BlockStorage update reconciliation.
- Status conflict errors during concurrent updates resolved with `retry.RetryOnConflict`.

[Unreleased]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.2...HEAD
[1.1.2]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.1...v1.1.2
[1.1.1]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.1.0...v1.1.1
[1.1.0]: https://github.com/Arubacloud/arubacloud-resource-operator/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/Arubacloud/arubacloud-resource-operator/releases/tag/v1.0.0
