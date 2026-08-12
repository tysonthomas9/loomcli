# Migration Design Documents

This directory contains implementation-oriented migration plans for changes that span multiple Loom subsystems or must preserve compatibility across several releases.

Migration documents complement the decision records under `docs/design/`:

- design records explain the product or architecture decision;
- migration folders define sequencing, compatibility seams, validation, and removal gates.

## Current migration proposals

| Migration | Status | Scope |
|---|---|---|
| [Modular monolith](modular-monolith/README.md) | Proposed | Reorganize `loom serve` around capability ownership without adding services, Go modules, or feature packages |
