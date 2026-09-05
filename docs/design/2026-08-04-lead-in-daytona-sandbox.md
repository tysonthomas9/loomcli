# Lead in a Daytona Sandbox

**Status:** proposal, not yet approved
**Date:** 2026-08-04
**Goal:** the lead agent process runs inside a Daytona sandbox instead of on the
host. The web-UI terminal tab attaches to a PTY *in the sandbox* rather than a
local one.

Related: `docs/design/fleetdb-agent-platform-v2-execution-topology-addendum.md`
(runner placement vs sandbox placement), `docs/loom-glossary.md` (Lead, daytona).

---

## 1. What changes

Today:

```
serve (host) ──pty.StartWithSize──▶ loom lead (host) ──leadcontrol──▶ codex/claude CLI (host)
                                                                       └─ host worktree
```

Target:

```
serve (host) ──DaytonaPTYSource──▶ sandbox PTY ──▶ loom lead (sandbox) ──leadcontrol──▶ codex/claude CLI (sandbox)
                                                                                         └─ cloned repo in sandbox
```

Only the *outer* PTY is remoted. `leadcontrol` — the harness-wrapper that owns
the backend CLI's PTY and injects queued inbox messages as turns — moves into
the sandbox wholesale, in-process with `loom lead`, exactly as it is today.

---

## 2. Current state

### 2.1 The lead

`loom lead` (`internal/cli/agent/lead/lead.go:88`) runs in-process on the host:

1. Registers an `AgentSession{Kind:orchestration}` in fleet-db plus a 30s
   heartbeat (`lead.go:285`), exporting `LOOM_ORCHESTRATOR_SESSION_ID` so
   agents it spawns attribute back to it.
2. Resolves a prompt (`--prompt` file → inline role prompt → builtin) and
   appends epic-runner assignment context (`lead.go:213`).
3. Hands off to `backends.RunControlledLeadRuntime`
   (`internal/cli/backends/harness_lead_runtime.go:41`) →
   `internal/leadcontrol/`, which wraps the agent CLI in a harness-wrapper PTY
   with human TUI passthrough and drains the lead's inbox into it as turns.

The web UI launches it as a local PTY. `buildAgentLaunchSpec`
(`internal/webui/handlers/terminal/agent_session.go:319`) emits
`LaunchSpec{Argv: ["loom","--workspace",ws,"--backend",be,"lead",…], Cwd: <remembered worktree>}`,
and `PTYManager.spawnSession` runs `pty.StartWithSize(cmd, …)`
(`internal/webui/terminal/pty_manager.go:368`).

### 2.2 Daytona

`internal/workflows/builtin/daytona-task-runner.ts` (1151 lines) is a Flue
workflow runner for **one-shot, non-interactive, patch-returning** task runs:

- Reads the sealed Daytona key via the runtime-credential API
  (`readRuntimeCredential(client, "daytona")`), never from env.
- `new Daytona({apiKey}).create({labels, autoStopInterval, autoDeleteInterval})`
  (line 137) — **the default snapshot**, so no `loom`, no backend CLI, no Node
  beyond the base image.
- Clones `repoUrl`, checks out the branch, runs an env-**leak probe** that fails
  the run if host secrets reached the sandbox (`daytona_sandbox_env_leak`).
- Runs a Flue codex agent **host-side**, proxying only its fs/shell tools into
  the sandbox via `DaytonaSandboxApi` (line 373) over `sandbox.fs.*` and
  `sandbox.process.executeCommand`.
- Returns a patch; `sandbox.delete(60)` in `finally` unless
  `KEEP_DAYTONA_SANDBOX=1` (line 301).

Selection is an entrypoint switch on a shared bundle, not a separate system:
`LOOM_DAEMON_LEAF=ts` + `LOOM_DAEMON_LEAF_RUNNER=daytona-task-runner`
(`internal/cli/agent/tsruntime/tsruntime.go:52`).

### 2.3 Two facts that shape the design

