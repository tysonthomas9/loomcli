# Beads vs Fleet-DB Parity Validation

**Status:** Draft — awaiting approval
**Date:** 2026-04-22
**Supersedes planning for:** `docs/design/remove-beads.md`
(parity validation runs first; removal is re-scoped afterwards based on findings)
**Delivery:** Dedicated branch `beads-vs-fleet-parity` off `v4`.

---

## 1. Premise

Before removing beads from loomcli, verify that fleet-db provides feature
parity across three surfaces that loomcli depends on:

1. **Interface parity** — `backend.IssueBackend` method shapes + semantics
2. **CLI parity** — every `bd …` subcommand loomcli / agents rely on has a
   fleet equivalent that returns the same shape
3. **Webui parity** — Kanban / Table / Graph / Monitor / Settings views
   render identically against both backends

If gaps exist, fix them in fleet-db **before** removing beads from
loomcli.

## 2. Existing infrastructure (do not reinvent)

`~/codebase/fleet-db/` already has most of the harness:

- `test/parity/` — Go test suite under build tag `parity`
  - `BeadsCaller` drives the real `bd` CLI
  - `Contract`, `Model`, `Operation`, `Invariant`, `Waiver` types
  - `dual_runner` runs ops against both backends and emits diffs
  - `differ`, `normalizer`, `report` produce `artifacts/parity/*.json` +
    `release-report.md`
- `internal/compat/` — RPC-level compatibility layer with its own
  tests (`crud_test.go`, `claim_test.go`, `cycle_test.go`,
  `deferred_ready_test.go`, `dep_graph_test.go`, `epic_ready_test.go`,
  `ready_test.go`, `status_transitions_test.go`, `rpc_compat_test.go`)
- `test/fixtures/` — 32 fixtures covering claims, comments,
  dependencies, labels, ready queue, status transitions
- `api/openapi.yaml` — source of truth for fleet-db's HTTP API
- `docs/parity/README.md` + `RELEASE-CHECKLIST.md`
- `make test-parity` — runs the whole harness and writes artifacts

### Last run results (as of 2026-04-02)

- Verdict: PASS
- Mode: `fleet_db_only` — **`bd` was not installed**, so comparisons: 0
- 32 fixtures, 120 steps, 0 diffs (because nothing to diff against)
- 3 permanent architecture-approved waivers on record

**Conclusion:** fleet-db has a parity harness but the last run did not
actually compare against beads. To get a real answer, we need to run
it with `bd` available — then fill gaps that the harness doesn't
cover for loomcli-specific surfaces.

## 3. Plan

Three phases, each committed separately. Phase 1 answers the main
question cheaply. Phases 2–3 fill the loomcli-specific gaps.

### Phase 1 — Run the existing parity harness with bd present

**Goal:** turn the fleet-db parity report from synthetic-only into a
real bd-vs-fleet-db diff.

**Artifacts produced:**
- `~/codebase/fleet-db/artifacts/parity/diff-report.json` with
  real `total_comparisons > 0`
- `~/codebase/fleet-db/artifacts/parity/release-report.md` regenerated
- A checked-in snapshot of the outputs under this repo's
  `docs/design/parity-report-2026-04-22/` so the loomcli side has a
  referenceable record

**Steps:**
1. Ensure `bd` is on PATH in the test environment (build from
   `loomcli/third_party/beads/cmd/bd`, put in `$GOBIN`)
2. Ensure a Redis instance is reachable (`redis:6379` via docker run)
3. `cd ~/codebase/fleet-db && make test-parity`
4. Inspect `artifacts/parity/release-report.md`:
   - If PASS with bd available → interface-level parity validated
   - If FAIL → diff-report.json enumerates specific operations/fields
     where fleet-db differs from bd
5. Copy the artifacts into
   `loomcli/docs/design/parity-report-2026-04-22/` and commit

**Time:** half a day if the harness works out of the box; full day if
bd isn't building cleanly or fixtures need updates.

**Exit criteria:** a real parity report exists, checked into loomcli,
with `beads_available: true` and non-zero `total_comparisons`.

### Phase 2 — Loomcli-specific parity layer

**Goal:** cover the surfaces fleet-db's harness can't see — things
specific to how loomcli uses the backend.

**What fleet-db's harness does NOT cover:**
- Loomcli's `backend.IssueBackend` interface is a *superset* of what
  the fleet-db contract validates. Additional methods loomcli uses:
  - `SearchIssues(query, limit)` — full-text ranked search
  - `GetChildren(id)` — epic children
  - `DeferIssue(id, until)` / `UndeferIssue(id)` — scheduling
  - `ListEvents(id, limit)` — audit trail per issue
  - `Batch(ops)` — atomic multi-op
  - `GetMutations(sinceMs)` / `WaitForMutations(sinceMs, timeoutMs)` —
    SSE / polling
  - `RemoveLabel(id, label)` — label deletion (not just add)
- Loomcli's test wrappers around the interface (`ipcIssueBackend`,
  `agentipc.Backend`) — fleet-db has no visibility into these
- Loomcli-specific workflows that chain multiple ops: epic ready
  queue, claim→update→close, recovery after crash

**New code (loomcli side):**
- `internal/backend/paritytest/` — a new package under build tag
  `parity` that:
  - Instantiates both `beadsbackend.New(...)` and
    `fleet.New(...)` against running instances
  - Iterates a fixture matrix mirroring fleet-db's but extended for
    loomcli-specific methods
  - Emits a diff report compatible with fleet-db's format so they
    can be consolidated
