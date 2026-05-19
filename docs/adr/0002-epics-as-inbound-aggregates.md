# Epics are inbound aggregates

**Status:** accepted (2026-05-14)

Epic ↔ story/feature membership is expressed by **stories and features carrying an `epic: epics:<id>` field**, not by the epic carrying `related_stories:` / `related_features:` lists. `catalog/epics.yaml` holds only an epic's metadata (id, name, summary, outcome, status, sponsor_personas, source, tags). The renderer assembles the "advances these stories" view from reverse refs.

## Why

Without this inversion, epics fail the "items must be referenced from multiple other files" rule for being their own catalog file ([ADR-0003](0003-catalog-schema-in-index-yaml.md) and the test for file-worthiness in `catalog/README.md`). Outbound `related_*` lists also cause silent drift: a new story added under a category but missed in the epic's membership list never shows up in the epic view. Inbound `epic:` fields make membership a single-writer fact owned by the story/feature, not a duplicated list to be maintained on both sides.

## Consequences

- **Aggregates use inbound refs; junctions stay outbound.** A story's `persona: [...]` is a junction (many-to-many, neither side owns) and stays outbound. An epic *containing* its stories is an aggregate (each story in at most one epic) and uses inbound.
- **Data migration required.** The 10 existing epics still carry the old `related_features:` / `related_stories:` fields — 20 schema violations surfaced by `catalog-curator` validation. The migration is curator-class work: remove the two fields from every epic, then add `epic:` to each story/feature that was listed. ~100 inverse refs.
- The epic-cataloger never edits stories or features; it relies on the curator to verify membership coverage during refresh.

See `catalog/epics.yaml`, `~/.claude/agents/epic-cataloger.md`, `catalog/index.yaml > files > epics > fields`.