**The `PTYSource` seam already anticipates this.**
`internal/webui/terminal/source.go:10` says, verbatim, that a second
implementation will be "gRPC to loom-agentd running inside a persistent-agent
Firecracker microVM", and that keeping the interface narrow means
`handlers/terminal/ws.go` never learns how the backend is realized. Daytona is
a different remote than the one that comment imagined, but the seam is the one
we want and it is already load-bearing (`*PTYManager` and `*MultiPTYManager`
both satisfy it).

**There is already a settings dial for this that nothing reads.**
`agent_runtime.default: local|daytona` exists in
`internal/localsettings/settings.go:27`, is validated
(`ValidateAgentRuntime`, line 240), sanitized for the UI, exposed over
`PATCH /localsettings`, and rendered in `SettingsView.tsx`. Grep finds **zero
consumers** of `AgentRuntime.Default`. This work is its first consumer.

---

## 3. The core decision: inverting the trust model

This is the part to settle before any code is written.

Today's Daytona posture is explicit and enforced in two places:

- `internal/cli/envfilter/envfilter.go:71` blocklists `DAYTONA_API_KEY` and
  `DAYTONA_SDK_IMPORT` from ever flowing to a subprocess as raw env.
- `daytona-task-runner.ts:1056` (`sandboxLeakProbeCommand`) runs *inside* the
  sandbox and counts how many of 13 named credentials are present. Non-zero
  fails the run. Its comment requires the list to mirror
  `env.go trustedLocalProviderCredentials` "to prevent drift".

The design intent is unambiguous: **the sandbox is untrusted and holds no
credentials**; the model-driving agent stays on the host and reaches in.

Running the lead in the VM inverts this. In-sandbox, the lead needs:

| Need | Why |
|---|---|
| Model auth (codex `auth.json` / `ANTHROPIC_API_KEY`) | the backend CLI runs there now |
| fleet-db access | orchestrator session create + 30s heartbeat + issue reads/writes (`lead.go:285`, `lead.go:407`) |
| Git credentials | if the lead pushes from the sandbox |

So the lead sandbox becomes a credential holder. Consequences:

1. **The two sandbox classes must be distinct.** The leak probe stays enforced
   for `daytona-task-runner`. It cannot be applied to a lead sandbox — it would
   fail by design. Do not relax the shared probe; give the lead sandbox its own
   trust class with its own (weaker, explicit) assertion.
2. **Mint scoped, short-lived credentials — do not ship host creds.**
   - fleet-db: a workspace-scoped expiring token. The pattern already exists —
     `LOOM_RUN_TOKEN` is a run-scoped bearer minted at claim, signed with
     `LOOM_RUN_TOKEN_SIGNING_KEY` (see AGENTS.md "Driver Runtime Auth").
   - model auth: the runner already does codex refresh-token exchange
     (`refreshCodexAccessToken`, `daytona-task-runner.ts:456`). Inject a
     short-lived **access** token; never the refresh token.
   - GitHub: fine-grained token via the runtime-credential API, or don't push
     from the sandbox at all and keep delivery host-side.
3. **Constrain egress.** The SDK's `create()` accepts `networkBlockAll` and
   `networkAllowList` (`Daytona.d.ts:128-129`). Allowlist fleet-db, the model
   API, and the git host.

For Daytona phases 1–3, scoped credentials in the sandbox are the chosen
tradeoff. This is not the only possible architecture: a provider with an
out-of-sandbox HTTPS credential broker (for example, Cloudflare Outbound
Workers) could keep credentials unreadable by the lead while injecting them
only for allowlisted destinations. Daytona does not provide that mechanism.
If scoped credential minting proves unacceptable, the choices are therefore to
change provider for credential brokering or keep the lead local and dispatch
work to VMs. Do not weaken the existing Daytona task-runner leak probe.

---

## 4. Architecture

### 4.1 Transport: Go ↔ Daytona

Use the official Daytona Go SDK
(`github.com/daytonaio/daytona/libs/sdk-go`, evaluated at v0.190.0). Its
`PtyHandle` supplies output, input, resize, wait, disconnect, and kill; its
process module supplies `CreatePty`, `ConnectPty`, `ListPtySessions`, and PTY
inspection. It also owns runner-URL resolution and preview-token auth.

