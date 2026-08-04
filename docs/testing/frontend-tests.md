# Frontend Tests

> **Status:** Current · *audited 2026-08-03*

Where the frontend tests live, how they are wired, and which entry points to
read first. **This page deliberately does not enumerate individual test files.**
It used to, and the inventory rotted: `src/` was reorganised into domain
subdirectories and roughly 20 of 24 spot-checked paths in the old listing no
longer existed. There are 369 Vitest files and 117 Playwright specs; a
hand-maintained list of them cannot stay true. Run the `find` commands below
instead.

**Location**: `internal/webui/frontend/`

---

## Testing Framework

| Tool                  | Purpose                       | Version      |
| --------------------- | ----------------------------- | ------------ |
| Vitest                | Unit/integration test runner  | ^4.0.18      |
| React Testing Library | Component rendering/querying  | ^16.3.2      |
| Playwright            | Browser E2E automation        | ^1.58.0      |
| jsdom                 | DOM simulation, opted into per file | (via Vitest) |

### Configuration Files

- **Vitest**: `vite.config.ts:219-241` — `globals: true`,
  `environment: "node"`, `pool: "forks"`,
  `exclude: ["tests/e2e/**", "node_modules/**"]`, and a v8 coverage block with
  all four thresholds at **60**.
- **Playwright**: `playwright.config.ts` — projects, base URLs, webServer, CI settings.

**The default Vitest environment is `node`, not jsdom.** Every test that needs a
DOM opts in with a per-file docblock:

```ts
/**
 * @vitest-environment jsdom
 */
```

317 of the 369 unit test files carry that docblock. A component test failing on
`document is not defined` is missing it.

### Test Commands

From `package.json`, verbatim (test-related scripts only):

```json
{
  "test": "vitest run && playwright test",
  "test:unit": "vitest run",
  "test:coverage": "vitest run --coverage",
  "test:watch": "vitest",
  "test:e2e": "playwright test",
  "test:e2e:ui": "playwright test --ui",
  "test:e2e:headed": "playwright test --headed",
  "test:e2e:debug": "playwright test --debug",
  "test:visual": "playwright test visual-regression",
  "test:visual:update": "playwright test visual-regression --update-snapshots",
  "test:e2e:integration": "RUN_INTEGRATION_TESTS=1 playwright test --project=integration",
  "test:e2e:local-integration": "RUN_LOCAL_INTEGRATION_TESTS=1 playwright test --project=local-integration",
  "test:e2e:api": "RUN_INTEGRATION_TESTS=1 playwright test --project=api"
}
```

The real-stack smoke and regression flows are **Makefile-only** — there is no
`test:e2e:real-smoke` npm script. `make test-e2e-real-smoke` (`Makefile:417`)
and `make test-e2e-real-regression` (`Makefile:427`) run
`RUN_INTEGRATION_TESTS=1 npx playwright test` against the `*-smoke` / `*-regression`
projects from this directory; the `-local` variants (`Makefile:422`, `:432`)
add `LOOM_LOCAL_SERVER=1`.

`test-e2e-real-smoke` is the fast gate for the real fleet-db-backed browser and
API paths: SSE connection, API-created issue delivery, and the Kanban card
status move without reload or issue-list refetch. `test-e2e-real-regression`
carries the slower cases. See
[Dogfood to Playwright Coverage](dogfood-playwright-coverage.md) for which
scenarios sit in which suite, and how a spec joins one (by `@smoke` /
`@regression` tag, not by filename).

The frontend half of `make gate` also runs `npm run check:arch`
(`check:loc` + `check:no-raw-fetch` + `check:no-hardcoded-urls` +
`check:boundaries`) and `npm run check:generated` — see
[test-infrastructure.md](test-infrastructure.md).

---

## Unit Tests (Vitest)

Most tests live in `__tests__/` directories beside the code they cover. 13 of
the 369 sit directly next to their source instead (e.g.
`src/components/AgentDetailMain/AgentDetailMain.test.tsx`); the `find` command
below catches both.

| Directory | Covers |
| --- | --- |
| `src/api/{common,issues,agents,workspace,terminal,workflows}/__tests__` | HTTP client, SSE client, per-domain API modules |
| `src/hooks/{common,issues,ui,workspace,agents,terminal,workflows}/__tests__` | Custom hooks by domain |
| `src/stores/__tests__` | Zustand stores — issue, agent, workspace, editor, backends, fileBrowser |
| `src/components/<Component>/__tests__` | One directory per component (~90 of them) |
| `src/contexts/__tests__`, `src/views/__tests__` | Context providers, route-level views |
| `src/utils/__tests__`, `src/utils/{issue,workspace}/__tests__` | Pure helpers |
| `src/types/__tests__`, `src/styles/__tests__`, `src/test-utils/__tests__` | Type guards, colour tokens, test helpers |
| `src/__tests__` | App-level tests, including `App.test.tsx` |

