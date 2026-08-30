# `loom preflight` command implementation plan

**Target:** LoomCLI `origin/v5` at `90a3ae716`

**Input:** PR #182's `loom preflight` implementation and the current
`internal/runtimepreflight` automatic gate.

## Decision

Add `loom preflight` as the diagnostic surface of one canonical local-backend
readiness module. The same structured verdict must drive:

- `loom preflight` human output;
- `loom preflight --json` machine output;
- the CLI epic-run gate;
- the CLI workflow-run gate; and
- the Web UI workflow-run gate.

The command is read-only. It does not install a backend, start an auth flow,
change the selected backend, update a workspace, or queue a run.

## Current v5 state

- [`internal/runtimepreflight/preflight.go`](../../internal/runtimepreflight/preflight.go)
  returns only formatted errors and silently defaults to Codex after daemon
  profile read failures.
- The early gate is called from the epic CLI, workflow CLI, and Web UI workflow
  handler, but each caller receives only an error string.
- [`internal/driver/task_bridge.go`](../../internal/driver/task_bridge.go)
  separately resolves the actual execution backend from the agent row named
  by `req.WorkerProfileID` — in the epic-runner flow that ID doubles as the
  spawned worker's agent name, so one ID is deliberately consulted against
  two stores for two different concerns: scheduling reads the
  `WorkerProfile` (whose `Backend` field is a *runtime-provider constraint*,
  e.g. `remote-sandbox`, matched against node capabilities — never an AI
  CLI name), while launch reads the agent row's `Backend` for the AI CLI.
  Preflight must never interpret `WorkerProfile.Backend` as an AI backend.
- v5 auto-discovers arbitrary `loom-backend-*` executables as registered,
  health-checkable backends, while the TS local runner supports only a fixed
  set and rejects everything else at run time with
  `local_backend_unsupported`.
- The local task runner is spawned from two seams, not one:
  `HostBridgeTaskExecutor.runBuiltInFlueWorkflow` in
  [`internal/driver/task_bridge.go`](../../internal/driver/task_bridge.go)
  and `RunBundledTaskRunner` in
  [`internal/driver/bundled_runner.go`](../../internal/driver/bundled_runner.go)
  (sole caller: the tsruntime CLI command). Within the host-bridge executor,
  a configured task-runner command (the `Command` field or
  `LOOM_DRIVER_TASK_RUNNER_CMD`) takes precedence over the built-in Flue
  path and can carry local-task-runner requests.
- [`internal/webui/handlers/prreview/reviewer.go`](../../internal/webui/handlers/prreview/reviewer.go)
  is a fourth consumer: `reviewerBackend` calls `ResolveLocalBackend` and
  inherits its silent Codex fallback, then applies
  `leadcontrol.IsControlledLeadBackend` — a hand-maintained allowlist that
  must stay in sync with `backends.RunControlledLeadRuntime` and is also
  consumed by the driver's outbox dispatcher.
- [`internal/cli/backends/backend_cmd.go`](../../internal/cli/backends/backend_cmd.go)
  already owns all-backend health inventory, but it duplicates the health
  projection and exits from inside the handler.
- [`internal/cli/root.go`](../../internal/cli/root.go) resolves the global
  backend before normal subcommands run, which can prevent a diagnostic
  command from reporting an unknown override in its own stable format.