This keeps the provider adapter in Go and removes the extra sidecar process and
stdio framing protocol. The evaluated release requires Go 1.25 or newer, which
matches this repo's `go 1.25.6` directive. Pin the module version. A Node adapter
remains a fallback only if the live Go PTY spike fails.

### 4.2 New Go components

```
internal/webui/terminal/
  daytona_source.go      DaytonaPTYSource — implements PTYSource
  daytona_attachment.go  daytonaAttachment — implements Attachment + realtime.Resizer
  routing_source.go      RoutingPTYSource — dispatches local vs daytona
```

`daytonaAttachment` must satisfy the same six methods as `localAttachment`
(`internal/webui/terminal/pty_session.go:40`): `ConnID`, `Output`,
`WriteInput`, `Scrollback`, `Resize`, `ExitReason`. Scrollback reuses the
existing `ringBuffer` (`internal/webui/terminal/ringbuf.go:50`, 256 KiB
default) unchanged — the multi-client fan-out, non-blocking send, and replay
snapshot semantics in `ptySession` are all transport-agnostic and should be
factored out rather than duplicated.

### 4.3 Routing

`PTYSource` is a single instance wired at construction
(`internal/webui/modbuilder/modbuilder.go:66` →
`internal/webui/handlers/terminal/module.go:44`). Add a `RoutingPTYSource` that
dispatches per session on a new field:

```go
// internal/webui/tabmeta/store.go
type LaunchSpec struct {
    Argv    []string          `json:"argv,omitempty"`
    Env     map[string]string `json:"env,omitempty"`
    Cwd     string            `json:"cwd,omitempty"`
    Runtime string            `json:"runtime,omitempty"` // "" | "local" | "daytona"
}
```

`buildAgentLaunchSpec` (`agent_session.go:319`) sets `Runtime` from
`localsettings.AgentRuntime.Default` (finally giving that setting a consumer),
overridable per role/agent. `LaunchSpec` is persisted on tab metadata, so a
tab's runtime survives restarts — and note `agentTerminalLaunchSpecStale`
(`agent_session.go:141`) already exists to detect and rebuild stale specs, so a
changed default re-provisions on next open rather than silently mismatching.

### 4.4 Sandbox image

Today's `create()` passes no `image`/`snapshot`, so the lead sandbox would have
no `loom` and no backend CLI. Needs a prebuilt snapshot — `loom-lead:<version>`
— containing Node, git, the linux/amd64 `loom` binary (goreleaser already
produces it, `.goreleaser.yml`), and the backend CLI. Prefer a published
snapshot over the SDK's declarative `Image` builds: per-create image builds
would put a multi-minute build in front of every terminal open.

### 4.5 Lifecycle

This is where the sandbox model and the terminal model disagree, and it is a
real cost risk.

- `PTYManager` kills sessions on a grace timer after the last detach
  (`pty_manager.go:389`) and on an idle timeout. A sandbox is billed and has
  its own `autoStopInterval` / `autoArchiveInterval` / `autoDeleteInterval`.
- The task runner deletes in a `finally` bracket. A long-lived lead has no such
  bracket — a serve crash orphans a paid sandbox.

Required: persist `sandbox_id` on both the tab metadata and the
`AgentSession.Metadata` (the lead already writes metadata there,
`lead.go:338`), so a restarted serve reattaches instead of orphaning. Add a
reaper that reconciles labelled sandboxes (`labels: {loom: "lead", …}`) against
live sessions. Use `autoStopInterval` as an intermediate idle control and
`autoArchiveInterval` as the billing backstop; stopped sandboxes retain billed
disk, while archived sandboxes do not.

### 4.6 leadcontrol

Should need no code change — it runs in-process with `loom lead`, so it moves
into the sandbox with it. Two things to verify rather than assume:

- **Resize now has two hops**: browser → Daytona PTY (`resizePtySession`) →
  harness-wrapper resize (`internal/leadcontrol/harness_resize.go`). Column
  mismatch between the hops will corrupt the TUI.
- **Inbox delivery** (`internal/leadcontrol/delivery.go`) needs the store, so
  it inherits the fleet-db reachability constraint below.

