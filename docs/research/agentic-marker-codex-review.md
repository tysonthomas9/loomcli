**Findings**

1. **blocker** — attacks: “definitionally empty” is true in this codebase.
Current code still opens an outer Flue `kind=task` session before the leaf runs: [task_bridge.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/driver/task_bridge.go:249), [task_bridge_session.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/driver/task_bridge_session.go:164), [task_bridge_session.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/driver/task_bridge_session.go:169). That includes deterministic leaves such as eval preflight, which only checks Codex availability: [session-eval-task-runner.ts](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/workflows/builtin/session-eval-task-runner.ts:37).  
Suggested amendment: say the claim is true only after the bridge deletion lands, and require a cutover test proving `session_eval_preflight` creates zero `AgentSession` rows.

2. **should-fix** — attacks: “agent.exec construction makes deterministic exec sessions impossible.”
The prototype helper is a generic process wrapper: it opens the session before spawn via `client.sessionOpen`: [agent-exec.mjs](/Users/tyson/codebase/code-agents/agent-traces/loomcli/sdk/prototypes/agent-exec/agent-exec.mjs:90), [agent-exec.mjs](/Users/tyson/codebase/code-agents/agent-traces/loomcli/sdk/prototypes/agent-exec/agent-exec.mjs:102), and validation only requires a backend string plus non-empty argv: [agent-exec.mjs](/Users/tyson/codebase/code-agents/agent-traces/loomcli/sdk/prototypes/agent-exec/agent-exec.mjs:198), [agent-exec.mjs](/Users/tyson/codebase/code-agents/agent-traces/loomcli/sdk/prototypes/agent-exec/agent-exec.mjs:206). Nothing prevents `argv: ["true"]` with `transcript: "none"`.  
Suggested amendment: keep “no marker,” but add a required enforcement point: `agent.exec`/the LOOMCLI-105 adapter must not expose arbitrary sandbox command capture as session capture. Add tests for “deterministic sandbox command => no session-open.”

3. **should-fix** — attacks: “the LOOMCLI-105 comment guard is sufficient.”
There is a machine-checkable boundary available: the real `taskrunapi` surface does not yet include `session-open`/`session-close`: [module.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/webui/handlers/taskrunapi/module.go:105), while it already has lease verification machinery: [module.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/webui/handlers/taskrunapi/module.go:248).  
Suggested amendment: require the future session-open op to enforce fenced task-run ownership and the “agent invocation only” contract; do not rely on a comment in the Daytona adapter alone.

4. **should-fix** — attacks: “sessions only come from leaves/agent.exec.”
The hidden test helper `daemon seed-transcript` writes a synthetic `AgentSession` directly: [seed_transcript_cmd.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/seed_transcript_cmd.go:26), [seed_transcript_cmd.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/seed_transcript_cmd.go:74), then stamps `transcript_ref`: [seed_transcript_cmd.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/seed_transcript_cmd.go:105). It omits `Kind`, making it synthetic/untyped session state.  
Suggested amendment: either move this behind test-only build plumbing, delete it before cutover, or explicitly carve synthetic test fixture sessions out of the glossary invariant.

5. **nit** — attacks: exact wording “agent process invocation.”
Daemon supervisor creates the session before `cmd.Start()`: [supervisor.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/supervisor/supervisor.go:453), [supervisor.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/supervisor/supervisor.go:457), [spawn.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/supervisor/spawn.go:230), [spawn.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/supervisor/spawn.go:240). Build/start failures are finalized as `spawn_failure`: [supervisor.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/cli/daemon/supervisor/supervisor.go:797).  
Suggested amendment: define session as an “agent process invocation attempt” if spawn failures intentionally remain visible.

6. **should-fix** — attacks: “Traces defaults stay exactly as decided.”
In this checkout, `judge` is not in the Go enum: [control_plane.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/domain/control_plane.go:56), nor the frontend kind union: [session.ts](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/webui/frontend/src/types/agent/session.ts:33). Traces sends `kind` only when URL-selected and defaults to “All kinds”: [TracesView.tsx](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/webui/frontend/src/components/TracesView/TracesView.tsx:528), [TracesView.tsx](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/webui/frontend/src/components/TracesView/TracesView.tsx:655).  
Suggested amendment: record LOOMCLI-106 as contingent on the prior judge-kind/default-filter implementation landing.

7. **should-fix** — attacks: ignored consumer risk.
Eval selection will judge any terminal `kind=task` session with `transcript_ref`: [evals.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/evals/evals.go:178), [evals.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/evals/evals.go:198), and manual rejudge uses the same shape: [evals.go](/Users/tyson/codebase/code-agents/agent-traces/loomcli/internal/evals/evals.go:373).  
Suggested amendment: no reserved agentic/deterministic tag is necessary, but add regression tests proving no deterministic/preflight/synthetic path can produce `kind=task + transcript_ref`.

8. **nit** — attacks: glossary sharpening.
`Agent Invocation` now says avoid “execution”: [CONTEXT.md](/Users/tyson/codebase/code-agents/agent-traces/loomcli/CONTEXT.md:73), [CONTEXT.md](/Users/tyson/codebase/code-agents/agent-traces/loomcli/CONTEXT.md:76), while `Parent Session` immediately says “from whose execution”: [CONTEXT.md](/Users/tyson/codebase/code-agents/agent-traces/loomcli/CONTEXT.md:91). Product docs also use “agent execution” as a broader product term: [agent-execution-prd.md](/Users/tyson/codebase/code-agents/agent-traces/loomcli/docs/product/agent-execution-prd.md:16).  
Suggested amendment: avoid only “exec session” / “execution session” / “deterministic exec session,” not the general product phrase “agent execution.”

Overall verdict: **RECORD WITH AMENDMENTS**. The no-marker resolution is defensible, but not as an absolute “definitionally empty” claim unless the bridge deletion, session-open enforcement, judge-kind default, and synthetic-session carveout are recorded with it. No files modified; no tests run.

