---
sidebar_position: 3
---

## Architecture

At a high level:

- **CRDs**: you declare Aruba Cloud resources as Kubernetes objects (`apiVersion: arubacloud.com/v1alpha1`).
- **Controller / Reconciler**: watches those CRDs and continuously drives the remote Aruba Cloud state to match `spec`.
- **References**: resources relate to each other via `spec.*Reference(s)` fields (`name` + `namespace`).
- **Status**: reconciliation progress is surfaced via `.status.phase`, `.status.resourceID`, and `.status.conditions`.

