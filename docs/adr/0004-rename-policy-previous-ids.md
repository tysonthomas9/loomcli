# Rename policy: `previous_ids:` with curator cleanup

**Status:** accepted (2026-05-14)

When a catalog item's `id:` changes, the owner cataloger moves the old ID into a `previous_ids: [...]` field on the same item. The resolver checks canonical IDs first, then falls back to `previous_ids`, so existing refs continue resolving. The `catalog-curator` periodically rewrites stale refs to point at canonical IDs (a curator-class cross-file write) and regenerates `catalog/.aliases.yaml` so external consumers (e.g. the wireframe's `REGION_MAP`) can follow redirects. Once a prior ID has zero remaining catalog refs, the owner cataloger removes it.

## Why not the alternatives

- **Strict permanence** (never rename, only deprecate-and-recreate) — accumulates ghosts and adds friction to legitimate clean-ups. The "epic vs theme" discussion (see [ADR-0006](0006-epic-naming-collision.md)) showed we *do* sometimes get IDs wrong.
- **Free rename + auto-migrate at rename time** — violates the per-file ownership contract from [ADR-0001](0001-catalog-ownership-enforcement.md) because the cataloger doing the rename would have to write sibling files. External consumers can't be migrated atomically anyway, so a redirect map is required regardless.

## Consequences

- `aliases:` (semantic synonyms — alt names a user might type, e.g. `mon` for `loom monitor`) is **distinct** from `previous_ids:` (rename history). The schema descriptions in `index.yaml` call this out, and the glossary-cataloger and feature-cataloger prompts both warn against conflating them.
- The curator's "alias-cleanup" mode fails loudly on prior-ID collisions (the same prior ID claimed by two items).
- Reverse renames (A → B → A) are forbidden; if revival is needed, pick a fresh ID.

See `catalog/RENAMES.md`, `catalog/.aliases.yaml`, `~/.claude/agents/catalog-curator.md > "Process for alias cleanup"`.
