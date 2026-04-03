# Documentation

This directory contains the documentation website for **arubacloud-resource-operator**.

The docs are built with [Docusaurus](https://docusaurus.io/) and published to GitHub Pages via GitHub Actions.

## Development

### Prerequisites

- Node.js 18+ and npm

### Local Development

Using Make (recommended):

```bash
make docs-install
make docs
```

Or using npm directly:

```bash
cd docs/website
npm install
npm start
```

Open `http://localhost:3000` in your browser.

### Italian locale

```bash
make docs-serve-it
```

## Versioning

The documentation supports versioning. A dedicated GitHub Actions workflow creates versioned docs when a GitHub Release is published (or manually via workflow dispatch).

When you add a new version, Italian content for that version is automatically synced from the current Italian docs (see `docs/website/scripts/sync-version-translations.js`).

