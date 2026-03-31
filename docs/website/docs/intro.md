---
sidebar_position: 1
---

## Overview

The **Arubacloud Resource Operator** is a Kubernetes operator that enables declarative management of Aruba Cloud resources through Kubernetes Custom Resources. This operator allows you to provision and manage Aruba Cloud infrastructure using familiar Kubernetes tools and workflows.

### What you manage

The operator installs CRDs in the API group `arubacloud.com` (version `v1alpha1`) and reconciles them against Aruba Cloud.

### Tenancy modes

You can run the operator in:

- **Single-tenant mode**: the operator provisions Aruba Cloud resources for a **specific ARU Tenant**.
- **Multi-tenant mode**: the operator can provision and manage Aruba Cloud resources across **multiple ARU Tenants**.

### Next steps

- Read the [Architecture](./architecture)
- Follow [Installation](./installation)
- Start from the CRDs [Introduction](./crds)

