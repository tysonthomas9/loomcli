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
make test-aft-real                 # opt-in real codex epic-runner tier
make test-aft-terminal             # opt-in live-tmux Logs tab tier (real codex)
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
checkout (default `../fleet-db`; a sibling `../fleet-db-main` — e.g. a git worktree of
origin/main — is preferred when present because the epic-runner needs the driver-runs
domain), an aft checkout (default `../testing-app`, override `AFT_DIR`), a **flue**
checkout at `../flue` (pinned commit in `internal/workflows/FLUE_COMMIT`, built with
pnpm) for the agent-flow suite, and `claude` unless `--no-agent`.

## Real codex tier

`make test-aft-real` runs the opt-in real-codex tier: the server keeps `claude`
stubbed, but lets the epic-runner resolve the operator's real `codex` CLI from
`PATH`. It requires `codex` on `PATH` and a logged-in `~/.codex/auth.json`
(`codex login`). The target passes `--no-agent`; only the server-side codex run is
real.

This does not spend marginal API dollars for a ChatGPT-account codex login, but it
does consume that account's codex rate-limit window. Do not loop it casually.
CI never runs this tier because CI has no operator `~/.codex`, the run is
nondeterministic, and it touches a real account limit.

Accidental triggering is blocked three ways: `AFT_REAL_CODEX=1` must be set, the
separate `make test-aft-real` target sets it, and real scenarios live in
`tests/aft/real-suites/` instead of the default `tests/aft/suites/` directory.
In real mode the harness also unsets `OPENAI_API_KEY`, defaults `AFT_TIMEOUT` to
`600000`, and fails fast if `codex` or `~/.codex/auth.json` is missing.

### Live terminal tier

`make test-aft-terminal` proves the agents-page Logs tab's live-tmux path:
`AgentLogsTab` detects a real auto-mode tmux session, mounts
`EmbeddedTerminal`, and renders non-empty real codex output through wterm. It
requires the normal real-codex preflight (a real `codex` on `PATH` and a logged-in
`~/.codex/auth.json`) plus `tmux` on `PATH`.

This tier is opt-in by running the target; there is no extra environment flag.
Its suites live in `tests/aft/real-terminal-suites/`, so `make test-aft-real`
does not include them. Claude remains stubbed and the spawned agent is codex-only,
so it spends zero Claude tokens. The run does auto-spawn a real agent and consumes
the operator's codex account rate-limit window, so do not loop it casually. CI
cannot run this tier because it has neither operator Codex credentials nor a
supported interactive tmux environment.

### Podman cloud tier

`make test-aft-podman` runs the real-codex scenario against `loom-serve`,
fleet-db, and Redis in separate Podman containers. It proves that Loom selects
ModeCloud and projects session artifacts across the container/Redis boundary.
Runner-filesystem isolation is architectural: Codex writes to the `loom-work`
named volume mounted at `/work`, while the host credential and frontend mounts
(`~/.codex` and the built frontend) are read-only. The suite verifies that
architecture structurally by inspecting the serve container and requiring the
`/work` mount to have type `volume`, never `bind`. It also checks that `HELLO.md`
does not appear under host `tmp/` as a cheap guard against a future host-bind
regression; that absence check is not, by itself, proof of filesystem isolation.

This manual tier requires a running Podman machine, a real `codex` CLI with a
logged-in `~/.codex/auth.json`, and the normal local AFT checkout. The first run
builds the four stack images and can take several minutes; later runs reuse them
unless `AFT_PODMAN_REBUILD=1` is set. On Apple silicon, the capped `loom-serve`
container is the isolation boundary; per-task nested-container sandboxing is not
available through the macOS Podman machine. CI cannot run this tier because it
has neither the Podman machine nor operator Codex credentials, and the real run
consumes an account rate-limit window.

aft runs suite files alphabetically. `zz-agent-flow` creates an agent definition and
run artifacts, but it now runs in its own workspace, **`E2E-WS-AGENT`**, so agent
artifacts no longer leak into the shared **`E2E-WS`** workspace used by empty-state
suites. New suites that create persistent agents should use their own workspace too.

Every run writes `tests/aft/reports/report.html` — a self-contained run browser (suite
navigation, run history + trend, step screenshots, video playback with a step timeline,
agent verdicts).

Product bugs and stack-improvement work surfaced by these runs are tracked in
[`FINDINGS.md`](FINDINGS.md).

## Coverage

Coverage is measured, not declared. Each run regenerates a **census** of the web UI's
surface straight from the frontend source (`scripts/gen-census.py`: routes from
`router.tsx`, `data-testid`s + `testId=` props, API endpoints from `wsUrl()` calls and
`/api` literals across `src` — tests, mocks, and the generated OpenAPI contract
excluded, so only UI-reachable endpoints count), and aft records a **trace**
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