List them for real rather than trusting a table:

```bash
cd internal/webui/frontend
find src -name '*.test.ts' -o -name '*.test.tsx' | sort
find src -type d -name __tests__ | sort          # the structural view
```

### Entry points worth reading first

| File | Why |
| --- | --- |
| `src/stores/__tests__/issueStore.test.ts` | The core issue state machine — optimistic updates, SSE mutations during refetch |
| `src/api/common/__tests__/client.test.ts` | HTTP client: error mapping, retries, base-URL resolution |
| `src/api/common/__tests__/sse.test.ts` | SSE client: reconnect/backoff, event dispatch |
| `src/__tests__/App.test.tsx` | How the app is wired together, and what has to be mocked to render it |

### Issue state is a Zustand store, not a hook

`useIssues`, `useSSE` and `useAgents` no longer exist — earlier revisions of
this page documented roughly 2300 lines of coverage for them. Issue state moved
into a vanilla Zustand store: `src/App.tsx:241` reads "Issue state from Zustand
store (replaces useIssues hook)", and `src/stores/issueStore.ts:1-4` says it
"Replaces useState/useRef/useCallback composition from useIssues.ts with a
single testable, framework-agnostic store." The tests moved with it, into
`src/stores/__tests__/`.

---

## E2E Tests (Playwright)

`playwright.config.ts:97` sets `testDir: "./tests/e2e"`. Three families:

| Family | Path | Count | Backend |
| --- | --- | --- | --- |
| Browser E2E | `tests/e2e/*.spec.ts` | 92 | Route-mocked; no server |
| API E2E | `tests/e2e/api/*.api.spec.ts` | 12 | Real `loom serve` |
| Integration | `tests/e2e/integration/*.integration.spec.ts` | 13 | Real `loom serve` |

```bash
ls tests/e2e/*.spec.ts tests/e2e/api/*.api.spec.ts tests/e2e/integration/*.integration.spec.ts
```

### API E2E

`tests/e2e/api/api-client.ts` is a typed client wrapping Playwright's
`APIRequestContext`: `health` (`:368`), `stats` (`:380`), `listIssues` (`:404`),
`getIssue` (`:416`), `createIssue` (`:426`), `updateIssue` (`:438`),
`closeIssue` (`:450`), `addComment` (`:468`), `addDependency` (`:484`),
`removeDependency` (`:495`), `ready` (`:510`), `blocked` (`:522`),
`graph` (`:534`), `deleteIssue` (`:550`), `cleanupIssue` (`:559`). It also
exports `generateTestId()` (`:711`) for per-test isolation and `waitFor()`
(`:719`) for polling until SSE propagation lands.

The 12 spec files cover issue lifecycle and CRUD roundtrip, triage, dependency
management, finding work, review workflow, team collaboration, agent
monitoring, agent logs, task logs, project health, and realtime updates.

API tests verify the backend contract independently of the UI, so an API
regression is caught before it reaches the frontend.

### Integration

The 13 integration specs cover concurrent ops, cross-workspace move, daemon
recovery, dependency graph, dogfood regressions, kanban CRUD, lead epic runner,
playground, SSE multiclient, SSE updates, terminal parity, workspace files, and
workspace lifecycle.

### How the server gets provisioned

`tests/e2e/global-setup.ts` returns immediately unless `RUN_INTEGRATION_TESTS`
is set (`:41-45`), then takes one of three paths:

1. **local-server** — `LOOM_LOCAL_SERVER=1`: health-check the `loom serve` you
   already started at `LOOM_BASE_URL` (default `http://localhost:8080`) and
   return (`:47-66`).
2. **webServer (default)** — logs "webServer mode — Playwright manages server
   lifecycle", writes the state file and returns (`:68-81`). Playwright then
   runs `bash ../../../scripts/start-e2e-server.sh` with `E2E_PORT` (default
   8090, API) and `E2E_FRONTEND_PORT` (default 3100, Vite preview) and waits on
   the preview URL (`playwright.config.ts:70-95`, `:26-28`).
3. **Podman compose (legacy)** — only when `PODMAN_COMPOSE` is set: brings up
   `compose.e2e.yml` and waits on `:8080` and `:9000` (`:82-101`). **That
   compose file is not in this repo**, so this path only works if you supply
   one.

