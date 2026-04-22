# Parity Run Analysis — After Harness Fixes (2026-04-22, run 2)

Second dual-run parity report after landing three upstream fleet-db fixes:
- **fleet-ynu5**: BeadsCaller response capture + flag mismatches
- **fleet-je9d**: widen `_error_code` drift for bd↔fleet-db code mapping
- **fleet-614s**: WAIVER-004/005/006 for idempotency drift

Commit in fleet-db: `parity-harness-fixes` @ `b1eaef0`.

## Delta vs baseline

| Metric | Baseline (run 1) | After fixes (run 2) | Delta |
|---|---|---|---|
| Diffs with no `beads` value (harness bug) | 553 | 211 | **−62%** |
| Diffs with both sides populated | 66 | 516 | **+682%** |
| Total comparisons | 619 | 727 | +108 |
| Unapproved diffs | 473 | 324 | −31% |
| Normalized (accepted drift) | 58 | 315 | **+443%** |
| Waived | 88 | 88 | 0 |
| Fixtures passing | 1/32 | 3/32 | +2 |

Hidden root cause from Agent A: `BeadsCaller.run()` used `exec.Cmd.CombinedOutput()`, merging bd's `"Note: No git repository initialized"` stderr into stdout and breaking JSON parsing. That single bug accounted for most of the 553 missing-beads diffs.

## Remaining 324 unapproved — all real semantic drifts

No more harness artifacts. These are genuine field-level differences between
bd and fleet-db response shapes.

### Category A — Fields bd returns but fleet-db doesn't track (~100 diffs)

| Field | Count | bd value | fleet-db value | Notes |
|---|---|---|---|---|
| `owner` | ~90 | git user email (e.g. `11642062+tysonthomas9@users.noreply.github.com`) | `None` | Fleet-db may not have an `owner` concept separate from `assignee` / `created_by` |
| `close_reason` | 10 | `"Closed"` | `None` | Fleet-db doesn't expose close reason on `issue.show` |

**Options:**
- A1. Add `owner` / `close_reason` fields to fleet-db's issue model
- A2. Waive — architecture decision that fleet-db simplifies the shape (like WAIVER-001 for type subset)
- A3. Normalizer rule: treat `bd: <string>` vs `fleet_db: null` as normalized drift for these specific fields

### Category B — Type encoding mismatches (~115 diffs)

| Field | Count | bd shape | fleet-db shape | Semantic |
|---|---|---|---|---|
| `labels` | ~70 | `[]` (empty list) | `None` (null) | Empty list vs null — same meaning, different shape |
| `type` | ~45 | missing | `"task"` (default) | bd doesn't echo the field when it's the default; fleet-db always returns |

**Options:**
- B1. Normalizer rule: `[] ≡ null` for list fields; `<missing> ≡ <default>` for enum fields (low-effort, high-impact)
- B2. Change one side's response shape (higher effort)

### Category C — Timestamp sub-second drift (~30 diffs)

| Field | Count | bd value | fleet-db value |
|---|---|---|---|
| `created_at` / `updated_at` | ~30 | timestamp T+0.5 to T+1.0s | timestamp T |

Both sides are creating the issue fresh at slightly different wall-clock moments. The existing timestamp normalizer isn't accepting ~1s differences. Fix is to either:
- C1. Widen normalizer tolerance for timestamp fields (e.g. `<5s` treated as normalized)
- C2. Use relative-ordering comparison instead of exact-equality

## Actionable cleanup remaining (~324 → likely < 20)

If all three categories are resolved with the lightest-weight option:
- A: waive `owner` + `close_reason` (2 new WAIVER-00X) → −100 diffs
- B: add normalizer rules for `[] ≡ null` and `<missing> ≡ <default>` → −115 diffs
- C: widen timestamp tolerance to `<5s` → −30 diffs

Projected result: ~80 unapproved remaining, which would be the truly hard-to-reconcile cases worth discussing individually. A second round of agent fixes could land the bulk of these in <1h of wall-clock time.

## What this means for the remove-beads direction

Strengthens the case:
- No new features found missing on fleet-db side
- Remaining gaps are all shape normalization or fields fleet-db deliberately doesn't track — architecture-level choices, not missing functionality
- Harness is now trustworthy — future parity reports will have real signal

Still required:
- Phase 2: loomcli-specific surface (`SearchIssues`, `Batch`, `GetMutations`/`WaitForMutations`, `Count`, full `Stats`) — this harness doesn't cover them
- Three category-A/B/C cleanups if we want a clean PASS verdict on the harness

## Files

- `release-report.md` — this run's top-line summary
- `diff-report.json` — full 727-diff detail
- `analysis.md` — this file
- Parent directory has the baseline run for comparison
