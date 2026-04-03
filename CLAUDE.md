# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Context loading

Read the following files from `ai/` based on the task at hand:

- **Always read** `ai/ARCHITECTURE.md` and `ai/REPO.md` before modifying any code under `internal/`, `api/`, or `cmd/`.
- **Read** `ai/CONVENTIONS.md` before writing or reviewing Go code — it documents naming, error handling, testing, and import conventions.
- **Read** `ai/DEVEX.md` when you need build, test, lint, or manual-testing commands.
- **Read** `ai/KNOWN_ISSUES.md` when working on deletion flows, OwnerReferences, or cascade delete — it documents edge cases and open problems.
- **Read** `ai/DOCS.md` when your changes affect CRD types (`api/v1alpha1/`), user-facing behaviour, installation, or configuration — it maps code changes to documentation files and describes the docs structure and conventions.
- **Read** `ai/SKILLS.md` when asked to plan a new or refactored controller (e.g. `/plan new controller for <Resource>`).
- **Read** `ai/templates/plans/NEW_CONTROLLER.md` only when executing the `/plan` skill.

For trivial tasks (typo fixes, config-only changes) you do not need to read the full `ai/` directory — use your judgement. For documentation edits, read `ai/DOCS.md`.
