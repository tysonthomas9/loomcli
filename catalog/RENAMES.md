# Catalog rename policy

When a catalog item's identity needs to change — `solo-dev` → `solo-dev-local`, `recover-stuck-agent` → `release-stuck-agent`, etc. — use the **alias** path. Never bulk-rewrite refs at rename time.

## The rule

Renaming is allowed. The procedure has three steps and one cleanup pass.

### When you rename

The owner cataloger of the item being renamed (and only that cataloger) does this in a single edit:

1. Set the new canonical `id:`.
2. Move the prior ID into `previous_ids: [<prior>]`. If the item has been renamed before, the list grows: `previous_ids: [<oldest>, <middle>, <prior>]`.
3. Touch nothing else. **Do not** rewrite refs in sibling files at rename time — that's a curator job, done lazily later.

The resolver in `catalog/index.html`, `wireframe.html`, and the `catalog-curator` falls back to `previous_ids` when the canonical lookup misses, so all existing refs keep resolving immediately.

### When the curator cleans up

On a `catalog-curator` cleanup pass (run manually or as part of `refresh`):

1. For every item with a `previous_ids:` entry, scan all catalog files for refs that still use a prior ID.
2. If refs to prior IDs exist anywhere in the catalog: rewrite them to point at the canonical ID. This is a curator-class cross-file write.
3. After the rewrite, regenerate `catalog/.aliases.yaml` to mirror the current `previous_ids` state.
4. **Once a prior ID has zero remaining refs in the catalog**, the owner cataloger may remove it from `previous_ids:` on its next run. The curator's report should flag prior IDs that are safe to remove.

External consumers (wireframe `REGION_MAP`, scripts, future API clients) read `catalog/.aliases.yaml` to follow redirects. Until the curator removes a prior ID from that file, external refs continue to resolve.

## Rules

- **Unique aliases.** A prior ID may appear in `previous_ids:` on exactly one item across the entire catalog. Two items can't both claim to be the canonical of the same prior ID. The curator's validation fails loudly on collisions.
- **No reverse renames.** Once `A` has been renamed to `B` (so `B.previous_ids: [A]`), don't rename it back to `A`. The history is one-way. If you really need to revert, pick a new canonical ID and move both `A` and `B` into `previous_ids`.
- **Renames don't change the schema.** A renamed item must still satisfy every required field for its type. Renaming is identity-only.

## Renaming concepts vs renaming items

If you find yourself renaming an item to *change what it means* — not just to clean up its name — that's a different operation. Don't use `previous_ids:` to redirect to a semantically different item. Instead:

- Mark the old item as deprecated (or delete it) and create a new item with a fresh identity.
- The catalog-curator will surface broken refs to the old ID; their referrers (stories, epics, etc.) should update by hand to either the new item or a different one, depending on what they really meant.

`previous_ids:` is for stable concepts that got a better name. It is not a generic "redirect" mechanism.

## Why not just edit refs in place

Two reasons:

1. **Cross-file writes violate the ownership contract.** A cataloger that renames an item shouldn't reach into stories.yaml to fix refs — that's the user-story-cataloger's file. Aliases let the rename happen as a single-file edit.
2. **External consumers can't see the catalog yaml directly.** The wireframe's `REGION_MAP` lives in HTML; future API clients might be scripts on someone else's machine. `catalog/.aliases.yaml` is the single document they all watch.
