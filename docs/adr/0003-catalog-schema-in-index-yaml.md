# Catalog item schema lives in `index.yaml`

**Status:** accepted (2026-05-14)

`catalog/index.yaml` is the single source of truth for catalog item schema. Each entry under `files:` declares its item schema in a `fields:` block — field name, type, required flag, and one-line description. Cataloger agent prompts no longer duplicate schema inline; they reference `index.yaml > files > <type> > fields`. The renderer and the `catalog-curator` both read this manifest.

## Why

Before this, the schema for a "feature" was repeated in `feature-cataloger.md`, in the renderer's reading code, and implicitly in every consumer. Adding a field (e.g. `epic:` per [ADR-0002](0002-epics-as-inbound-aggregates.md)) meant updating three places and risking drift. Schema-in-manifest collapses the duplication.

## Considered options

- **Full JSON Schema files in `catalog/schemas/`** — rejected as v1 overkill. The catalog has ~5 entity types and ~5–12 fields each. The compact `{type, required, desc}` flow-mapping form covers ~95% of what's needed. If real validation (regex patterns, custom predicates) becomes necessary, JSON Schemas can be added alongside without breaking the v2 manifest.
- **Status quo** (schema in each agent prompt) — rejected. Drift is guaranteed at scale.

## Consequences

- The type vocabulary is fixed and documented in `index.yaml > field_types`: `string`, `int`, `bool`, `date`, `enum`, `list<T>`, `ref<file-id>`.
- The `catalog-curator` gains a schema-validation responsibility — see ["Process for schema validation"](../../catalog/../../.claude/agents/catalog-curator.md) (also captured in the curator's prompt).
- Cataloger prompts retain a "file structure" block (the `meta:`/`categories:` wrapper) but no longer redeclare items. They defer to the manifest.

See `catalog/index.yaml`, `~/.claude/agents/catalog-curator.md`.
