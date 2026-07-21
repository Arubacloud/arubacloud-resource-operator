---
sidebar_position: 1
---

# Overview

The **Aruba Cloud Resource Operator** is a Kubernetes operator that enables declarative management of Aruba Cloud infrastructure through Kubernetes Custom Resources (CRDs). Instead of provisioning servers, networks, and storage through the Aruba Cloud console or API directly, you describe the desired infrastructure in YAML files and apply them to your Kubernetes cluster. The operator continuously reconciles the declared state with the live Aruba Cloud environment, handling creation, updates, and deletion automatically.

## How It Works

The operator installs a set of CRDs in the API group `arubacloud.com/v1alpha1`. Each CRD maps to an Aruba Cloud resource type (Project, VPC, CloudServer, etc.). When you create, update, or delete one of these objects in Kubernetes, the operator detects the change and calls the Aruba Cloud API to reflect it. Progress is reported back through `status.phase`, `status.resourceID`, and `status.conditions`.

Resources are organized in a **parent–child hierarchy** — for example, a VPC belongs to a Project, and a Subnet belongs to a VPC. The operator manages dependencies automatically: it will not create a child until its parent is provisioned, and it will cascade-delete children when a parent is removed. See the [Architecture](./architecture) page for the full ownership diagram and lifecycle details.

## Tenancy Modes

- **Single-tenant mode**: all resources are provisioned under a single Aruba Cloud tenant. Set `spec.tenant` on each resource to the same ARU tenant identifier.
- **Multi-tenant mode**: the operator manages resources across multiple Aruba Cloud tenants. Each resource can reference a different `spec.tenant`, and the operator resolves credentials per-tenant (optionally via HashiCorp Vault).

## Key Concepts

| Concept | Description |
|---------|-------------|
| **CRD** | A Kubernetes custom resource definition mapping to an Aruba Cloud resource type |
| **Phase** | The current lifecycle state of a resource (`Pending`, `Creating`, `Active`, `Failed`, etc.) |
| **Reference** | A `name`/`namespace` pointer from one CRD to another (e.g., `spec.projectReference`) |
| **ResourceID** | The Aruba Cloud-side identifier assigned after the resource is created, stored in `status.resourceID` |
| **Reconciliation** | The operator's continuous loop that compares declared spec with live cloud state and drives them to match |

## Next Steps

- Read the [Architecture](./architecture) — resource ownership diagram, lifecycle phases, cascade deletion
- Follow the [Installation](./installation) guide to deploy the operator
- Browse the [CRDs](./crds) — available resource types, creation ordering, common patterns
- Walk through an [end-to-end example](./examples)
