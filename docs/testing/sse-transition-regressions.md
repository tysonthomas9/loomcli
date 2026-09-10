# SSE UI transition regression coverage

The suite catches transient content loss during background refresh, stale
responses crossing selection/workspace/repository boundaries, and real stream
recovery. A final screenshot alone is insufficient: the browser observer starts
before the action and remains active through response completion and rendering.

## Additional product bugs found

Four distinct root causes were reproduced while implementing this suite:

1. Standalone issue detail replaced loaded controls during same-issue refresh.
   Retain only a full detail snapshot matching the selected issue identity.
2. Agents collection refresh cleared its selected full detail. Retire detail on
   workspace/agent scope changes; collection updates must not reset selection.
   Collection rows never substitute for inaccessible full detail.
3. Graph response mapping discarded `source_repo`; repository filtering then
   removed every returned node. Preserve the canonical source field and its
   frontend `repo` mapping, as the list adapter already does.

4. Completed-session notifications omitted `workspace_id`, so the realtime hub
   dropped them and Runs depended on polling. Send the resolved workspace with
   the task/session identifiers and assert literal JSON keys. Remove the
   guaranteed-invalid pre-claim notification with no task ID; this does not
   introduce a taskless session-start protocol.

These counts exclude the original panel flicker and authorization retry leak,
which motivated the work, and exclude test-harness failures. Original local
browser recordings and traces are retained locally and are not part of this PR.

## Layers and exact cases

| Layer                          | Coverage                                                                                                                                    |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Component/API/hook             | Snapshot eligibility, access retirement, identity changes, graph repository mapping                                                         |
| Continuous browser observation | Node identity, detached subtree removal, transient loading states, forbidden foreign content, bounded evidence                              |
| Controlled browser timing      | Held/failed/overlapping reads, scope reversal, refresh/retry, unsaved draft/value/caret/focus/tab retention                                 |
| Real services                  | Same manifest on Redis and PostgreSQL, actual SSE and HTTP completion evidence, reconnect/cursor behavior, draft and filtered Graph refresh |
| Deterministic agent workflow   | One UI-created task, planner design and completed run, UI approval, coder completion, actual transcript and diff rendered in Runs           |

Version 2 of `scripts/sse-ui-transition-manifest.tsv` requires 22 mocked cases
and 8 paired cases per store. The verifier rejects missing, extra, skipped,
retried, flaky, wrong-file or duplicated cases. Source identities are recorded
before and after execution. A mocked run owns its frontend; it cannot silently
reuse another checkout's Vite server. A paired run verifies its own Compose
resources and actual storage/runtime identity before running the tests.

The workflow backend is `localdogfood`: real local services and deterministic
agent orchestration. This does not claim execution by a paid external model.
Ordinary resync has mocked coverage; a certified expired-cursor recovery browser
case is outside this manifest until a supported producer trigger is specified.

## Reproduction

Install frontend dependencies and Playwright Chromium first. From the Loom root:

```sh
SSE_UI_BACKEND=mocked SSE_UI_RUN_ID=my-mocked-run make test-sse-ui-transitions
SSE_UI_BACKEND=redis SSE_UI_RUN_ID=my-redis-run FLEET_DB_REPO=/path/to/fleet-db make test-sse-ui-transitions
SSE_UI_BACKEND=postgres SSE_UI_RUN_ID=my-postgres-run FLEET_DB_REPO=/path/to/fleet-db make test-sse-ui-transitions
make test-sse-ui-transition-runner
```

Use a new run ID each time. Fleet HEAD must match the reviewed full SHA in
`scripts/sse-ui-transition-fleet-revision`, or an explicitly supplied
`FLEET_DB_REVISION`. Paired runs build their own stack and clean only resources
owned by that run. Reports live in `tmp/sse-ui-transitions/<run>/<backend>/`.
The selected cases and exact source/runtime identities are recorded alongside
results and failure evidence. Successful hosted jobs also upload summaries.

## Hosted rollout

Loom PRs run mocked coverage and both stores against a reviewed Fleet pin.
Fleet PRs run both stores against a reviewed public Loom pin; the Fleet direction
uses the repository's own read-only token and needs no cross-repository secret.

At the last inspection, Loom had no `FLEET_DB_REPO_TOKEN` secret, and Fleet jobs
were rejected before starting due to GitHub billing/spending restrictions.
These are hosted execution blockers, not passing tests or product failures.

After those are resolved and both matrices run green on the published revisions:

1. Require `UI transition regressions (mocked)`, `SSE UI transitions (redis)` and
   `SSE UI transitions (postgres)` for Loom's protected target branches.
2. Require both `SSE UI transitions` store jobs for Fleet's protected target.
3. Preserve existing branch rules and verify an intentionally failing regression
   blocks merging. Do not enable unavailable checks prematurely.

Workflow YAML and local green evidence do not establish hosted enforcement.
Final local run results and publication revisions are recorded in the PR.