See [../testing-terminology.md](../testing-terminology.md) §Axis 3 for the
provisioning vocabulary.

### Playwright projects

`playwright.config.ts` defines eight:

| Project | Line | Selects |
| --- | --- | --- |
| `chromium` | `:128` | Route-mocked browser specs; ignores `*.integration.spec.ts` unless `RUN_INTEGRATION_TESTS` |
| `integration` | `:138` | All `*.integration.spec.ts` |
| `integration-smoke` | `:155` | Integration specs tagged `@smoke` (`:158`) |
| `integration-regression` | `:173` | Integration specs tagged `@regression` (`:176`) |
| `local-integration` | `:191` | Needs `RUN_LOCAL_INTEGRATION_TESTS=1` |
| `api` | `:208` | All `*.api.spec.ts` |
| `api-smoke` | `:220` | API specs tagged `@smoke` (`:224`) |
| `api-regression` | `:233` | API specs tagged `@regression` (`:237`) |

**CI settings**: `retries: 3` (`:100`), `workers: 1` and `reporter: "github"`
(`:101-102`), `timeout: 30000`, `forbidOnly`.

---

## Test Infrastructure

Fixtures, page objects and helpers live under `tests/`, **one level above**
`tests/e2e/`. `tests/e2e/helpers/` still exists but holds only `fleet.ts`.

### Fixtures (`tests/fixtures/base.ts`)

`test` extends Playwright's base with three fixtures (`base.ts:31-60`):

| Fixture   | Purpose                                        |
| --------- | ---------------------------------------------- |
| `mockApi` | API response mocking with request tracking (`:33-36`) |
| `mockSSE` | SSE connection simulation with event injection (`:39-44`) |
| `appPage` | Page with config + auth mocked, SSE intercepted, navigated and connected (`:47-60`) |

Import `test`/`expect` from `../fixtures`, not from `@playwright/test`. Also in
the directory: `index.ts`, `screenshot.ts`, `deterministic-terminal.sh`.

### Page Object Models (`tests/pages/`)

`app.page.ts` (`AppPage` — navigation, connection status), `kanban.page.ts`
(`KanbanPage:14` — column interaction, card counts, drag), `monitor.page.ts`,
`table.page.ts`, re-exported from `index.ts`. POMs encapsulate selectors, so a
UI change touches the POM rather than every spec.

### Test Helpers (`tests/helpers/`)

| File | Contents |
| --- | --- |
| `test-data.ts` | `resetIdCounter():23`, `createIssue():29`, `createStats():77`, `createKanbanData():148` |
| `api-mock.ts` | `createApiMockHandler():87` — the `mockApi` implementation |
| `sse-mock.ts` | `createSSEMock():36` — the `mockSSE` implementation |
| `fixture-routes.ts`, `wait.ts`, `keyboard-setup.ts`, `terminal-seed.ts` | Route fixtures, waits, input setup, terminal seeding |

### Mocking Strategies

| Strategy                      | Where Used      | Why                                                      |
| ----------------------------- | --------------- | -------------------------------------------------------- |
| `vi.mock('@/api')`            | Hook and store tests | Isolate logic from network calls                    |
| Custom MockEventSource        | SSE tests       | Simulate the browser EventSource API                     |
| `vi.hoisted()` mock pattern   | Component tests | Consistent mock access across test files                 |
| Heavy component mocking       | App tests       | Prevent ResizeObserver/terminal renderer issues in jsdom |
| Playwright route interception | Browser E2E     | Control API responses without a server                   |

### Key Testing Patterns

1. **Race condition prevention**: store tests verify SSE mutations arriving
   during an in-flight refetch are not clobbered.
2. **Fake timers**: `vi.useFakeTimers()` / `vi.advanceTimersByTime()` for
   controlled async.
3. **Accessibility-first queries**: `getByRole()`, `getByLabelText()` over
   `getByTestId()`.
4. **Optimistic update testing**: verify the immediate UI update, then verify
   rollback on API failure.
5. **Functional state updates**: `setState(prev => ...)` so a stale snapshot
   cannot clobber a newer one.

Full conventions with code examples: [test-patterns.md](test-patterns.md).

## Related

- [test-patterns.md](test-patterns.md) — how to write a test here
- [test-infrastructure.md](test-infrastructure.md) — CI, Makefile targets, coverage thresholds
- [dogfood-playwright-coverage.md](dogfood-playwright-coverage.md) — smoke/regression suite membership
- [../testing-terminology.md](../testing-terminology.md) — depth / realness / provisioning / polarity
- [README.md](README.md) — testing docs index
