# Codex second opinion — LOOMCLI-105 exec-adapter shape (harness)

Reviewed: the harness recommendation + prototype (`sdk/prototypes/exec-adapter/`)
against `internal/workflows/builtin/daytona-task-runner.ts` and the LOOMCLI-88
helper prototype. Model: gpt-5.5 @ xhigh, explore mode, thread
`019f8205-33d3-7530-acfb-9d70144b3a33` (2026-07-20).

**Verdict: CONCUR WITH AMENDMENTS** — Harness matches the current Daytona leaf,
but the resolution must explicitly broaden "agent process invocation" and
tighten API/failure semantics.

**Findings**

1. **major** — The invariant is being redefined unless the text says so. Daytona has one in-process Flue prompt call (`session.prompt`) and Daytona is only the sandbox/tool backend, so harness fits reality: `internal/workflows/builtin/daytona-task-runner.ts:190`, `:201-204`, `:356-362`, `:421-427`. But LOOMCLI-88 states "agent process invocation" and spawn timing, while LOOMCLI-105 says prompt call equals process invocation: `sdk/prototypes/agent-exec/README.md:7-9`, `sdk/prototypes/exec-adapter/adapter-model.mjs:19-22`. Change invariant wording to "agent invocation attempt," with OS process as one implementation.

2. **blocker** — The API seam is not designed yet. `agent.exec` currently requires `argv` and always runs `runProcess`: `sdk/prototypes/agent-exec/agent-exec.mjs:55-64`, `:90-125`, `:198-207`. A harness wrapper with no argv cannot be a casual optional field without becoming two APIs. Use an explicit shape, e.g. `loom.agent.exec.process(...)` and `loom.agent.exec.invoke(...)`, or `mode: "process" | "harness"` with disjoint validation.

3. **major** — Pull is over-disqualified. The prototype's bad case is real for the modeled implementation, but not inherent: it chooses to close a clean exit as completed after pull loss: `sdk/prototypes/exec-adapter/adapter-model.mjs:232-241`, `:261-268`. A pull design could close only after successful pull or fail the session on pull failure. The better rejection is that in-sandbox upload needs a credential the current Daytona runner treats as a leak: `internal/webui/handlers/taskrunapi/module.go:12-28`, `internal/workflows/builtin/daytona-task-runner.ts:1056-1067`.

4. **major** — Harness has a durability/memory hole the README understates. The collector stores entries in a JS array: `sdk/runtime-adapters.js:1-8`; Daytona serializes only after `session.prompt` returns: `internal/workflows/builtin/daytona-task-runner.ts:245-247`; bridge upload happens from returned inline entries: `internal/driver/task_bridge_artifacts.go:151-180`, `:188-199`. Leaf crash or OOM before return loses the transcript. The model notes leaf crash, but the resolution should say harness trades sandbox-pull loss for host-memory loss.

5. **major** — Multiple prompt calls need invocation-key rules. Current Daytona has one prompt call: `internal/workflows/builtin/daytona-task-runner.ts:201-204`. But session-open is idempotent on `(taskRunID, attempt, invocationKey)`: `sdk/prototypes/agent-exec/fake-taskrunapi.mjs:35-40`; the prototype's session id is fixed to `...-agent`: `sdk/prototypes/exec-adapter/adapter-model.mjs:65-67`. Multi-prompt leaves would collapse sessions unless each prompt gets a stable distinct invocation key.

6. **major** — The real taskrunapi/server contract is not there yet. Current taskrunapi ops omit session-open/session-close: `internal/webui/handlers/taskrunapi/module.go:105-116`; `TaskRunClient` exposes logs/artifacts/runtime credentials, not `agent`: `sdk/runner.js:81-91`. Store update also has no first-terminal/CAS primitive: `internal/store/control_plane_store.go:88-99`, `internal/infra/memstore/control_plane.go:236-274`. The prototype models the desired contract, not current production.

7. **minor** — Lease/heartbeat is probably covered, but the resolution should name the dependency. The task-run executor heartbeats during execution: `internal/driver/task_request.go:651-654`, `:731-742`; default interval is 30s: `internal/driver/task_worker.go:246-249`. Session close/upload must happen before task-run completion, because taskrunapi lease verification rejects terminal/stale runs: `internal/webui/handlers/taskrunapi/module.go:24-28`, `:447-463`.

8. **minor** — Usage semantics need "unknown," not zero. Daytona uses final `response.usage`: `internal/workflows/builtin/daytona-task-runner.ts:247`; event-based partial usage exists: `sdk/runtime-adapters.js:73-101`. If abort usage is intentionally crash-lossy, don't stamp `{tokens: 0}` as the helper prototype does: `sdk/prototypes/agent-exec/agent-exec.mjs:154-157`; product contract says missing usage is unknown: `docs/product/session-artifact-contract.md:97-98`.

**What I Would Change In The Resolution Text**

- Say: "AgentSession represents one agent invocation attempt; for process leaves this is an OS process, for Daytona harness this is one Flue prompt call."
- Specify a disjoint SDK API shape for process vs harness capture.
- Reject pull as "requires extra sandbox credential/durable upload semantics or accepts evidence-loss semantics," not as logically impossible.
- Add required tests for deterministic sandbox commands creating no sessions, one prompt = one invocation key, pull/upload failure behavior, and missing usage remaining unknown.
