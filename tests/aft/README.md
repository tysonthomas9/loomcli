# aft browser tests

Live-server E2E suites for the Loom web UI, driven by
[aft](https://github.com/tysonthomas9/aft) — deterministic YAML browser tests with a
Claude recovery agent that diagnoses (`--strict`) or heals (`--heal`) failures.

These complement the mocked Playwright suite: they run against the **real v5 stack**
(`loom serve` + embedded fleet-db + vite preview, fresh isolated workspace, auth open),
so they cover what mocks can't — above all that server-side mutations reach the UI via
SSE.

## Run

```bash
make test-aft                      # deterministic, no model calls (what CI runs)
make test-aft-strict               # failures get an agent diagnosis + suggested fix
make test-aft-heal                 # local dev: agent may complete a broken step's intent
tests/aft/run-aft.sh --record ...  # or call the harness directly with any aft flags
```

Extra aft flags go through `AFT_ARGS`, e.g.
`make test-aft AFT_ARGS="--screenshots --record-all"`.

The harness starts `scripts/start-e2e-server.sh` (loom API on `E2E_PORT`, default 8090;
vite preview on `E2E_FRONTEND_PORT`, default 3100, proxying `/api`), waits for readiness,
runs every suite in `tests/aft/suites/`, and tears the stack down. The primary workspace
is `e2e-ws` with id **`E2E-WS`** — exported to suites/hooks as `AFT_WS`.

Requirements: `go`, `node` >= 20 (24 for agent-browser), `agent-browser`, a **fleet-db**
checkout (default `../fleet-db`, override `FLEET_DB_REPO`), an aft checkout (default
`../testing-app`, override `AFT_DIR`), and `claude` unless `--no-agent`.

Every run writes `tests/aft/reports/report.html` — a self-contained run browser (suite
navigation, run history + trend, step screenshots, video playback with a step timeline,
agent verdicts).

## Coverage

Coverage is measured, not declared. Each run regenerates a **census** of the web UI's
surface straight from the frontend source (`scripts/gen-census.py`: routes from
`router.tsx`, `data-testid`s, API endpoints from `src/api`), and aft records a **trace**
of what each test actually touched (browser URLs, the page's own network requests, API
calls from `run:` steps, executed-step testids/selectors). The join — covered and
uncovered routes/endpoints/testids, with the tests that touched each — prints after the
run summary and renders at the top of the HTML report. A new route or endpoint added to
the app shows up as uncovered automatically; no list to maintain.

## CI

`.github/workflows/aft.yml` runs `make test-aft` (plus `--record --screenshots --junit`)
on every PR. Reports upload as the `aft-reports` artifact and a per-test table lands in
the step summary. `workflow_dispatch` with `mode: strict` adds agent diagnoses (needs
`ANTHROPIC_API_KEY`). Required secret: `AFT_CHECKOUT_TOKEN` with read access to the
private `tysonthomas9/aft` **and** `tysonthomas9/fleet-db` repos.

## Suites

- `smoke.test.yaml` — boots to `/ws/E2E-WS/kanban`, and an issue POSTed to
  `/api/workspaces/E2E-WS/issues` *while the board is open* appears via SSE, no reload.
- `issue-lifecycle.test.yaml` — create → `in_progress` → close via the API; the board
  follows each transition live.
- `issue-create-ui.test.yaml` — creates an issue through the New Issue modal
  (`new-issue-button` → `create-issue-title` → `create-issue-submit`).
- `filters.test.yaml` — board search narrows the kanban, syncs `?search=`, survives
  reload. (v5's board toolbar has no group-by control, so search only.)
- `views.test.yaml` — Kanban ↔ List via the toolbar tabs (`role=tab`, List routes to
  `/ws/:id/list`; the data table stays at `/ws/:id/table` and is covered by deep link).

Suites that create issues declare `teardown: scripts/close-open-issues.sh`, so every
suite starts against an empty board. Cross-step state goes through `$AFT_WORK_DIR`.

## v5 anchors worth knowing

The default board is **epic-grouped swim lanes** (`swim-lane-board`); issues without an
epic land in the "Ungrouped" lane (`swim-lane-lane-epic-__ungrouped__`). Columns are
`section[data-status=backlog|ready|blocked|in_progress|review|done]` — the "Open" column
is `data-status=ready` and holds status-`open` issues; cards are `article` elements
(`aria-label^="Issue: ..."`, no testid). There is **no rendered connection indicator** in
v5 — assert SSE by observing board content. `<title>` is "Loom"; there is no h1 on the
board. Kanban drag uses dnd-kit PointerSensor with a 5px activation distance.
