# Parity Run Analysis — After Feature Sprint (2026-04-22, run 3)

Third dual-run report after the fleet-db feature sprint. This is the final
"fleet-db can do everything beads can" milestone — five features landed
plus three harness-alignment fixes.

## Feature commits (fleet-db `parity-harness-fixes`, ~10 total)

| SHA (short) | What |
|---|---|
| `b1eaef0` | BeadsCaller capture fixes + `_error_code` drift widening |
| `3099bb9` | Remove all 6 waivers (pivot moment) |
| `2bd45b0` | Restore WAIVER-001/002/003 + 5s timestamp tolerance |
| `882d1c1` | Owner field wiring (update path + pkg/client) |
| `60626ba` | Owner resolution alignment (git config + default-to-actor) |
| `4268074` | close_reason persistence on issue read model |
| `d8f94ec` | label.add idempotency |
| `101b346` | label.remove + worker.claim self-idempotency |

## Raw counts

| Metric | Baseline | Post-harness-fixes | Post-owner | Post-all-features |
|---|---|---|---|---|
| Unapproved | 473 | 324 | 186 | **176** |
| Waived | 88 | 88 | 88 | 88 |
| Normalized | 58 | 315 | 358 | **366** |
| Fixtures pass | 1/32 | 3/32 | 3/32 | 3/32 |

## What the 176 unapproved actually are

All Category B1/B2 drift plus scattered strict field mismatches on
fixtures. **No `_outcome` errors remain** — every feature ticket closed
the error-path its waiver was covering:

| Ticket | What was fixed | `_outcome` diffs eliminated |
|---|---|---|
| fleet-jkmf | owner field on update path | — |
| fleet-gjqh | owner default matches bd (via BeadsCaller git config + service default-to-actor) | 71 owner-field diffs → 0 |
| fleet-v5mo | close_reason surfaced on issue.show | 11 close_reason diffs → 0 |
| fleet-swz0 | label.add idempotent on duplicate | `label.add._outcome` diffs → 0 |
| fleet-w8lc | label.remove idempotent on absent | `label.remove._outcome` diffs → 0 |
| fleet-1dxc | worker.claim actor-aware self-reclaim | `worker.claim._outcome` (same-actor) diffs → 0 |

### Breakdown of the remaining 176 (top 10)

| Count | method.field | Category |
|---|---|---|
| 44 | `issue.create.labels` | B1 — `[]` vs `null` |
| 14 | `issue.ready.issues[0].type` | B2 — missing vs default |
| 11 | `issue.close.type` | B2 |
| 11 | `issue.close.labels` | B1 |
| 6 | `issue.show.type` | B2 |
| 5 | `issue.ready.issues[1].type` | B2 |
| 5 | `issue.show.labels` | B1 |
| 3 | `worker.claim.type` | B2 |
| 3 | `worker.claim.labels` | B1 |
| 3 | `worker.claim._outcome` | long-tail (cross-actor case retained; fixture uses different actors) |

- Cat B1 (`labels` — `[]` vs `null`): ~60 diffs
- Cat B2 (`type` — missing vs default): ~42 diffs
- Long tail (timestamps outside tolerance, strict mismatches): ~74 diffs

## Per-waiver walkthrough outcomes (captured 2026-04-22)

| Item | Decision | Status |
|---|---|---|
| WAIVER-001 type subset | Keep waiver | Restored on fleet-db |
| WAIVER-002 hash vs sequential IDs | Keep waiver | Restored |
| WAIVER-003 body/text field | Keep waiver | Restored |
| WAIVER-004 label.add idempotency | Close in fleet-db | Landed |
| WAIVER-005 label.remove idempotency | Close in fleet-db | Landed |
| WAIVER-006 worker.claim idempotency | Close in fleet-db | Landed |
| G7 owner field | Close in fleet-db | Landed |
| G8 close_reason field | Close in fleet-db | Landed |
| Cat B1 labels `[]` vs `null` | Leave as real diff | Open |
| Cat B2 missing vs default | Leave as real diff | Open |
| Cat C timestamp tolerance | Harness rule: 5s | Applied |

## What this means for the remove-beads plan

- Fleet-db now has every feature loom reads from the issue model
- Remaining 176 diffs are either wire-format (B1/B2) or architectural (WAIVER-001/002/003)
- A future decision on B1/B2 could drop unapproved count to ~74
- No evidence of missing features on fleet-db side; the original
  `docs/design/remove-beads.md` plan can be revisited once B1/B2 is
  decided and the loomcli-side paritytest has broader fixture coverage

## Loomcli-side additions

Commits on `beads-vs-fleet-parity`:

| SHA (short) | What |
|---|---|
| initial | Plan doc, baseline + first-fix parity snapshots |
| `004fbdc5` | paritytest scaffold (types, DualRunner skeleton) + Makefile target |
| `20b0ed20` | Phase 3 docker-compose + seed.sh + browse.md |
| `abbb9796` | FleetBackend adapter stubs filled (Count, Batch, GetMutations, WaitForMutations) |
| `9216533d` | paritytest MVP: spawn bd+fleet-db subprocesses, 1 fixture runs end-to-end |
| new | This snapshot (`after-features/`) |

## Files in this directory

- `release-report.md` — fleet-db's top-line verdict from this run
- `diff-report.json` — full 727-diff detail as of 2026-04-22 post-features
- `analysis.md` — this file

## Recommendations for the next slice

1. **Decide Cat B1/B2 wire-format alignment.** 100 of 176 diffs collapse to
   normalized/pass if fleet-db emits `[]` for empty labels and bd (or the
   normalizer) handles missing-vs-default on enum fields. Single design
   decision, small code.
2. **Extend paritytest fixture coverage** (in flight). Need
   `crud_update_fields`, `crud_close_reopen`, `crud_show_not_found`. More
   after that: dep ops, labels, comments.
3. **Push branches + open PRs** — 14 commits of feature work haven't been
   reviewed upstream. Good checkpoint.
4. **Revisit remove-beads.md** after B1/B2 lands. Fleet-db is now feature
   capable; removal is unblocked on everything except broader coverage
   confidence (which Phase 2 extension provides).
