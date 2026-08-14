# aft browser tests

Live-server E2E suites for the Loom web UI, driven by
[aft](https://github.com/tysonthomas9/aft) — deterministic YAML browser tests with a
Claude recovery agent that diagnoses (`--strict`) or heals (`--heal`) failures.

These complement the mocked Playwright suite: they run against a **real `loom serve`**
(fresh isolated bd workspace, auth off, SPA + API + SSE all on one port), so they cover
what mocks can't — above all that server-side mutations reach the UI via SSE.

## Run

```bash
make test-aft                      # deterministic, no model calls (what CI runs)
make test-aft-strict               # failures get an agent diagnosis + suggested fix
make test-aft-heal                 # local dev: agent may complete a broken step's intent
tests/aft/run-aft.sh --record ...  # or call the harness directly with any aft flags
```

Extra aft flags go through `AFT_ARGS`, e.g.
`make test-aft AFT_ARGS="--record --junit tests/aft/reports/junit.xml"`.

## CI

`.github/workflows/aft.yml` runs `make test-aft` (plus `--record --junit`) on every PR:
isolated server, no model calls, ~30s of test time after builds. Failures upload
`tests/aft/reports/` as the `aft-reports` artifact (JSON + JUnit reports, failure
screenshots, failure videos, server log) and a per-test table lands in the job's step
summary via `scripts/report-summary.py`. A `workflow_dispatch` run with `mode: strict`
installs the claude CLI and attaches agent diagnoses + suggested fixes to the summary
(requires the `ANTHROPIC_API_KEY` secret).

Required repo secret: `AFT_CHECKOUT_TOKEN` — a token with read access to the private
`tysonthomas9/aft` repo, which the workflow checks out to `.aft/`.

The harness starts `scripts/start-e2e-server.sh` on `E2E_PORT` (default 8090), waits for
`/health`, runs every suite in `tests/aft/suites/`, and tears everything down — including
bd daemons the run spawned (and only those; pre-existing daemons are left alone).

Every run also writes `tests/aft/reports/report.html` — a self-contained run browser
(suite navigation, run history + trend, step details, verdicts, failure media). Open it
in a browser after a run; in CI it's part of the `aft-reports` artifact.

Requirements: `go`, `node` >= 20, `bd` on PATH (`make install-bd`), an aft checkout
(default `../testing-app`, override with `AFT_DIR`), and `claude` unless `--no-agent`.

Env knobs: `E2E_PORT`, `AFT_DIR`, `AFT_SUITES` (file or dir), `RUN_ID` (unique test data;
defaults to a timestamp).

## Suites

- `smoke.test.yaml` — app boots to kanban, SSE channel connects, and an issue created
  via `POST /api/issues` *while the board is open* appears via server push without a
  reload (the pipeline the mocked suite structurally can't test).
- `issue-lifecycle.test.yaml` — an issue is created, started (`PATCH` to `in_progress`),
  and closed entirely through the API; the kanban follows every transition live via SSE.
- `filters.test.yaml` — search narrows the board, syncs to `?search=`, and survives a
  reload; group-by renders the swim-lane board and syncs `?groupBy=`.
- `views.test.yaml` — nav-rail switching (kanban ↔ table/"List") with `?view=` URL sync
  and deep links. Views are lazy-loaded, so presence is awaited via `wait: {fn}`.

Suites that create issues declare `teardown: scripts/close-open-issues.sh`, so every
suite starts against an empty board regardless of execution order. Cross-step state in a
test (e.g. a created issue's id) goes through files under `$AFT_WORK_DIR`.

## Writing suites

See the aft README for the step/assertion vocabulary. Loom-specific anchors that matter:
the h1 is **Cortex**; there is no client-side router — views switch via the `?view=`
query param; kanban columns are `section[data-status=backlog|ready|blocked|in_progress|review|done]`
("Open" column is `data-status=ready`), cards are `article` elements; the connection
indicator is `[aria-label^="Connection status"]` with `data-state=connected|connecting|reconnecting|disconnected`.
Issues are created out-of-band (HTTP API or bd CLI) — this UI has no New Issue button.