---

## 5. Hard constraint: fleet-db reachability

`loom lead` in the sandbox needs `LOOM_FLEET_DB_URL` reachable *from Daytona's
cloud*. `DetectMode` returns `ModeCloud` only when that var is set
(`internal/bootstrap/mode.go:51`); otherwise loom runs `ModeLocal` against an
embedded fleet-db.

**A laptop-local `loom serve` with an embedded fleet-db cannot serve a cloud
sandbox.** So lead-in-VM requires one of:

1. A deployed fleet-db the sandbox can reach (loom-dev already is), or
2. A tunnel from the sandbox to the host, or
3. Degraded mode — the lead runs without fleet-db registration, losing
   orchestrator-session linkage, heartbeats, and epic assignment. `lead.go`
   already tolerates this (registration is best-effort, `lead.go:285`), but the
   result is a lead that cannot orchestrate, which defeats the purpose.

Option 1 is the only one that delivers the feature. This should be stated as a
prerequisite, not discovered during implementation.

---

## 6. Phases

**Phase 0 — decisions (blocking).** Settle §3 (credential inversion), §5
(fleet-db mode), §4.4 (snapshot strategy). No code.

**Phase 1 — transport, provably.** Daytona Go SDK + `DaytonaPTYSource` +
`daytonaAttachment`, behind a flag, launching a **plain shell** in a default
sandbox — no loom, no credentials. Deliverable: a terminal tab in the web UI
that is a real remote shell, with working resize, scrollback replay on
reattach, and correct close-code mapping on exit. This proves the whole
transport with zero security surface.

**Phase 2 — the lead boots there.** `loom-lead` snapshot; scoped credential
minting; `loom lead` starts in-sandbox and registers its orchestrator session
against a reachable fleet-db.

**Phase 3 — routing + lifecycle.** `LaunchSpec.Runtime`, `RoutingPTYSource`,
`agent_runtime.default` wired through, sandbox-id persistence, reattach across
serve restart, reaper.

**Phase 4 — parity.** leadcontrol inbox delivery, two-hop resize, transcript
capture, epic-runner assignment delivery — each checked against the host lead.

---

## 7. Testing

Per `docs/testing-terminology.md` coordinates:

- **deterministic** — `RoutingPTYSource` dispatch, `LaunchSpec.Runtime`
  plumbing, attachment lifecycle against a fake Daytona adapter.
  No network. These are the bulk.
- **real** — Daytona Go adapter against a stub Daytona server speaking the PTY
  protocol; verifies reconnect, resize, and close semantics without spend.
- **live** — gated, costs money, mutates external state: real sandbox create →
  `loom lead` boots → registers a session → one turn → teardown verified.
  Follow the existing gating pattern (`bundled_runner_daytona_live_test.go`,
  `test/local-mode/docker-compose.daytona.yml`). Must assert the sandbox is
  actually deleted.

Add a lead-sandbox analogue of the leak probe that asserts *only* the expected
scoped credentials are present — the inverse assertion to
`sandboxLeakProbeCommand`, not a removal of it.

---

## 8. Open questions

1. **Trust class naming.** The driver plane already has
   `trust_level: trusted|untrusted` with enforced placement (AGENTS.md
   "Workflow Sandbox"). Should lead sandboxes reuse that vocabulary or get a
   separate axis? Reusing it risks conflating "bundle we authored" with
   "sandbox we handed credentials to".
2. **Worktree semantics.** The host lead runs in a remembered worktree
   (`agentLaunchCwd`, `agent_session.go:339`). The sandbox lead has a fresh
   clone. What happens to uncommitted host work, and does the lead's own
   git state need to round-trip back?
3. **One sandbox or many?** One per lead session, or one per workspace shared
   across the lead and the workers it dispatches? The latter is the third shape
   from the original fork and would change §4.5 substantially.
4. **Cost ceiling.** Who is allowed to open a Daytona-backed terminal tab, and
   is there a per-workspace cap? `PTYSource.MaxSessions()` exists
   (`source.go:65`) but means something different when each session is billed.
