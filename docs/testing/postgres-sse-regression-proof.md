# Current Fetch SSE browser regressions

This follows Loom #680 / Fleet #287. Work branches are `test/pg-sse-regressions`
and `feat/pg-public-issue-routing`, based on Loom
`304f65d03f8a609c032b6ab47f880fc0b9e68df1` and Fleet
`06eeedfd200f4399724464e3fbdce49b7e64401b`. Runs span September 5–6, 2026 Pacific.

## Why the old tests were insufficient

The unchanged status browser test failed waiting for `[data-state="connected"]`,
which is absent from the current UI. Its permanent zero-collection-refetch
requirement also disagreed with production: the issue store applies the mutation
immediately, then debounces authoritative projection refresh (one second; five
seconds maximum under sustained events). Derived readiness/blocking data are
not all present in SSE. Removing the refresh would leave derived state stale.
The old multiclient recovery tests navigated away/back and reloaded on failed
assertions, which could turn an ordinary page fetch into apparent stream recovery.

The revised existing specs observe the application's actual Fetch response bytes
through Chromium CDP `Network.streamResourceContent`. They do not replace fetch,
mock responses, or open another SSE connection. Buffered bytes precede subsequent
chunks; the parser handles fragmented UTF-8 and CRLF and does not count incomplete
records. Request evidence excludes credentials and token query strings. Full
mutation data are retained only for registered run-owned issue IDs; metadata are
bounded and observer failures fail the test.

The assertions require real connected frames, source-bound mutation IDs and
workspace/action identity. Create and claim/release rendering must occur before
any new collection response arrives, followed by bounded projection refresh.
Two independent contexts must receive the same deliberate mutation sequence.
The outage test stops only the explicitly selected project proxy, requires a
new failure for each exact active stream request, and resumes with the previously
observed cursor in Last-Event-ID. No document navigation/reload may repair it.
These are finite, controlled-sequence delivery checks, not a universal exactly-once
guarantee or a whole-client recovery publication proof.

## Real failure exposed after repairing the observer

The status test then reached a real 500: Loom PATCH `in_progress` calls Fleet
`/claim`, whose public service still invoked the forbidden legacy lock writer.
The paired Fleet branch routes persisted enrolled workspaces to the existing
atomic claim/full-release owners and adds a distinct operational lock-only owner.
It preserves Review/Closed state and handles late cleanup after deletion without
masking an orphan lease. Admission and receipts are not weakened; the test still
requests `in_progress`. See Fleet's `docs/postgres-public-issue-routing-proof.md`.

## Execution evidence and current limit

Evidence coordinates: real local product stack and PostgreSQL, isolated Podman
project `loomcli-pg-browser-regression-0905`, deterministic localdogfood agent
backend, browser/HTTP integration with positive delivery and negative API checks.
No paid AI service was used and no database state was hand-seeded for the browser.
Ports are Fleet8580, API8582 and UI8583. The ordinary PostgreSQL Make target built
and bootstrapped the stack; supported API helpers create and close test issues.

- The [unchanged test failed on its retired selector](evidence/pg-sse-regression-0906/retired-indicator-failure.log).
- The initial new observer accidentally counted SPA URL normalization as a reload;
  it now counts actual main-frame document requests. That harness failure was not
  counted as product evidence.
- The corrected observer exposed [the public claim 500](evidence/pg-sse-regression-0906/public-claim-failure.log).
- [Connection and create passed, 2 tests / 4.0s](evidence/pg-sse-regression-0906/connection-create-pass.log).
- [Rapid create and close passed, 2 tests / 5.8s](evidence/pg-sse-regression-0906/rapid-close-pass.log).
- [The passive parser passed 3 focused tests](evidence/pg-sse-regression-0906/probe-parser-pass.log).
- Focused browser-test TypeScript compilation and ESLint passed. Independent review
  required response-arrival timing, scoped actions and exact failed-stream IDs;
  those assertions are present in the final source.
- Fleet's preceding HTTP sequence/rollback proof passed in 2.560s. Its final
  service race tests, build, vet, scoped lint and harness gates passed. The last
  deleted-issue/orphan refinement was independently reviewed but its PostgreSQL
  rerun could not execute after the resource failure below.

**The final seven-test browser suite has not passed yet.** Four intermediate
browser cases passed before the final public routing build and strengthened
assertions. The in-progress/release and multiclient outage cases must pass against
the final paired source before acceptance. No final screenshots or final-head
browser success are claimed from those earlier runs. Hosted CI is separate.

Host disk exhaustion prevented Go from creating a work directory. Removing only
8 GiB of older files from this run's disposable Go cache restored host space, but
the proof PostgreSQL then reported SQLSTATE58030 (`pg_filenode.map` I/O error), and
Podman's shared VM began failing SSH handshakes. The VM still reports Running;
our API returns connection reset. No shared VM restart or data deletion has been
performed. A shared restart requires explicit approval under the goal's shared
resource constraint. The normal final browser rerun remains pending runtime
recovery; the broader goal is active.

## Reproduce the final gate after runtime recovery

The paired Fleet build must contain the public routing changes. Start a fresh,
run-owned PostgreSQL local-mode project with the normal Make workflow, then:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-regression-0905 \
LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
make local-mode-postgres-sse-verify
```

The target requires Podman and an owned `loomcli-pg-browser-*` project. It verifies
its exact UI container's Compose ownership label, stops/starts that proxy only,
and restores it in `finally`. A process kill can still require restoring that
run-owned proxy. It runs both existing SSE integration files with one worker and
zero retries; JSON evidence is written to `internal/webui/frontend/test-results/pg-sse-report.json`.
Use `PLAYWRIGHT_JSON_OUTPUT_FILE` to preserve the report elsewhere and
`PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` for an installed compatible Chromium.

The local-mode auth-disabled fixture explicitly uses a non-secret key value so
the harness never reads the host's real API key. This is not an auth-security
proof. Screenshots and sanitized passive traces are test attachments; do not
publish general HARs or credentials.

The runtime outage prevented final teardown of this run's Compose project.
After recovery, remove only `loomcli-pg-browser-regression-0905` with
`make local-mode-postgres-down` and its matching project name. The older proof
PostgreSQL container and foreign lifecycle services must not be included in that
cleanup. Preserve the reported failures and rerun the exact final PostgreSQL HTTP
proof before treating the paired fix as fully validated.

## App HTTP fixture repair after hosted CI

At `beff9556c`, Linux Go coverage and macOS tests exposed three stale app-level
SSE fixtures: mutation delivery, catch-up and Last-Event-ID. Their c1/numeric
cursors and absent source identity correctly triggered the current handler's
resync path before page delivery. The repair supplies explicit c2/s1 fixtures,
checks exact replay IDs, and replaces unsynchronized header capture with a
channel. Negative cases preserve rejection of c1/numeric cursors and absent
identity; production validation is unchanged. The header test uses no query,
matching browser reconnect. The endpoint's established opaque-query precedence
is unchanged.

The original three failures were reproduced locally. The repaired `TestSSELive`
selector passed with race detection (1.754s), and the full affected app package
passed (13.288s):

```sh
GOCACHE=/private/tmp/loom-sse-integration-go-cache go test -race -p 1 ./internal/webui/app -run TestSSELive -count=1 -timeout=90s
GOCACHE=/private/tmp/loom-sse-integration-go-cache go test -race -p 1 ./internal/webui/app -count=1 -timeout=120s
```

This is real local HTTP with deterministic mutation-source fixtures, not a
PostgreSQL or browser proof. Independent review covers the final fixture repair;
new hosted results and the paired browser gate remain separate requirements.
