# UI FleetDB Regression Test Suite

Side-by-side Playwright suite that proves the web UI renders identically
when loom is backed by reference (`:8081`) vs fleet-db (`:8082`). Implements
`docs/design/fleetdb-regression-report-2026-04-22/ui-test-plan.md`.

## What makes this different from the frontend e2e suite

The frontend e2e suite in `internal/webui/frontend/tests/e2e/` mocks the
backend so it can test React components in isolation. This suite does the
opposite: it runs two REAL backends (reference + fleet-db) and asserts the UI
behaves the same against both. Silent fallbacks are the main failure mode
we defend against — see [Routing verification](#routing-verification).

## Preflight — the most important thing

**Every run executes a preflight that aborts the suite if the two
backends aren't what we expect.** See `_support/preflight.ts`. Nine
checks, all must pass:

1. `GET :8081/api/config` returns `issue_backend: "reference"`
2. `GET :8082/api/config` returns `issue_backend: "fleet"`
3. All three containers (loom-reference, loom-fleet, fleet-db) are healthy
4. A POST to `:8082/api/issues` shows up in fleet-db's access log
5. `loom-fleet` container env `LOOM_FLEET_DB_URL` == `http://fleet-db:8080`
6. `loom-fleet` container env `LOOM_WORKSPACE` == `FLEETDB`
7. `fleet-db` admin API lists the `FLEETDB` workspace
8. `:8081/api/config` surfaces "reference" to the Settings page
9. `:8082/api/config` surfaces "fleet" to the Settings page

On every run the preflight rewrites the Step 0 table in
`docs/design/fleetdb-regression-report-2026-04-22/webui-gaps.md` with actual results.

If the stack isn't up, preflight fails loudly with instructions — it
NEVER "skips because no stack" (that's a silent-fallback vector; see
ui-test-plan.md §0).

## Running locally

```bash
# One-time: build + start the regression stack
docker compose -f test/fleetdb/docker-compose.regression.yml up -d --build
docker compose -f test/fleetdb/docker-compose.regression.yml run --rm fleetdb-regression-seed-fleet

# Run the suite
make test-fleetdb-ui

# Inspect results
open test/fleetdb/ui/artifacts/reports/html/index.html
cat test/fleetdb/ui/artifacts/reports/coverage.json
cat test/fleetdb/ui/artifacts/reports/routing-proof.json

# Tear down
docker compose -f test/fleetdb/docker-compose.regression.yml down -v
```

### Fleet-db-only regression mode

After side-by-side regression has passed, the same browser suite can run against
only fleet-db-backed loom. This path does not start or require `loom-reference`,
or any old issue-store sidecar.

```bash
docker compose -f test/fleetdb/docker-compose.regression.yml up -d --build redis fleet-db loom-fleet ui-fleet fleetdb-regression-seed-fleet

make test-fleetdb-ui
```

Fleet-only mode sets `FLEETDB_MODE=fleet-only`. The harness opens two isolated
browser contexts against the fleet UI so existing dual-tab assertions still
exercise rendering, SSE, routing, and write flows without a reference backend.

## Environment overrides

| var | default | purpose |
|---|---|---|
| `LOOM_REFERENCE_URL` | `http://localhost:8081` | reference-backed loom |
| `LOOM_FLEET_UI_URL` | `http://localhost:8082` | fleet-backed loom |
| `FLEET_DB_URL` | `http://localhost:8080` | fleet-db admin API |
| `FLEETDB_WORKSPACE` | `FLEETDB` | fleet-db workspace key |
| `FLEETDB_MODE` | `dual` | `dual` or `fleet-only` |
| `FLEETDB_COVERAGE_WAIVE` | (none) | comma-separated list of routes to skip |
| `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` | (system) | pin Docker chromium |

## Routing verification

Every fleet-tab write passes through `assertRoutingForAction`, which
confirms the action reached fleet-db through THREE independent channels:

1. Browser network intercept — Playwright's `page.on('request')` saw a
   matching XHR leave the fleet tab.
2. Fleet-db request delta — fleet-db's metrics / access log counter grew.
3. Redis XLEN delta — `events:FLEETDB` stream grew (proves fleet-db
   actually applied the write, not just received it).

If ANY of the three shows zero on a write action, the test fails with
`silent-fallback detected` and dumps `forensics/<testId>/routing-*.json`.

## Per-spec skeleton

All 14 specs follow the shape in `_support/spec-harness.ts`:

```ts
import { fleetdbTest as test, expect, useFleetDBHooks } from "./_support/spec-harness";
import { gotoViews, assertRoutingForAction, captureBothTabs, visualDiff } from "./_support";

useFleetDBHooks();  // wires preflight + resetBothBackends

test("...", async ({ tabs, fleetSpy }) => {
    await gotoViews(tabs, "kanban");
    await assertRoutingForAction(tabs.testId, "action", fleetSpy, async () => {
        // action that must hit fleet-db
    });
    const shot = await captureBothTabs(tabs.reference, tabs.fleet, tabs.testId, "step");
    await visualDiff(shot);
});
```

## 14 spec files

| file | covers |
|---|---|
| `01-kanban.spec.ts` | swim-lane count, drag-drop, ordering |
| `02-table.spec.ts` | row count, sort, filter |
| `03-graph.spec.ts` | node + edge count |
| `04-monitor.spec.ts` | stats counters, ready queue |
| `05-settings.spec.ts` | backend selector string |
| `06-issue-detail.spec.ts` | all REQUIRED_FIELDS render |
| `07-comments.spec.ts` | add/list comments (WAIVER-003 body vs text) |
| `08-dependencies.spec.ts` | add/remove dep, blocks chain |
| `09-sse-realtime.spec.ts` | create in tab1, tab2 within 2x |
| `10-create-flow.spec.ts` | full form submit |
| `11-update-flow.spec.ts` | PATCH priority + description |
| `12-close-reopen-flow.spec.ts` | close_reason display, reopen clears |
| `13-search.spec.ts` | same query, same result set |
| `14-error-handling.spec.ts` | 404, 422, 409 fleetdb-regression |

## Known limitations

- The suite assumes the docker stack is already up. See preflight above.
- The first run in this sandbox FAILS preflight because the sibling
  `fleet-db` repo isn't at `../../../fleet-db` relative to the compose
  file. Real operators have it at the expected path. The Makefile target
  and test harness work; only the stack boot is sandbox-limited.
- `visualDiff` does a byte-level comparison as a coarse change detector;
  true pixel diffs come from Playwright's `expect(page).toHaveScreenshot`
  mechanism when specs use it directly.
- The drag-and-drop spec (`01`) uses an API-level PATCH instead of dnd-kit
  drag events because the latter is flaky under Playwright and the wire
  effect is identical. If a future redesign changes the drop wire
  endpoint, update `01-kanban.spec.ts` accordingly.

## Coverage gate

`_support/global-teardown.ts` flushes a coverage report to
`artifacts/reports/coverage.json` and throws if any of the
`REQUIRED_ROUTES` weren't exercised. Operators can knowingly waive
routes via `FLEETDB_COVERAGE_WAIVE=/api/issues/blocked,...`. Starting
fresh, most routes should be hit on a clean run.
