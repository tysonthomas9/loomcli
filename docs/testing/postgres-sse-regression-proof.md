# PostgreSQL SSE browser regression proof

The paired final implementation passed **7 real browser tests in 32.2s**, with
zero skips, retries or flaky results, on September 6, 2026. Tested source is Loom
`c9267da8e` (branch `test/pg-sse-regressions`, PR #682) and FleetDB `a82180b2`
(branch `feat/pg-public-issue-routing`, PR #288). Later documentation commits do
not change tested production or test code.

## Evidence and scope

[Results and attachment index](evidence/pg-sse-final-0906/results.json) and
[original browser output](evidence/pg-sse-final-0906/browser-run.log) retain the
actual run. JSON attachments contain sanitized observations of the application's
Fetch SSE bytes through Chromium CDP; PNG attachments capture the rendered UI.
No browser responses were replaced, no extra SSE stream was opened, and no
manual database enrollment supplied the proof.

| Scenario | Observed result |
| --- | --- |
| Two-client delivery | Both independent contexts received the same create and claim IDs exactly once in the observed interval. |
| Real proxy interruption | Each captured stream failed; both clients reconnected HTTP200 with the exact preceding c2 Last-Event-ID and no since query. Both received the same four ordered actions: claim, update, update, assign. The disconnected update arrived before connected. |
| Initial connection | Actual Fetch200, attached byte observer and connected frame. |
| Create | Card rendered before a collection response could repair it, followed by bounded projection refresh. |
| Claim and release | Status moved before collection response; exact claim/release actions and later authoritative refresh. |
| Rapid creates | Three deliberate creates each yielded one scoped unique mutation ID and rendered card. |
| Close | Card left Open through the observed mutation without a document reload. |

Independent review decoded both reconnect traces and checked exact IDs, stream
failures, cursor headers, action order, workspace/issue identity and absence of
probe errors. This is a finite delivery/replay proof, not a general exactly-once
guarantee or whole-client cache publication/reset-acknowledgment proof.

The standard local-mode product entrypoint bootstrapped the real services.
Each spec then created a fresh workspace through an actual POST201, registered
the existing source repository and verified that no agents were assigned there.
All test tasks and mutations used normal workspace-scoped product APIs. Existing
localdogfood agents stayed in their own workspace. The updates spec created one
baseline task so the initially empty board had columns; it closed that task
after the suite. Whole-project teardown removed the disposable workspaces.

The manual agent-browser check used a dedicated profile and the separate
SSE-VISUAL-0906 workspace. UI controls created a task, moved it to In Progress,
then Review and Closed. The API agreed with the visible Review state and final
Closed state. See [Review screenshot](evidence/pg-sse-final-0906/browser-visual-review-0906.png),
[DOM snapshot](evidence/pg-sse-final-0906/browser-visual-review-0906.txt), and
[API result](evidence/pg-sse-final-0906/browser-visual-api-0906.json).
This manual check supplements the passive stream proof; DOM alone is not used
to establish replay correctness.

## Bugs exposed and repaired

The old browser tests waited for a retired connection indicator, required no
collection refresh despite the production derived-state contract, and could
pass reconnect through page reload. The replacements observe real frames,
require mutation rendering before collection responses, and preserve the normal
debounced projection refresh (one second, five-second maximum under load).

The browser then exposed an enrolled public claim HTTP500. FleetDB now routes
claim/full release through existing committed owners. A real PostgreSQL HTTP
regression later exposed a second bug: deleted issues leave operational leases,
so treating a surviving lease as corruption made late cleanup return500. The
final fix locks the lease independently, retains foreign-owner rejection and
performs caller-only deletion without changing issue/worker/source/receipt state.
The complete HTTP regression passed with race detection in 2.232s; final service
race tests, build, vet, scoped lint and harness gates passed separately.

The first recovered browser run had three passes followed by a create-case
failure: an autonomous planner legitimately claimed the test task, adding a
second distinct mutation. Dedicated workspaces resolve this interference;
event-count assertions were not weakened. The final run above includes all seven
cases without retries.

Hosted Go coverage/macOS failures also exposed three app HTTP fixtures using
obsolete c1/numeric cursors without source identity. Strict production rejection
was correct. Updated c2/s1 fixtures, exact replay-ID checks, synchronized header
capture and fail-closed negative cases passed focused race tests (1.754s) and the
full app package (13.288s). Eight hosted checks passed on predecessor `4eadce658`,
including mocked browser **379 passed / 118 skipped**. That predecessor CI result
is not final-head CI or the real PostgreSQL browser proof.

## Reproduce

Use a dedicated `loomcli-pg-browser-*` Podman project and the paired final FleetDB
source. Start through `make local-mode-postgres-up` with the matching project,
ports and `LOCAL_MODE_FLEETDB_BUILD_CONTEXT`; wait for the product entrypoint to
report ready. This run used Fleet8580, API8582 and UI8583:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-regression-0905 \
LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH='/Users/tyson/.agent-browser/browsers/chrome-151.0.7922.34/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing' \
PLAYWRIGHT_JSON_OUTPUT_FILE=/private/tmp/sse-stack-review/browser-regression-isolated-report-0906.json \
make local-mode-postgres-sse-verify
```

The source repository defaults to `/workspace/source-repo` inside the container;
`LOOM_SSE_TEST_SOURCE_REPO` overrides it. The proxy test verifies its exact
container ownership label, stops/starts only that proxy and restores it in
finally. An interrupted test process can still require owned-proxy restoration.
The auth-disabled local stack uses a non-secret harness key. No paid AI was
called; this is not an authentication or real AI-backend proof.

## Recovery and cleanup

The user approved restarting the shared VM after disk exhaustion. Podman's
normal stop fell back to a hard stop; a stale VM proxy was removed and a detached
launcher kept the restarted VM alive across tool sessions. No VM disk was
removed or recreated. The existing separate PostgreSQL proof fixture recovered
without rebuilding its data. The browser project's disposable data was recreated
through its normal down/up targets before the final test build.

After verification, the dedicated browser session/profile was removed and
`make local-mode-postgres-down` removed only `loomcli-pg-browser-regression-0905`
and its volumes. A label-filtered container check returned no remaining project
containers. The separately started `fleet-projection-proof-0904-pg` was stopped,
not removed; its data is preserved. The shared VM remains running. Other projects
were not restarted or removed by test cleanup.

## Remaining work

The broader architecture is unfinished: whole-client atomic publication and
exact reset acknowledgment, historical enrollment/rebuild, remaining public
lifecycle routing, external-view coverage, Redis correctness/durability proofs,
generated canonical contracts and executable cross-repository CI remain tracked
in FleetDB's progress document. Expiry, source replacement and workspace-switch
fault scenarios are not certified by these seven tests. No merge or deployment
was performed.
