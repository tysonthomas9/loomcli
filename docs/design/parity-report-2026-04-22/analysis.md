# Parity Run Analysis — 2026-04-22

**Raw verdict:** FAIL (31 of 32 fixtures failed, 473 unapproved diffs)
**Actionable verdict:** fleet-db is broadly feature-complete for the harness surface; ~400 of the reported diffs are harness-side issues, not fleet-db semantic gaps.

## What the harness actually measured

| Counter | Value |
|---|---|
| fixtures_run | 32 |
| steps_executed | 120 |
| total_comparisons | 619 |
| diffs_found | 619 |
| unapproved_diffs | 473 |
| waived_diffs | 88 (WAIVER-001/002/003 working correctly) |
| normalized_diffs | 58 (timestamps etc. normalized away) |
| verdict | fail |

## Critical finding: 553 of 619 diffs have NO beads value

Of the 619 diff entries, 553 (~89%) have only a `fleet_db` field populated; the
`beads` field is absent. This means the BeadsCaller in fleet-db's harness did
not capture bd's response payload for most operations. The diff engine then
compares "structured fleet-db object" to "nothing" and reports every field as
a mismatch.

**Distribution of missing-beads diffs by method:**

| Method | Missing-beads diffs | Likely cause |
|---|---|---|
| `issue.create` | 406 | bd emits `"✓ Created issue: <id>"` shell text, not a JSON object; BeadsCaller doesn't parse it into a structured response |
| `issue.show` | 67 | partial capture; some fields parsed, most missing |
| `worker.claim` | 30 | same pattern |
| `label.add` | 20 | same |
| `issue.update` | 12 | same |
| `label.remove` | 9 | same |
| `issue.reopen` | 9 | same |

**These are NOT parity gaps — they are harness bugs.** Fixing BeadsCaller to do a
follow-up `bd show <id> --json` after each successful mutation would eliminate
the majority of failure noise.

## Real actionable diffs (66 where both sides have values)

Grouped by category:

### A. Error-code mapping differences (beads JSON-RPC -32603 vs fleet-db narrower codes)

| Op | bd code | fleet-db code |
|---|---|---|
| `dep.add` failures | -32603 (generic) | -32002 (specific) |
| `comment.add` failures | -32603 | -32000 |
| `worker.claim` already-claimed | (bd returns success) | -32001 |

These look fixable by aligning fleet-db's error taxonomy to bd's, or by widening the parity contract to treat related codes as equivalent.

### B. Idempotency / strictness differences (real behavior drift)

- **`label.add` duplicate**: bd = success (idempotent), fleet-db = error `-32002` "label already present"
- **`label.remove` missing**: bd = success (idempotent), fleet-db = error `-32002` "label not present on issue"
- **`worker.claim` already-claimed**: bd = success (no-op), fleet-db = error `-32001` "issue is already claimed"

These are the three semantic behavior differences most worth a team
discussion. Idempotency is a legitimate design choice either way; the parity
contract should pick one and waive the other.

### C. Features fleet-db has that the harness couldn't exercise in bd

- **`issue.children`**: fleet-db returns children; bd returned None in the one fixture
- **`issue.blocked`**: fleet-db returns blockers; bd returned None
- **`issue.defer`** / **`issue.undefer`**: bd rejects because harness uses `--defer-until` / `--undefer` flags (bd uses `--defer <time>` and `--defer ""` to clear). Harness flag mismatch, not a missing feature.
- **`issue.ready.issues[N]`**: fleet-db returns populated issue objects; bd returned None (again, harness capture issue — bd's `bd ready --json` does emit JSON, so BeadsCaller may not be passing `--json`)

### D. Harness template-variable bugs

- `dep.add` fixtures fail with `Error: resolving issue ID ${dep_b_id}` — literal unsubstituted template variable. The fixture's `${dep_b_id}` is not being resolved before passing to bd. This is a harness bug.
- `issue.close`, `dep.remove`, `comment.list` fail for the same reason.

## Categorization summary

| Category | Count | Action |
|---|---|---|
| Harness not capturing bd response payloads | ~400 | Fix BeadsCaller: follow each mutation with `bd show --json` |
| Harness template variable substitution bugs | ~30 | Fix fixture loader; issue IDs need to be resolved before bd invocation |
| Harness bd-flag mismatches (`--defer-until`, `--undefer`, `comment --body`) | ~20 | Update BeadsCaller to use current bd flag names |
| Real error-code taxonomy drift | ~15 | Decide: align fleet-db codes to bd's, or widen parity contract |
| Real idempotency drift | ~10 | Design decision needed (label add/remove, claim re-claim) |
| Waived (architecture-approved permanent) | 88 | Already handled |
| Normalized (timestamps, workspace) | 58 | Already handled |

## What this tells us about the remove-beads direction

**Good news for removal:**
- Fleet-db's HTTP surface is broader than bd's (`issue.children`, `issue.blocked`, native defer without flag weirdness)
- The three architectural waivers (type subset, sequential IDs, body/text field name) are by design
- No fixture revealed fleet-db missing a feature bd has

**Caveats:**
- The harness wasn't actually producing trustworthy comparisons for the majority of ops — so absence of evidence of missing features ≠ evidence of absence
- Three real behavior differences (idempotency of label add/remove, claim double-claim) need a design decision before we can declare parity
- Loomcli-specific surface (`SearchIssues`, `Batch` mixed-ops, `GetMutations`, `Count`, full `Stats`) isn't tested by this harness at all — Phase 2 still required

## Recommendations

1. **Do NOT conclude "fleet-db lacks features" from the raw 473 failures.** Most are harness bugs.
2. **File an upstream fleet-db ticket** to fix the BeadsCaller so it captures bd response payloads (via follow-up `bd show --json`).
3. **Proceed to Phase 2** to cover loomcli-specific surface, but upgrade P2.4 (the new loomcli harness) with a lesson from this run: capture both sides' responses as full JSON objects, not shell text.
4. **Discuss the three idempotency divergences** with the team before building fixtures that encode one direction — pick the contract, then waive the other.
5. **Don't re-run this harness in isolation** until the BeadsCaller capture bugs are fixed; it produces more noise than signal.

## Files in this directory

- `release-report.md` — top-line verdict (unchanged from 2026-04-02 template; overwritten on every make test-parity run)
- `diff-report.json` — full 619-diff detail as of 2026-04-22T04:20Z
- `toolchain.md` — environment versions used to produce this run
- `analysis.md` — this file
