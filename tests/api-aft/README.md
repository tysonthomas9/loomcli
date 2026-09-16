# api-aft — API end-to-end harness for loom ↔ fleet-db

Scenario-driven end-to-end tests at the **HTTP layer**, with full request/response
capture and a layered oracle stack. Complements `tests/aft` (browser) rather than
replacing it, and unlike `tests/aft` it has **no external engine dependency** — it runs
from this directory with `node`.

```bash
npm install
node bin/api-aft.ts doctor    # no stack, no network; reports spec oracle strength
node bin/api-aft.ts run       # isolated stack, scenarios, oracles, artifacts
```

Both are also `make test-api-aft-doctor` / `make test-api-aft` from the repo root.

## The bet

The oracles worth building are the ones that **can fail for a reason other than someone
editing a file**. `doctor` shows why the specs alone are not enough:

| | loom | fleet-db |
|---|---|---|
| operations | 142 | 259 |
| bare `type: object` 2xx (zero oracle power) | 18 | 0 |
| enum-constrained properties | 36 / 729 | 61 / 1655 |
| objects closed to extra fields | **0** | **0** |

So the spec is an **advisory** lane. It supplies breadth and paste-ready patches, but it
never fails a run on its own. What carries the run is derived from the system itself.

## Coverage

`node bin/api-aft.ts plan` classifies all 401 operations, because "401" is not an honest
denominator: some stream, some spawn PTYs, some need GitHub. Current state:

| class | total | exercised | checked | notes |
|---|---:|---:|---:|---|
| `safe-read` | 153 | 146 | 128 | read sweep |
| `mutating` | 153 | 120 | 117 | scenario packs |
| `destructive` | 27 | 7 | 6 | needs isolated packs |
| `streaming` | 27 | 13 | 12 | needs a stream reader |
| `host-effecting` | 33 | 0 | 0 | gated tier: spawns processes / touches git remotes |
| `external` | 8 | 1 | 1 | gated tier: needs a GitHub fixture |
| **TOTAL** | **401** | **287** | **264** | |

Remaining unbindable path params are the work list: `{taskId,sessionId}`, `{id,phase}` --
each needs a scenario that creates the entity, never a fabricated id.

## The oracle stack

Checked in precedence order. A lower layer never overrules a higher one; when L4
disagrees with L2, the spec is the suspect.

| Layer | Oracle | Source of "expected" | Gates? |
|---|---|---|---|
| **L1** | `GET /api/v1/{ws}/admin/verify` | fleet-db replays its own event log in memory and diffs it against the read model (`fleet-db/internal/recovery/verify.go`) | yes |
| **L2** | Seam differential | the same fact observed twice — through loom, and through fleet-db | yes |
| **L3** | Semantic invariants | hand-written, human-reviewed (`src/invariants.ts`) | yes |
| **L4** | Spec conformance, lax + strict twin | `api/openapi.yaml`, both repos | **advisory** |

L1 is the strongest oracle in either repo and nothing else calls it from a test.

Because neither spec closes a single object, undocumented response fields are invisible
to plain validation. `src/contract.ts` therefore synthesizes a **strict twin** with
`unevaluatedProperties: false` at every object root (`unevaluated*`, not
`additionalProperties`, so `allOf` still composes).

## Scenarios contain no assertions

Enforced by `scripts/check-no-asserts.mjs`. A scenario moves state and calls
`w.checkpoint(name)`; the harness then snapshots both observers, folds them into an
entity ledger, and runs **every invariant whose declared inputs the ledger holds**.

That auto-dispatch is the yield multiplier: a 40-line scenario picks up the whole
oracle stack without naming it, and a new invariant retroactively strengthens every
scenario already written. Allowing scenario-local assertions would trade that away one
file at a time.

## Artifacts

Written to `_reports/<runId>/`:

| File | Contents |
|---|---|
| `candidate-findings.md` | deduplicated, triaged, FINDINGS.md-shaped |
| `transcript.json` | every exchange, raw **and** normalized |
| `coverage.json` | **two** numbers — exercised, and actually checkable |
| `ledger.log` | one accounting line per run |

Findings are deduplicated before counting. One defect observed 24 times is one defect;
a raw-observation headline is how a capture-and-diff harness earns a reputation for
noise and gets switched off.

## Safety

- The stack env is an explicit **allowlist**, not an inherited `process.env` — an
  ambient `LOOM_FLEET_DB_URL` would otherwise silently point a run at a shared cloud
  fleet-db.
- Preflight is **fail-closed** and probes identity, not just readiness: loom `/health`
  can answer 200 while every fleet-db call 401s, because `resolveActor()`
  (`internal/bootstrap/openstore.go`) never received an actor.
- Teardown is SIGTERM-and-wait, and reports `ESCALATED` if it had to SIGKILL.
- No credentials, no containers, no model calls. CI-safe.

## Extending

- **A new invariant** — add to `src/invariants.ts` and `ALL_INVARIANTS`. Declare its
  `inputs`; dispatch handles the rest. This is where new bug-finding power comes from.
- **A new scenario** — add `scenarios/*.scn.ts` exporting `name` and `run(w)`. No
  assertions.
- **A known deviation** — add to `KNOWN` in `src/triage.ts` with a note explaining why.
  A pin that stops matching is itself a signal that something was fixed.
