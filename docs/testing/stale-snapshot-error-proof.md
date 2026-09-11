# Stale snapshot error presentation and E2E coverage

The issue store retained collection data after transient refresh failures, but IssueViewGuard unconditionally replaced the view whenever error was set. The stale-state banner therefore described content the UI no longer displayed.

The store now certifies whether an error belongs to a previously committed snapshot in the same scope. The guard preserves that view (including a successfully loaded empty result) and displays “Unable to refresh — showing last known state” with Retry now. The warning remains through retry requests and clears after a successful read. Initial failures, scope changes, permanent errors and explicit recovery remain blocking. Permanent errors and explicit recovery retire the old snapshot's eligibility until a new read succeeds, including when a later ordinary retry fails transiently. All four issue views consume the same policy.

## Verification

- Mounted-component regression failed before the fix because the board node disappeared; it now asserts node identity and the nonblocking warning.
- Store tests cover populated/empty snapshots, first-load failure, scope changes, explicit recovery, permission failure and retry after retired ownership.
- Agent-browser 0.37.1: UI reopen of SSE-MANUAL-0909-1 reached the observer while collection requests were deliberately aborted. Observer showed exactly one Open card, a refresh warning, and no “Failed to load data” screen. Removing the abort and clicking Retry now cleared the warning while retaining one card. Saved assertions are in [evidence](evidence/stale-snapshot-error/). Failed navigation and uncommitted status probes were excluded. This is DOM proof, not a 60 fps flicker recording.
- The automated PostgreSQL suite uses real Loom/FleetDB/PostgreSQL/Redis services with deterministic localdogfood, no paid inference. Its multiclient test now aborts only collection requests after initial load, receives the real SSE mutation, asserts readable stale content, interrupts the owned proxy, verifies both resume cursors and replay, and checks the refresh warning clears. SSE responses are never mocked. Fixture creation/mutations in this automated suite use product APIs; the separate agent-browser proof uses UI actions.

Final validation: 165 focused tests passed, frontend build/typecheck passed, scoped lint passed (one existing App hook warning), and all seven PostgreSQL SSE tests passed with zero skips or retries. Independent review found no remaining blockers after the snapshot-retirement correction.

## Repeatable E2E command

With this run's existing stack:

```sh
LOCAL_MODE_COMPOSE_PROJECT=loomcli-pg-browser-manual-0909 \
LOCAL_MODE_API_PORT=8582 LOCAL_MODE_UI_PORT=8583 \
make local-mode-postgres-sse-verify
```

For a fresh stack, choose a dedicated `loomcli-pg-browser-*` project and compatible FleetDB checkout, run `make local-mode-postgres-up`, then the verifier with the same project and ports. See [local-mode setup](../../test/local-mode/README.md). The verifier selects both SSE spec files, supplies the integration and proxy flags, uses one worker and zero retries, and writes the JSON report under `internal/webui/frontend/test-results/pg-sse-report.json`. It rejects a proxy whose ownership label does not match the selected project. The test restores the proxy in finally.

## CI gap and recommended gate

This command is automated and locally executed; it is not currently a required PR CI job. The ordinary Playwright PR workflow runs mocked browser tests and cannot establish PostgreSQL SSE correctness. To enforce this in CI, add a required paired-stack job that checks out the compatible private FleetDB revision, boots the PostgreSQL local-mode stack, runs the verifier, uploads the JSON report and browser attachments even on failure, and tears down only its own project. Fail the job on skipped/interrupted tests as well as failures; expect all seven tests to pass. The runner needs authorized private-repository checkout access and Podman because the socket-fault helper invokes Podman directly. Do not claim the PR browser check covers this suite until that job is configured and runs successfully.