- `test/parity/loomcli_fixtures.jsonl` — fixtures for the extended
  surface
- `make test-parity` target in loomcli's Makefile that spins up:
  - Redis (via testcontainers or docker run)
  - fleet-db server (build from `~/codebase/fleet-db`)
  - bd daemon (from `third_party/beads`)
  - Runs the harness

**Expected gaps (hypotheses to validate):**
- Fleet-db may not implement `GetMutations` / `WaitForMutations` —
  loomcli's SSE hub depends on these for real-time UI updates
- Fleet-db may not have `SearchIssues` with the same relevance ranking
  as beads SQLite FTS
- Fleet-db's `Batch` atomicity may differ from beads'
- `GetChildren` via the loomcli HTTP backend may require an extra
  round-trip that's a single op in beads

**Artifacts:** a per-method gap table committed to
`docs/design/parity-report-2026-04-22/loomcli-gaps.md`.

**Time:** 2–3 days of focused work depending on how many real gaps
surface.

### Phase 3 — docker-compose for manual side-by-side + webui parity

**Goal:** let a human drive both backends through `loom serve` and
compare Kanban / Table / Graph / Monitor visually. Also covers any
bd-CLI-surface gap that only shows up in interactive workflows.

**New files (loomcli side):**
- `test/parity/docker-compose.parity.yml` — 5 services:
  - `redis` — shared by fleet-db; bd's sqlite storage is per-container
  - `fleet-db` — built from `~/codebase/fleet-db/deploy/docker/Dockerfile`,
    port 8080
  - `loom-beads` — loomcli built locally, `LOOM_ISSUE_BACKEND=beads`,
    bd daemon inside container, webui on :8081
  - `loom-fleet` — loomcli built locally,
    `LOOM_ISSUE_BACKEND=fleet`,
    `LOOM_FLEET_URL=http://fleet-db:8080`,
    webui on :8082
  - `parity-seed` — a one-shot job that loads the same fixture data
    into both backends and emits a "ready" signal when both are
    seeded
- `test/parity/seed.sh` — seeds a known fixture set (10 issues, 3
  epics, 2 dependency chains, comments, labels) into both sides
- `test/parity/browse.md` — step-by-step checklist for manual webui
  parity:
  - Kanban: swim lanes populated identically, drag-drop works on both
  - Table: sorting / filtering return same rows
  - Graph: same nodes + edges rendered
  - Monitor: stats numbers match within tolerance
  - Settings: backend selector shows correct active value
- `test/parity/agent-browser.md` — optional: Playwright script via
  agent-browser to automate the webui checklist

**Commands:**
```bash
cd loomcli
docker compose -f test/parity/docker-compose.parity.yml up -d
docker compose -f test/parity/docker-compose.parity.yml exec parity-seed /seed.sh
# browse http://localhost:8081 (beads) and http://localhost:8082 (fleet) side by side
```

**Artifacts:** annotated screenshots + any webui differences
committed to `docs/design/parity-report-2026-04-22/webui-gaps.md`.

**Time:** 1–2 days.

## 4. Decision tree after Phase 1

| Phase 1 result | Action |
|---|---|
| Full parity, no diffs | Proceed to Phase 2 for loomcli surfaces; the answer is probably "we can remove beads" |
| Small surmountable diffs (e.g. missing endpoint in fleet-db) | Open fleet-db tickets, fix there, re-run Phase 1 before Phase 2 |
| Large semantic diffs (e.g. ready-queue ordering differs) | Stop. Re-design. Consider whether replacement is still the right call. |

## 5. Ownership

- Phase 1: loomcli (us). Read-only against fleet-db.
- Phase 2: both repos. Findings drive fleet-db PRs, harness lives in loomcli.
- Phase 3: loomcli, with webui specifics.

## 6. Out of scope

- Actually removing beads from loomcli. That plan
  (`docs/design/remove-beads.md`) becomes unblocked only after Phase
  1 shows adequate parity.
- Migrating existing `.beads/` data. Still skipped per prior decision.
- Modifying fleet-db's contract to match beads exactly — architecture
  waivers (WAIVER-001/002/003) stand.
- Benchmarking. This is a correctness exercise, not a perf one.

## 7. Risks

| Risk | Mitigation |
|---|---|
| fleet-db's parity harness doesn't build with current bd version | Pin both versions explicitly; record toolchain versions in the report dir |
| Running bd + fleet-db together in docker needs different networks/mounts | Use `docker compose` with a shared volume for fixtures; isolate redis per-service if needed |
| Loomcli's IssueBackend surface is larger than fleet-db knew | That's exactly what Phase 2 is for — expect gaps |
| Webui behavior differs subtly (ordering, timestamp formats, null handling) | Phase 3 catches these; log them as fleet-db tickets rather than loomcli waivers |
| Phase 2 balloons in scope | Cap fixture count at 30 for Phase 2; remaining gaps become fleet-db tickets |
| fleet-db requires Redis but bd is standalone | docker-compose handles this; no special handling needed |

## 8. Deliverables summary

- **Phase 1:** real parity report checked into `docs/design/parity-report-2026-04-22/`
- **Phase 2:** loomcli parity harness in `internal/backend/paritytest/`, gap table, `make test-parity` target
- **Phase 3:** docker-compose + webui checklist, optional agent-browser script, gap report

---

*Next step: run Phase 1 and attach its real report here before
scoping Phases 2–3 in detail.*
