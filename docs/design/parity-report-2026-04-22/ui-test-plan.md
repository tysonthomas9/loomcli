# UI Parity Test Plan

**Status:** Approved, implementation pending
**Paired with:** `test/parity/browse.md` (manual checklist),
`test/parity/docker-compose.parity.yml` (stack), `webui-gaps.md`
(findings template).
**Delivery:** new `test/parity/ui/` Playwright suite; `make test-parity-ui`.

## 0. Pre-flight (already committed)

Before any test runs, the suite proves the two loom instances are on
different backends:
- `GET :8081/api/config .issue_backend` → `beads`
- `GET :8082/api/config .issue_backend` → `fleet`
- Network probe: write to `:8082/api/issues`, confirm fleet-db's access
  log shows `POST /api/v1/PARITY/issues`
- Container healthchecks fail if either loom instance reports the wrong
  backend (already wired in docker-compose.parity.yml)
- Settings page renders different backend strings in the two tabs

If any fail → suite aborts. No silent fallback can pass.

## 1. Test organization

```
test/parity/ui/
├── _support/
│   ├── preflight.ts
│   ├── backends.ts
│   ├── assert-routing.ts
│   ├── capture.ts
│   └── fixtures.ts
├── 01-kanban.spec.ts
├── 02-table.spec.ts
├── 03-graph.spec.ts
├── 04-monitor.spec.ts
├── 05-settings.spec.ts
├── 06-issue-detail.spec.ts
├── 07-comments.spec.ts
├── 08-dependencies.spec.ts
├── 09-sse-realtime.spec.ts
├── 10-create-flow.spec.ts
├── 11-update-flow.spec.ts
├── 12-close-reopen-flow.spec.ts
├── 13-search.spec.ts
└── 14-error-handling.spec.ts
```

## 2. Per-test skeleton

```
beforeAll:
  preflight()                    # abort whole run if backends wrong

beforeEach:
  resetBothBackends()            # wipe + reseed identical fixtures
  assertFleetDBReachable()       # curl fleet-db healthz
  snapshotBackendState("before") # GET /api/issues on both, save

test:
  # Mirror action on both tabs
  parallel([tabBeads.do(X), tabFleet.do(X)])

  assertFleetSideRoutedToFleetDB()   # network intercept proves it
  captureBothTabs("after-X")         # screenshot + DOM
  diffVisual(ref, actual)            # png diff, 2% threshold
  diffDOM(beadsDOM, fleetDOM)        # structural diff
  diffAPIResponses("/api/issues")    # data-level diff
  diffBackendState(before, after)    # delta sanity

afterEach:
  dumpNetworkLog(testId)
  if failed: saveFullForensics(testId)
```

## 3. Fleet-db routing verification (every test, every action)

Three independent verifications per fleet-tab write:

1. **Browser network intercept.** Playwright's `page.on('request')`
   listener. Every XHR/fetch from the fleet tab to `/api/issues` must
   also show a downstream request to `http://fleet-db:8080` in the
   network trace.
2. **Fleet-db access log diff.** Request count before/after must differ
   by ≥1 for writes.
3. **Redis stream check.** `redis-cli XLEN events:PARITY` must grow.

If any shows zero fleet-db activity after a fleet-tab write → test fails
as "silent fallback detected".

## 4. Assertion layers

| Layer | What | Example |
|---|---|---|
| Visual | screenshot diff ≤2% pixel threshold | Kanban columns rendered identically |
| Structural | DOM tree diff ignoring IDs/timestamps | same nodes / classes / order |
| Data | API response shape diff | `/api/issues` returns same fields |
| Network | fleet-tab requests reach `fleet-db:8080` | routing proof |
| State | backend state sync between tabs | no phantom data |
| Timing | SSE event latency within 2× of other side | no hung subscriptions |

## 5. Coverage matrix

```ts
const REQUIRED_ROUTES = [
  "/api/issues", "/api/issues/:id",
  "/api/issues/:id/close", "/api/issues/:id/reopen",
  "/api/issues/:id/labels", "/api/issues/:id/comments",
  "/api/issues/:id/deps", "/api/issues/:id/events",
  "/api/issues/ready", "/api/issues/blocked",
  "/api/issues/search", "/api/issues/stats",
  "/api/config", "/api/workspaces", "/api/sse",
];

const REQUIRED_FIELDS = [
  "id", "title", "description", "status", "priority", "type",
  "assignee", "owner", "labels", "external_ref", "defer_until",
  "due_at", "parent_id", "repo", "created_at", "created_by",
  "updated_at", "closed_at", "close_reason",
];
```

`afterAll` writes a coverage report; suite fails if any required route
was not exercised by at least one test.

## 6. Output artifacts

```
test/parity/ui/artifacts/
├── reports/
│   ├── index.html                # summary pass/fail tiles
│   ├── coverage.json
│   ├── routing-proof.json        # per-test fleet-db calls verified
│   └── data-diffs.json
├── screenshots/
│   └── <test>/{beads,fleet,diff}.png × per step
├── network-traces/
│   └── <test>/{beads,fleet}.har
└── forensics/                     # only on failure
    └── <test>/
        ├── {beads,fleet}-dom.html
        ├── backend-state-{before,after}.json
        ├── fleet-db-log.txt
        └── video.webm
```

## 7. Failure modes explicitly handled

- Silent backend fallback → preflight + network verification abort
- SSE disconnect → heartbeat timeout fails (no hangs)
- Flaky timestamps → normalize ±5s (matches Go harness)
- Race on reseed → `resetBothBackends` serialized + post-check
- Docker network hiccup → 3 retries then fail fast
- Screenshot env differences → pinned Playwright Docker image, fixed
  viewport / DPI / fonts

## 8. Integration with existing artifacts

- Runs against `docker-compose.parity.yml` (5-service stack)
- Writes results into `webui-gaps.md` Step 0 table
- Uses `seed.sh` for identical fixtures
- Matches fleet-db `parity` build-tag Go harness conventions

## 9. Out of scope

- Perf / load testing — this is correctness parity
- Chaos / network partitions
- Accessibility parity
- Cross-browser (Chromium only via Playwright)
- Mobile viewports
- Authentication flows (not in parity stack)

## 10. Tech choice rationale

Playwright over agent-browser:
- Native `page.on('request')` interception → cheap routing proofs
- Built-in screenshot diff + video + HAR export
- Matches existing loomcli frontend e2e stack
  (`internal/webui/frontend/tests/e2e/`) so devs don't learn a second tool
- Pinned Docker image removes DPI/font environment drift

agent-browser is best for ad-hoc exploration; the suite needs discipline
Playwright gives for free.

## 11. Agent deliverables

1. `test/parity/ui/` directory with 14 spec files
2. `_support/` helpers (preflight, routing-assertion, diff, capture)
3. `playwright.config.ts` with Docker test env
4. `make test-parity-ui` Makefile target
5. Initial run producing real artifacts + filling `webui-gaps.md` Step 0
6. GitHub Actions snippet for hermetic CI
