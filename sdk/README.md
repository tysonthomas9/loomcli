# @loom/sdk

Workflow SDK for Loom. Two clients, one package:

- **`@loom/sdk/driver`** — the driver client used *inside* a workflow run. A
  workflow is an ES module executed under a registered driver bundle; it talks
  to the Loom serve driver-op HTTP API with a run-scoped token.
- **`@loom/sdk/runner`** — the task-run client used by runner harnesses
  (claiming env, logs, artifacts, completion) against fleet-db.

All wire fields are **camelCase**. The frozen v1 surface ships with the
package as [`api-surface.v1.json`](./api-surface.v1.json) and is enforced by
contract tests on both sides of the wire.

> **Package name.** The intended name is `@loom/sdk`. If the `@loom` npm scope
> turns out not to be ours, the decided fallback is
> **`@browseroperator/loom-sdk`** — same contents, same versioning. Scope
> ownership must be verified from an authenticated machine before the first
> publish (see [Publishing](#publishing-maintainers)).

## Install

```sh
npm install @loom/sdk
```

Until the package is published, vendor it: `driver.js` is a **single file with
zero local imports** and can be embedded as-is (this is the path the driver
bundle scripts use). `runner.js` imports exactly one local file,
`internal.js`. A test (`package.test.mjs`) pins both facts.

## Quickstart: a workflow

A workflow is an ES module with a default export. The driver executes it under
a registered bundle; `createLoomClient()` picks up everything it needs from
the environment the driver injects.

```js
// my-workflow.mjs
import { createLoomClient, isWorkflowSuspended } from "@loom/sdk/driver";

export default async function run() {
  const loom = createLoomClient();
  // `local-task-runner` is the real local runner (see "Runners" below): it
  // executes the user-selected backend CLI over the prepared worktree and
  // requires that CLI + its auth locally. Fail-closed if either is missing.
  const runner = loom.input.runner || "local-task-runner";

  // Fan out task runs, then push-follow the epic until they settle.
  await loom.taskRuns.request({ taskId: "task-1", runner });
  await loom.taskRuns.request({ taskId: "task-2", runner });

  for await (const ev of loom.epics.watch({ epicId: loom.input.epicId })) {
    if (ev.type === "closed") break;
    if (ev.type === "taskRun") console.error("progress:", ev.data);
  }

  return loom.completed({ summary: "both tasks settled" });
}
```

## Runners

`loom.taskRuns.request({ taskId, runner })` dispatches a child `TaskRun` to a
named runner. The built-in runners are:

| Runner | What it does |
| --- | --- |
| `local-task-runner` | **The real local runner.** Executes the user-selected backend CLI (`claude` / `codex` / `opencode` / `gemini` / `cursor`) over the prepared worktree, parses its output into a transcript + patch, and reports the CLI's real exit status. It requires the selected backend's CLI **and** its auth (e.g. `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`/`CODEX_API_KEY`, `GEMINI_API_KEY`, `CURSOR_API_KEY`) to be available on the host. If the worktree is missing, the backend is unsupported, the CLI is not on `PATH`, auth is missing, or the CLI exits non-zero, it **fails closed** (`local_worktree_missing`, `local_backend_unsupported`, `local_backend_unavailable`, `local_backend_auth_missing`, `local_agent_failed`) — it never fabricates a "completed" result. |
| `daytona-task-runner` | Runs the task on the **Flue runtime** inside a Daytona sandbox. |
| `openshell-task-runner` | **Unimplemented.** Fails closed with `openshell_runner_unimplemented`; it is excluded from generated manifests and is not selectable. |

The backend that `local-task-runner` runs comes from the Settings "Project
Default Backend" (`codex` by default), with an optional per-agent override; the
host resolves it and injects `LOOM_TASK_RUNNER_BACKEND` into the runner.

A workflow that defaults `loom.input.runner || "local-task-runner"` is therefore
opting into **real local backend-CLI execution with local auth**. To run
elsewhere, pass `runner` explicitly (e.g. `"daytona-task-runner"`).

> **No fake/no-op default.** `local-task-runner` is no longer a stub. A task run
> only reports `completed` when its runner produced real execution evidence; an
> empty/missing runner result is rejected as `invalid_task_result` rather than
> normalized to success.

### Test/demo gates (disabled by default)

Two behaviors exist only for tests/demos and are **off** unless explicitly
enabled by an operator environment variable:

| Env var | Effect when `=1` |
| --- | --- |
| `LOOM_DRIVER_ENABLE_TEST_NOOP_PROVIDER` | Enables the `noop` test provider that auto-completes a task run. Disabled (default) and `noop` requested → fail closed with `provider_unsupported`. |
| `LOOM_DAYTONA_TASK_RUNNER_ENABLE_DEMO_MODES` | Enables the Daytona `e2e-smoke` / `slack-pr-chain` demo modes. Disabled (default) and one of those modes requested → `daytona_demo_mode_disabled`. |

Durable waits suspend the run instead of blocking it — let `WorkflowSuspended`
propagate (or rethrow it) and the platform resumes the run when the event
arrives:

```js
try {
  const res = await loom.events.await({
    pattern: "pr.merged",
    instanceKey: pr.url,
    timeoutMs: 60 * 60 * 1000, // REQUIRED — refused with await_timeout_required otherwise
  });
  if (res.status === "timed_out") return loom.needsReview({ summary: "PR never merged" });
} catch (err) {
  if (isWorkflowSuspended(err)) throw err; // suspend; the run resumes here
  throw err;
}
```

Determinism rule: `awaitIndex` derives from **call order**, shared between
`events.await` and `workflows.await`. On resume your module re-runs from the
top, so keep await calls in a stable order.

## Authentication

Workflow calls are **token-only**: a run-scoped bearer token minted by serve
when the run is claimed, injected as `LOOM_RUN_TOKEN`. The client then sends
`Authorization: Bearer <token>` and **no** `X-Loom-Driver-*` headers. There
are no ambient credentials — the token is the run's identity.

- **TTL = maximum run duration.** Default 24h; operators tune it with
  `LOOM_RUN_TOKEN_TTL` (Go duration) on serve. Expiry is the hard runtime
  ceiling, not the security boundary.
- **Revocation is fenced-run verification.** Every call re-verifies the run
  against the live lease, regardless of expiry. A token for a fenced,
  completed, or superseded run is rejected immediately; the TTL only bounds
  how long a stolen token for a still-live run could be replayed.
- **401 `token_expired`** means the run exceeded its maximum duration (or the
  token outlived the run). It is **never retryable**: do not retry, return a
  terminal result. A fresh token only exists for a fresh claim.
- The legacy header-quad (`X-Loom-Driver-Run-Id` / `-Node-Id` / `-Lease-Id` /
  `-Fencing-Token` + `Authorization`) is retained for CLI/ops tooling only;
  workflow code should never set it.

Client env (injected by the driver — you normally set none of these):

| Variable | Meaning |
| --- | --- |
| `LOOM_RUN_TOKEN` | Run-scoped bearer token (enables token-only auth) |
| `LOOM_DRIVER_API_URL` | Base URL of the serve driver-op API |
| `LOOM_DRIVER_WORKSPACE` | Workspace the run belongs to |
| `LOOM_DRIVER_RUN_ID` | Run id (legacy transport only; implied by the token) |

## Operation reference

Full signatures and JSDoc live in [`driver.d.ts`](./driver.d.ts) — the published
types are the reference. Namespaced surface of `LoomDriverClient`
(`createLoomClient()` / `createLoomDriverClient()`):

| Namespace | Operations |
| --- | --- |
| `epics` | `get`, `snapshot`, `watch` (SSE push: `snapshot` / `taskRun` / `closed`, resumes via `Last-Event-ID`) |
| `agents` | `list`, `orchestrationSession`, `updateParent`, `deliverAssignment`, `message` |
| `tasks` | `claimReady`, `complete`, `release` |
| `taskRuns` | `request`, `get`, `await` (client-side polling; prefer `epics.watch`), `active`, `recoverStale` |
| `connectors` | `github.merge` / `github.postReview` (require `expectedHeadSha`), `github.readPullRequest`, `github.listPulls`, `github.compare`, `github.postIssueComment`, `slack.post`, `slack.readConversations`, `datadog.readMonitors`, `datadog.readAlert`, `datadog.declareIncident`, `dispatch` |
| `events` | `await` (durable; throws `WorkflowSuspended` on suspension), `list` |
| `workflows` | `start`, `await` (shares the awaitIndex counter with `events.await`) |
| results | `completed`, `failed`, `needsReview` — terminal statuses `completed` / `failed` / `needs_review` / `cancelled` |

Flat method aliases (`requestTaskRun`, `watchEpic`, …) exist for older
workflows; new code should use the namespaces. The runner entry exports
`TaskRunClient`, `ArtifactHandle`, `RunnerEnv`, `LoomAPIError`, and
`AgentExecSpecError`.

### Agent invocation forms

`TaskRunClient.agent.exec` records one **Agent Invocation** at the moment its
leaf starts the backend process. It is not a generic command-capture helper:
deterministic checkout, diff, and test commands must not call it and therefore
create no AgentSession.

```js
const invocation = await runner.agent.exec({
  invocationKey: "agent",
  backend: "codex",
  model: "gpt-5",
  argv: ["codex", "exec", "..."],
  transcript: "stream-json",
  redactSecrets: [process.env.GITHUB_TOKEN], // explicit values; never inferred
});
```

Declared-secret redaction applies to the canonical transcript entries and the
uploaded transcript artifact. The returned `stdout` and `stderr` are raw so the
leaf can interpret the backend process result; do not return those fields from
the leaf unless its own output policy permits them.

Process failures are returned in `invocation` (`exitCode`, `timedOut`,
`spawnError`) rather than thrown. Only invalid caller input throws
`AgentExecSpecError`. The helper opens with two retries by default; if all
opens fail, it still runs the process and returns `session.degraded: true`, a
machine-readable `session.degradedReason`, and
`runtimeMetadata.observability_degraded: "true"` plus
`runtimeMetadata.observability_degraded_code`. Merge that returned metadata into
the leaf's final TaskRun result; the helper also best-effort heartbeats it while
the task-run lease is live.

`close: "auto"` uploads the transcript and closes the session before the result
returns. With `close: "deferred"`, the helper sends no close: the leaf must
call `await invocation.finalize({ status, summary, metadata })` on every return
path. A crash before that call intentionally leaves the session open for the
task-plane reconciler.

For a leaf whose agent loop is already in its own process (for example, a Flue
harness using a sandbox only as its tool backend), use the separately named
invoke form. It is deliberately not an optional-`argv` variant of the process
form:

```js
const collector = createFlueTranscriptCollector();
const session = await harness.session("task-123");
const invocation = await runner.agent.exec.invoke({
  invocationKey: "agent",
  backend: "codex",
  model: "gpt-5",
  transcriptCollector: collector,
  redactSecrets: [process.env.GITHUB_TOKEN],
  invoke: () => session.prompt(prompt), // leaf-owned prompt call
});
```

`exec.invoke` opens the session immediately before the prompt, snapshots the
canonical collector entries after it settles, derives usage only from
`response.usage`, uploads `transcript-<taskRunID>-a<attempt>-<invocationKey>`,
and auto-closes before it resolves. A rejected prompt returns as
`invocation.invokeError`; it is not an SDK throw. Missing usage remains
unknown/null rather than zero. If transcript upload fails, the helper marks
observability degraded and still closes the session without a transcript ref.

The invoke collector is intentionally held in host memory until the prompt
returns. A leaf crash or OOM mid-prompt loses the partial transcript/usage; the
task-plane reconciler stamps the unclosed session. Do not wrap deterministic
sandbox commands (clone, checkout, diff, tests, and similar setup) in either
agent-exec form: only agent prompt/process invocations create AgentSessions.

## Errors and retries

Every non-2xx driver-op response is a `DriverApiError` carrying the frozen
envelope: `{ code, message, retryable, details? }`. The `code` union is
published as `DriverApiErrorCode`.

**The SDK ships no automatic retry.** `retryable` is the server's guidance;
honor it with your own loop:

```js
import { DriverApiError } from "@loom/sdk/driver";

async function withRetry(fn, attempts = 3) {
  for (let i = 0; ; i++) {
    try {
      return await fn();
    } catch (err) {
      if (!(err instanceof DriverApiError) || !err.retryable || i >= attempts - 1) throw err;
      await new Promise((r) => setTimeout(r, 250 * 2 ** i));
    }
  }
}
```

| Code | Meaning / guidance |
| --- | --- |
| `timeout`, `unavailable`, `rate_limited` | Transient; safe to retry with backoff when `retryable` is true. |
| `upstream_error` | Connector provider failure; retry only if `retryable`. |
| `token_expired` | Run exceeded its max duration. **Never retryable** (the client pins this even if a response claims otherwise). Terminal. |
| `unauthenticated`, `identity_mismatch` | Credentials wrong or not this run's. Terminal; fix the environment. |
| `not_owner`, `stale_subject`, `conflict`, `invalid_transition` | Lost a race or acting on stale state. Re-read state; do not blind-retry. |
| `precondition_required` | Irreversible connector action missing its freshness assertion (e.g. `expectedHeadSha`). Thrown synchronously, before egress. |
| `grant_denied` | Connector grant does not allow this action. Terminal. |
| `await_timeout_required`, `await_pattern_unscoped`, `await_instance_key_malformed`, `await_actor_forbidden` | Await contract violations. Fix the call. |
| `driver_run_already_resumed`, `composition_depth_exceeded` | Workflow composition limits. Terminal. |
| `invalid`, `not_found`, `unknown_op`, `unschedulable`, `canceled`, `internal` | Standard request/state errors; retry only if `retryable`. |

## Versioning and breaking-change policy

Semver, with the wire contract as the compatibility unit:

- **Major**: any change to camelCase wire field names/shapes of a frozen op,
  the error envelope (`code`/`message`/`retryable`/`details?`) or the meaning
  of an existing error code, the SSE event types, await/suspend semantics, or
  removal of an export.
- **Minor**: new ops, new exported types, new error codes, additive optional
  fields.
- **Patch**: fixes that change no wire bytes and no types.

The frozen surface is machine-checked: `api-surface.v1.json` +
`contract.test.mjs` (client) and `contract_test.go` (server) fail on drift.

## Publishing (maintainers)

Publishing is currently **deferred**. Manual steps, in order:

1. **Verify scope ownership from an authenticated machine** (`npm whoami`,
   `npm access ls-packages` / org page for `@loom`). Never publish into a
   squatted scope — fall back to `@browseroperator/loom-sdk` by renaming in
   `package.json` only.
2. `npm test` (node tests + typecheck) must be green.
3. `npm pack --dry-run` — contents must be exactly the golden file list pinned
   in `package.test.mjs` (the `files` entries + `package.json` and
   `README.md`, which npm adds itself).
4. `npm publish` from CI with provenance (`publishConfig.provenance` is set;
   publishing from a laptop without OIDC will fail by design).