The historical comparison with PR #182 is recorded in
[PR #178 and #182 compared with v5](2026-08-29-pr-178-182-v5-comparison.md).

## Why the PR is not sufficient as-is

PR #182 established the right basic shape, but the v5 implementation needs
four deeper corrections:

1. An installed and authenticated but unhealthy backend needs its own stable
   `local_backend_unhealthy` class. It must not collapse into
   `local_backend_unavailable`.
2. Workspace backend lookup errors currently fall through to Codex. A
   readiness check must not claim Codex is ready when authoritative workspace
   configuration could not be read.
3. The eventual local runner can select a per-agent backend override (the
   agent row named by the worker-profile ID), while the current queue-time
   preflight only checks the workspace default. The command must identify
   its scope, and a targeted check must use the same resolution rules as
   execution.
4. `loom backend health` already reports raw backend health. `loom preflight`
   must reuse the health probe and add runner-specific resolution,
   classification, and remediation rather than create a third health model.

## Domain language

- **Preflight:** a read-only evaluation performed before a local task runner is
  launched.
- **Target:** the workspace, optional agent, and optional explicit backend
  the caller wants evaluated. `--agent` names an agent row — the identity
  the launch resolver reads (in the epic-runner flow, worker-profile IDs
  double as spawned workers' agent names).
- **Resolved backend:** the backend name plus the source that selected it.
- **Health:** raw facts reported by the backend adapter.
- **Verdict:** Loom's stable interpretation of resolution and health.
- **Ready:** the resolved backend is registered, supports health checks, is
  installed, has usable authentication, and reports healthy.

Precedence for resolving a backend is:

```text
explicit --ai-backend
  > selected agent's Backend
  > workspace DaemonProfile.AgentBackend
  > codex default
```

The explicit override is diagnostic only. It does not mutate workspace or
agent configuration. The override is a command-local `--ai-backend` flag;
neither the global `--backend` flag nor `LOOM_BACKEND` participates in
target resolution. Daemon-scheduled execution never reads that variable; the
tsruntime leaf path does launch the CLI's active backend (which honors it),
and its launch gate therefore checks the exact value being launched rather
than re-resolving from the store. An `--ai-backend` value that trims to
empty is treated as absent.

## Owning module and seam

Deepen `internal/runtimepreflight` rather than putting readiness logic in the
Cobra command. Its external interface should remain small:

```go
type Request struct {
    WorkspaceKey    string
    AgentName       string
    AgentRequired   bool
    BackendOverride string
}

func CheckLocalTaskRunner(
    ctx context.Context,
    st targetStore,
    req Request,
) (Result, error)

func RequireLocalTaskRunner(
    ctx context.Context,
    st targetStore,
    req Request,
) error
```

`AgentRequired` distinguishes the two consumers of `AgentName`. The
command's explicit `--agent` target must exist (`AgentRequired: true`; a
missing agent is `local_backend_resolution_failed`, exit 2). The launch
checker passes the worker-profile-derived name with `AgentRequired: false`,
where absence of the agent row simply selects the next precedence level.

`CheckLocalTaskRunner` owns backend precedence, the health probe, verdict
classification, safe messages, and remediation. `CheckLocalTaskRunner` always
returns the most complete safe result it can. Its `error` is non-nil only when
the evaluation itself was interrupted or failed, such as an authoritative
store read failure; in that case the result still carries
`local_backend_resolution_failed` for machine output. An ordinary completed
not-ready evaluation returns a result and nil error.

`RequireLocalTaskRunner` is the automatic-gate projection: it calls `Check`,
returns operational errors unchanged, and otherwise converts a not-ready
verdict into one typed error that wraps the full `Result` (class and message
are accessors on it). Consumers recover it with `errors.As`: the Web UI
extracts the embedded `Result` for its 400 body, and the driver extracts
only the class through a small locally-declared interface
(`interface{ PreflightClass() string }`) so `internal/driver` never imports
`runtimepreflight`. An operational error carries no class and falls back to
the driver's generic `task_executor_error` — that is intended: it is a
failure to evaluate, not a classified verdict.

The health-probe adapter is an internal seam. Production uses
`backends.CheckBackendHealth`; the package's own table-driven tests inject
an in-memory adapter directly. The exported stub hook
(`SetHealthCheckerForTest`) is retained: the epic CLI, workflow CLI, and Web
UI workflow suites use it to queue runs on hosts without real backend CLIs,
and the launch-time gate honors the same hook so those suites and e2e keep
working. Do not expose any further public surface merely for tests.

Health probes take no context: `ctx` is checked between steps, and hangs are
bounded by the probes' own limits (`VersionProbeTimeout`). Threading context
through `HealthCheckableBackend` is out of scope.

The Cobra package owns only flag parsing, active-workspace acquisition,
rendering, and exit-code selection.

## Canonical result

```go
type BackendSource string

const (
    BackendSourceOverride  BackendSource = "override"
    BackendSourceAgent     BackendSource = "agent"
    BackendSourceWorkspace BackendSource = "workspace"
    BackendSourceDefault   BackendSource = "default"
)

type ErrorClass string

const (
    ErrorClassUnavailable      ErrorClass = "local_backend_unavailable"
    ErrorClassUnsupported      ErrorClass = "local_backend_unsupported"
    ErrorClassAuthMissing      ErrorClass = "local_backend_auth_missing"
    ErrorClassUnhealthy        ErrorClass = "local_backend_unhealthy"
    ErrorClassResolutionFailed ErrorClass = "local_backend_resolution_failed"
)

type Result struct {
    Backend      string
    BackendSource BackendSource
    Ready        bool
    Health       *HealthStatus
    ErrorClass   ErrorClass
    Message      string
    Remediation  []string
}
```

`Health` is nil when no health probe was available. This prevents zero-value
booleans from pretending that a probe actually ran. `ErrorClass` is empty only
when `Ready` is true.

`Result` carries JSON tags in `runtimepreflight` and is the canonical
serialization. The CLI adds a thin envelope struct that embeds `Result` and
contributes only the command-surface fields (`schema_version`, `kind`,
`workspace`, `agent`); embedding is not field-copying, and no other output
type may restate `Result`'s fields.

The existing `HealthStatus.APIKeySet` field is consulted only to explain an
*unhealthy* backend — it never blocks a healthy one. OpenCode deliberately
reports `Healthy=true, APIKeySet=false` (multi-provider, no single key to
check) and must classify as ready. For an unhealthy external backend that
reports no auth signal, `local_backend_auth_missing` may actually be an
operational probe failure; the message carries the distinction, and refining
external `HealthStatus` reporting is a named follow-up. For OAuth-backed
CLIs such as Cursor, `APIKeySet` is still read as authentication readiness.
The new human output says
“Authenticated,” but the first implementation does not rename the existing
shared JSON field; changing `loom backend health --json` is a separate
contract migration and must not be smuggled into this command.

Replace `PreflightLocalTaskRunner` with the two interfaces above in the same
change. Diagnostic callers use `Check`; automatic gates use `Require`. Do not
leave a compatibility wrapper after all call sites have migrated.

## Classification table

| Condition | Ready | Error class | Required remediation |
| --- | --- | --- | --- |
| Registered, runner-supported, installed, healthy | true | empty | none |
| Unknown backend or no health capability | false | `local_backend_unavailable` | choose a supported backend |
| Registered but outside the runner-supported set (healthy or not) | false | `local_backend_unsupported` | choose a runner-supported backend |
| CLI not installed | false | `local_backend_unavailable` | install it or choose another backend |
| Installed, unhealthy, auth reported missing | false | `local_backend_auth_missing` | authenticate or configure credentials |
| Installed, authenticated, unhealthy | false | `local_backend_unhealthy` | show the backend's safe health detail |
| Agent not found, or agent/daemon-profile store lookup failed | false | `local_backend_resolution_failed` | fix the target, or repair connectivity/configuration and retry |

Classification order is load-bearing: registration and probe availability,
runner support, then health — **a healthy, runner-supported backend is ready
regardless of `APIKeySet`**. Only an unhealthy backend is then split:
not installed → `unavailable`; installed with auth reported missing →
`auth_missing`; otherwise `unhealthy`. This preserves the current v5
healthy-first semantics.

The runner-supported set (the TS runner's `SUPPORTED` map: claude, codex,
opencode, gemini, cursor) lives in `internal/backendnames`, with a Go test
that parses `internal/workflows/builtin/local-task-runner.ts` to prevent
drift — v5 auto-registers arbitrary `loom-backend-*` externals whose health
checks can pass, and preflight must not call them ready when the runner will
reject them.

Loom-authored fields — `Result.Message`, `Remediation`, and all human output
— are guaranteed safe: never tokens, authorization headers, or environment
dumps. `health.*` is probe-reported passthrough (external backends author
their own health JSON), length-capped and documented as such;
`loom backend health --json` already exposes the same probe text today, so
preflight adds no new exposure.

## Command contract

```text
loom preflight [--agent <name>] [--ai-backend <name>] [--json]
```

- With no backend override, resolve and verify the active workspace.
- With `--agent <name>`, resolve that agent row in the active workspace and
  honor its `Backend`. When combined with `--ai-backend`, the agent is
  still resolved and must exist — the uniform exit-2 rule applies.
- With an explicit `--ai-backend`, check that backend without changing
  stored configuration. The active workspace is included when available but
  is not required merely to diagnose a local CLI on a new installation.
- `--agent` requires a workspace and cannot be combined with a missing
  workspace.
- Do not add `--all`; `loom backend health` already owns the all-backends view.

The command must bypass the root command's backend selection step. Otherwise
an unknown value in the global `--backend` flag fails in root pre-run before
preflight can emit any structured verdict for its own target. Extract the shared root pre-run work
into a helper that initializes logging, mirrors `--server`/`--workspace`
into the environment, and rebuilds the deps container — everything root's
pre-run does except `ResolveAndSetBackend`. Use the same helper from
`loom doctor` instead of adding another bespoke pre-run path. Doctor thereby
gains the flag mirroring and deps refresh it lacks today; that behavior
change is intended.

### JSON output

After successful flag parsing, `--json` emits exactly one JSON document to
stdout:

```json
{
  "schema_version": 1,
  "kind": "local_task_runner",
  "workspace": "ACME",
  "agent": "worker-a",
  "backend": "gemini",
  "backend_source": "agent",
  "ready": false,
  "health": {
    "healthy": false,
    "installed": true,
    "version": "1.2.3",
    "api_key_set": true,
    "message": "backend health check failed"
  },
  "error_class": "local_backend_unhealthy",
  "message": "backend gemini is installed and authenticated but not healthy",
  "remediation": ["run loom backend info gemini", "repair the backend and retry"]
}
```

Keep `schema_version`, field names, enum values, nullability, and omission rules
stable. The JSON shape is the canonical contract; human output is a projection
of it. Do not maintain a second CLI-only output type with copied fields.

Omission rules, pinned: `workspace` and `agent` are omitted when absent
(never empty strings, never null); `health` is omitted when nil (never
null); `error_class` is omitted when ready; `remediation` is omitted when
empty; `backend` and `backend_source` are omitted when resolution failed
before a backend was selected. The `agent` field names the agent row
(worker-profile IDs double as agent names in the epic-runner flow).

### Human output

Human output should lead with the verdict and the selected source:

```text
Local task runner: NOT READY
Workspace: ACME
Agent: worker-a
Backend: gemini (agent override)
Installed: yes
Authenticated: yes
Healthy: no
Class: local_backend_unhealthy
Reason: backend health check failed
Next: loom backend info gemini
```

### Exit behavior

- `0`: the target is ready;
- `1`: the check completed and the target is not ready;
- `2`: the check could not be completed because target resolution or the
  health evaluation failed operationally.

A nonexistent `--agent` exits `2` with `local_backend_resolution_failed` and
a message naming the agent and workspace: exit `1` is reserved for a real
target that would fail to run. Broken-pipe and encoding failures exit `2`;
"exactly one JSON document" is a promise about what the command writes, not
about delivery on a broken pipe. The 0/1/2 contract applies after successful
flag parsing: a flag or usage error follows standard CLI behavior (exit 1,
usage on stderr, no report and no JSON document) — machine callers detect it
by the absence of a JSON document on stdout.

Use `cli.NewCommandExitError`; do not call `os.Exit` inside the command. On an
expected not-ready result, print the report first and set `SilenceErrors` and
`SilenceUsage` so Cobra does not duplicate the message on stderr. The process
main still prints every returned error to stderr before exiting, so the
command returns a single short line (`preflight: <class>`) as the error text
— the full report goes to stdout only, and stderr carries exactly that one
line.

## Aligning automatic gates

Update the epic CLI, workflow CLI, and Web UI to consume the same `Result` and
typed failure. Preserve the current early gate for fast feedback.

The early gate is not proof that every eventual child can run: an epic may
select an agent with a different backend after queueing. Add a final preflight
at the local-runner launch seam after the concrete agent and backend are known.
That final check is authoritative and closes both the per-agent gap and the
configuration-change race between queue and launch.

The launch-time gate covers both spawn seams: the host-bridge executor and
`RunBundledTaskRunner` (whose tsruntime caller runs the check before
invoking it, since that path is already CLI-land, against the active backend
it actually launches). Within the host-bridge executor the gate keys on the
request's local entrypoint, not the spawn branch: the configured-command
branch, which takes precedence over the built-in Flue path, is gated
identically. The gate resolves the backend once and passes the checked value
into the spawn environment — the env builder must not re-resolve, or a
configuration change between check and spawn reopens the race.

Launch-time resolution keeps v5's semantics, now documented: the agent row
named by `req.WorkerProfileID` (worker-profile IDs double as spawned
workers' agent names in the epic-runner flow) supplies the AI backend, else
`DaemonProfile.AgentBackend`, else codex. `WorkerProfile.Backend` is a
scheduling provider constraint and must never be read as an AI CLI name.
Absence of the agent row selects the next precedence level; only an actual
read failure is `local_backend_resolution_failed`.

`internal/driver` imports no CLI packages and stays that way:
`HostBridgeTaskExecutor` gains an injected preflight-checker field with the
driver-declared signature
`func(ctx context.Context, workspaceKey, workerProfileID string) (backend string, err error)`.
The checker performs resolution plus the readiness check and returns the
checked backend, which the executor binds into the spawn environment; the
CLI-side implementation wraps the canonical module, and its not-ready error
exposes `PreflightClass()`. The field is wired at all four production
construction literals (two in the CLI driver exec command, the task-worker
default, and the driver HTTP API module). The task-worker literal is built
inside `internal/driver`, which cannot construct the CLI-side checker, so
`TaskWorker` itself carries a checker field that its production constructor
in `internal/cli/serve` supplies and passes through to the executor — that
pass-through is part of the wiring inventory. A nil
checker on a local-entrypoint request fails closed with
`local_backend_resolution_failed` — a forgotten wiring site must fail
loudly, not silently recreate the unchecked-backend hole this gate closes.
Existing driver-package tests that execute local entrypoints construct
executor literals directly and are updated to inject a permissive stub
checker; the exported `runtimepreflight` hook lives in other test binaries
and does not cover them.

On a not-ready launch verdict, the driver's executor-error normalization
(`applyExecutorError`) unwraps the typed error with `errors.As` and writes
the canonical class into `TaskRun.ErrorClass`; the generic
`task_executor_error` remains the fallback for other executor errors.

The Web UI workflow-run gate keeps HTTP 400 but stops flattening the verdict
into a bare string: the body becomes
`{"error": "<message>", "preflight": {<canonical Result JSON>}}`. The
`error` key survives for existing clients; `Result` already carries
`error_class`, `message`, and `remediation`, so nothing is duplicated at the
top level. That body applies to the typed not-ready error (recovered via
`errors.As`); a non-typed operational failure follows the handler's existing
domain-error mapping (500 by default) with the plain error body — an
evaluation failure is not a verdict.

Backend resolution used by `HostBridgeTaskExecutor` and preflight must move to
one lower-level module that does not import CLI or Web UI packages. That module
also owns the single `LocalTaskRunnerEntrypoint` constant. Remove the current
duplicated constant and precedence logic from `internal/driver` and
`internal/runtimepreflight`.

Do not silently fall back after an authoritative agent or daemon-profile read
error. Absence or a blank configured value selects the next precedence level;
an actual read failure yields `local_backend_resolution_failed`.

The same strict resolver serves the fourth consumer: prreview's
`reviewerBackend` migrates in the same change and propagates resolution
errors instead of silently defaulting. Its controlled-lead capability filter
is a separate rule about which runtimes can host an interactive agent, and
is reworked in step 5.

## Implementation order

### 1. Canonical resolution and verdict

- Introduce the lower-level local-backend target resolver.
- Model source, result, classes, and safe remediation in
  `internal/runtimepreflight`.
- Add `local_backend_unhealthy`, `local_backend_resolution_failed`, and
  `local_backend_unsupported`; put the runner-supported set in
  `internal/backendnames` with the TS-source parity test.
- Move the package's own table-driven tests to an internal injected probe;
  the exported stub hook remains for the cross-package suites.
- Update all four existing consumers atomically: the epic CLI, workflow CLI,
  and Web UI early gates, plus prreview's `reviewerBackend` (which becomes
  fail-closed on resolution errors).

Gate: every caller produces the same class and message for the same target and
health facts.

### 2. Command and rendering

- Add `internal/cli/preflight` and register it from `cmd/loom/main.go`.
- Add `--json`, `--agent`, and a command-local `--ai-backend` override; the
  global `--backend` flag and `LOOM_BACKEND` are not consulted.
- Extract the root pre-run helper (logging, `--server`/`--workspace`
  mirroring, deps rebuild) and adopt it from doctor and preflight.
- Render both human and JSON output from the canonical result.
- Use coded exits and eliminate direct `os.Exit` from the new package.

Gate: healthy, not-ready, and operational-failure paths each emit exactly one
document/report and the promised exit code.

### 3. Authoritative launch-time gate

- Move launch resolution into the checker: agent row named by the
  worker-profile ID, else daemon profile, else codex — v5 semantics
  unchanged, now flowing through one seam that returns the checked backend.
- Run the same readiness check immediately before spawning at both launch
  seams (host-bridge executor and `RunBundledTaskRunner`'s tsruntime caller,
  which checks the active backend it launches), via the injected checker on
  `HostBridgeTaskExecutor`; a nil checker on a local entrypoint fails
  closed. Gate by entrypoint across all spawn branches, including the
  configured-command branch, and bind the checked backend into the spawn env
  (no second resolution).
- Update existing driver-package tests that execute local entrypoints or
  exercise backend env resolution (e.g.
  `TestLocalTaskRunnerBackendEnvResolution`) to inject permissive stub
  checkers.
- Propagate the canonical class into `TaskRun.ErrorClass` via `errors.As` in
  the executor-error normalization.
- Emit the embedded canonical result in the Web UI gate's 400 body.
- Ensure non-local runners bypass local-backend preflight.

Gate: changing an agent override after queueing cannot launch an unchecked
backend, and a remote runner is unaffected.

### 4. Remove duplicated health projections

- Keep `loom backend health` as the all-backends inventory command.
- Make its per-backend row reuse the canonical health projection where doing
  so does not require workspace resolution.
- Replace its direct `os.Exit(1)` with `cli.NewCommandExitError`.
- Keep `loom backend info` as the detailed capability/configuration view.
- Leave the other `CheckBackendHealth` consumers' semantics unchanged: the
  interactive lead's missing-binary check and `backendcheck`'s discovery
  fallback. `internal/cli/backends` must not import `runtimepreflight`
  (import cycle) — reuse flows through the shared `HealthStatus` and
  `CheckBackendHealth` only; classification stays in `runtimepreflight`.

Gate: health booleans and auth labels do not drift between backend health,
backend info, and preflight.

### 5. One controlled-lead capability

- Move the controlled-lead backend set to `internal/backendnames`;
  `leadcontrol.IsControlledLeadBackend`, the driver's outbox dispatcher, and
  prreview all read it (driver and leadcontrol cannot import CLI packages).
- Add a parity test in `internal/cli/backends` asserting
  `RunControlledLeadRuntime` handles exactly that set, so drift is a test
  failure instead of a comment.
- Make reviewer provisioning fail closed when the configured backend fails
  the filter: a clear error naming the backend and the supported set,
  instead of silently substituting Codex.

Gate: no hand-maintained allowlist remains, and a workspace backend that
cannot host a reviewer fails loudly in provisioning just as it does in run
preflight.

## Required verification

### Module tests

Cover:

- every backend-source precedence branch;
- unknown backend and missing health capability;
- missing binary;
- missing authentication;
- installed/authenticated/unhealthy;
- healthy;
- a healthy backend with `APIKeySet=false` (OpenCode) classifying as ready;
- a registered backend outside the runner-supported set — healthy or not —
  classifying as `local_backend_unsupported`;
- nil health when no probe ran;
- agent not found;
- agent-store and daemon-profile read failures;
- safe message and remediation generation;
- `LOOM_BACKEND` set in the environment not affecting resolution; and
- context cancellation before probing.

Tests assert the structured result through the module interface, not helper
implementation details.

### CLI tests

Cover:

- active-workspace default;
- agent override;
- explicit backend without a workspace;
- `--ai-backend` not mutating stored configuration;
- human output for every class;
- JSON schema and omission rules;
- stdout/stderr separation;
- exit codes 0, 1, and 2 (agent not found exits 2 with
  `local_backend_resolution_failed`);
- unknown backend reaching preflight instead of failing in root pre-run; and
- broken-pipe/encoding errors.

### Integration tests

Use a temporary workspace store plus controlled backend adapters. Prove the
same fixture produces the same verdict through:

- `loom preflight --json`;
- epic-run early gating;
- workflow-run early gating;
- Web UI early gating; and
- final local-runner launch gating at both spawn seams.

Also prove: the Web UI 400 body carries the embedded canonical result; a nil
launch checker fails closed; the configured-command spawn branch is gated;
the spawn environment carries the exact backend value that was checked; the
canonical class lands in `TaskRun.ErrorClass`; the runner-supported parity
test pins the TS `SUPPORTED` map; the controlled-lead parity test pins the
launch dispatch; and reviewer provisioning fails closed on a filtered
backend.

### Real local proof

This is a real local backend check, not a live paid-model invocation. It must
not send a prompt or consume model tokens.

Run the built CLI against an isolated temporary Loom config and record:

- exact Loom commit and binary version;
- selected workspace, agent, backend, and backend source;
- human and JSON output for one ready backend;
- a controlled missing-auth or missing-binary outcome;
- exit codes and separate stdout/stderr; and
- confirmation that no TaskRun, session, transcript, or external request was
  created.

## Non-goals

- Installing backend CLIs or repairing authentication.
- Starting browser/device login.
- Checking remote runners such as Daytona or OpenShell.
- Proving that a paid model request will succeed.
- Checking every registered backend; use `loom backend health` for inventory.
- Persisting or caching readiness. Health is point-in-time and must be checked
  again at launch.
- Evaluating scheduling constraints (worker-profile provider requirements,
  `Enabled`); the queue rejects unschedulable profiles separately, and
  `WorkerProfile.Backend` is a provider constraint outside preflight's
  vocabulary.

## Landing criteria

The work is complete when `loom preflight` is a truthful projection of the
same verdict that guards the final local-runner launch, all failure modes have
stable classes, JSON is deterministic, preflight never reports ready for a
backend the runner will reject, the launch gate binds the exact checked
backend into the spawn, and no caller can
silently substitute Codex after a configuration read failure — nor at all:
reviewer provisioning fails closed on a backend that cannot host it instead
of quietly switching models.
