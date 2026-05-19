# Catalog item lifecycle: active / deprecated / deleted

**Status:** accepted (2026-05-14)

Every catalog item has an implicit `lifecycle: active`. When the thing it describes is no longer in active use, the owner cataloger transitions to `lifecycle: deprecated` (still works but discouraged) or `lifecycle: deleted` (gone). The item stays in the file in both cases — never hard-deleted at the moment of transition. `lifecycle_at: <date>` is required when `lifecycle != active`; `lifecycle_reason:` is strongly recommended.

## Why tombstone instead of hard delete

The deciding question: can the `catalog-curator` distinguish "the target was intentionally deleted" from "the referrer has a typo"? With hard delete, both cases look identical — "broken ref to unknown target" — and the curator's report is non-actionable. With tombstones, the curator surfaces three distinct buckets: refs to unknown (typos), refs to deprecated (migration debt), refs to deleted (referrer needs revision). Each bucket points at a different owner with a different action.

## Considered options

- **Two states (active / gone)** — rejected. Real software has both "stop using" (with a migration window) and "this is gone" (with broken refs as evidence of unfinished work). Collapsing them loses the migration window.
- **Combine with rename via a `replaced_by:` field** — rejected. That duplicates `previous_ids:` from [ADR-0004](0004-rename-policy-previous-ids.md). Renames handle "concept moved"; lifecycle handles "concept ended." Two mechanisms for two different things.

## Consequences

- The renderer hides `lifecycle: deleted` items by default; a "Show deleted" toggle in the topbar reveals them.
- Refs that resolve to deprecated or deleted targets render with a target-`lifecycle` style (yellow border / red strikethrough) and a tooltip explaining the state.
- The curator's schema validation enforces `lifecycle != active → lifecycle_at required` and reports `lifecycle: deleted` items lacking a `lifecycle_reason:` as advisory.
- Hard purge happens by the owner cataloger only after the curator reports an item with zero remaining refs and `lifecycle_at` older than 90 days.
- An epic's `status:` (planned / in-progress / shipped / alpha / paused) is independent of `lifecycle:`. A shipped epic stays `lifecycle: active`. A shelved epic is `status: paused` with `lifecycle: active`. Only an epic that's no longer pursued at all is `lifecycle: deleted`.

See `catalog/LIFECYCLE.md`.
