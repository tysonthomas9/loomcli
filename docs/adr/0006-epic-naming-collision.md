# "Epic" naming: catalog wins, loom-product gets `epic-issue`

**Status:** accepted (2026-05-14)

The word "epic" is used in two distinct ways in this repo: the **catalog** uses it for strategic themes (Lead-Driven Orchestration, Distributed Control Plane, Web Onboarding) — bodies of work with outcomes that ship over months. **Loom-the-product** uses it for a specific data structure — a parent issue in fleet-db with child tasks, drained by `loom epic run`. We keep "epic" as the canonical industry-standard concept (catalog's use) and introduce **`epic-issue`** in the glossary for loom-product's implementation.

## Why catalog won the name

The catalog items behave exactly like the agile concept of an epic — they ship, decompose, and have sponsor personas. Renaming them to "themes" was the first instinct and was wrong: themes don't ship, and these clearly do. The loom-product's "epic" is one specific *implementation* of the epic concept in an issue tracker — a narrower use that fits as a sub-term.

## Considered options

- **Rename `catalog/epics.yaml` → `themes.yaml`** — rejected. "Theme" implies an evergreen organizing principle, not a body of work with a shipping state.
- **Two unrelated glossary entries (`epic-theme`, `epic-issue`)** — rejected. Every reader who searches "epic" would have to decode context. AI-trap of overloaded terms.

## Consequences

- `catalog/glossary.yaml` carries both entries; `epic` and `epic-issue` are linked via `related:` for discoverability.
- Stories/features point at their catalog epic via the `epic: epics:<id>` field per [ADR-0002](0002-epics-as-inbound-aggregates.md). This reads cleanly under the chosen naming.
- The wireframe's "Talk to Lead" region maps to `features:loom-epic-run` (the implementation) and to `epics:lead-driven-orchestration` (the catalog concept) — the disambiguation is by file prefix.

See `catalog/glossary.yaml` entries `epic` and `epic-issue`.
