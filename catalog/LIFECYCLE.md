# Catalog item lifecycle

Every catalog item has an implicit `lifecycle: active`. When the thing it describes is no longer in active use, the owner cataloger transitions it to `deprecated` or `deleted`. The item stays in the catalog file either way — the difference is in how it's surfaced and how the curator reasons about refs to it.

## States

| State | Meaning | Renderer | Refs to it |
|---|---|---|---|
| `active` (default; field may be omitted) | The thing exists and is supported. | Normal rendering. | Resolve normally. |
| `deprecated` | The thing still works, but it's discouraged and refs should migrate. | Dashed border, "· deprecated" badge, slight opacity. | Resolve with a yellow warning style. Click-through still works. |
| `deleted` | The thing is gone. The catalog entry remains only so the curator can distinguish intentional deletion from a typo'd ref. | Hidden by default. Toggle "Show deleted" in the topbar to reveal. Strike-through + red badge. | Resolve, but render struck-through and red. The curator's validation reports them as "broken ref to deleted target" — distinct from "broken ref to unknown target". |

## Procedure

### Deprecating

The owner cataloger of the item being deprecated does this in a single edit:

1. Set `lifecycle: deprecated`.
2. Set `lifecycle_at: <ISO date>`. The curator's schema check fails if this is missing.
3. Optionally set `lifecycle_reason: "<one sentence>"` — strongly encouraged because future readers will ask.
4. Don't touch sibling files. Refs to this item continue to resolve and the renderer marks them yellow. The curator's audit surfaces them so owners can migrate at their pace.

### Deleting

Same shape:

1. Set `lifecycle: deleted`.
2. Set `lifecycle_at: <ISO date>`.
3. Strongly recommended: `lifecycle_reason:`. Without it, a future reader (or LLM agent) can't tell *why* the deletion happened.
4. Don't touch sibling files. Refs continue to resolve so the curator can produce actionable reports.

### Hard purging

After deletion, the catalog-curator periodically reports items where `lifecycle: deleted` AND no other catalog file has any ref to the item AND `lifecycle_at` is more than some agreed time ago (default: 90 days). The owner cataloger may then hard-remove the item from its file. The curator's report lists candidates; the owners hard-purge on their next run.

## Reanimation

An item can transition from `deprecated` back to `active` (you decided it's worth keeping). Set `lifecycle: active` (or remove the field entirely) and clear `lifecycle_at:` / `lifecycle_reason:`. Document why in the commit message.

Going `deleted → active` is allowed but strongly discouraged — it means you killed something prematurely. Prefer creating a new item with a fresh ID if the previous concept is being revived in a different shape.

## Why two states (deprecated and deleted)?

Real software has two distinct moments:

- "This still works but stop using it" (deprecated) — give consumers time to migrate.
- "This is gone" (deleted) — but don't lie about whether it ever existed, because broken refs are evidence of unfinished migration work.

Collapsing both into a single `deleted` state would force you to choose between "broken in renderer immediately" (bad UX) or "broken in renderer eventually" (no migration window). Two states give a graceful path.

## Relationship to renames (RENAMES.md)

`previous_ids:` (Q6) handles "this concept moved" — the same idea kept its identity through a name change. Refs to the old name still resolve to the same concept.

`lifecycle: deleted` (Q7) handles "this concept ended" — the idea is gone, not relocated. Refs to it indicate work that needs attention.

If something simultaneously moved AND is being killed, you usually want a rename followed by a delete — but most often it's one or the other.

## Validation rules the curator enforces

- `lifecycle: deprecated|deleted` requires `lifecycle_at:`. Missing date = schema violation.
- `lifecycle: active` (or absence) must not have `lifecycle_at:` or `lifecycle_reason:`. Stale lifecycle metadata = schema violation.
- A broken ref to a `lifecycle: deleted` target is reported in a separate bucket from a broken ref to a nonexistent target. Distinct action items.
- `lifecycle_reason:` is not required but is strongly recommended; the curator's report flags deleted/deprecated items without a reason.
