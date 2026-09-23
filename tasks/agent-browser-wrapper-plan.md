# Plan: Loom-Owned Browser Capability Backed by agent-browser

## Outcome

Give an interactive Loom lead a first-class browser capability in the lead view:
the lead can navigate, inspect, and interact with the same browser page the user
sees, while the user can always take over and attach an annotation. Neither the
lead nor the user sees shell commands, CDP endpoints, `agent-browser` sessions,
or implementation-specific element references.

Loom owns the public contract, authorization, lifecycle, concurrency, recovery,
and UI. `agent-browser` is a replaceable private adapter used for semantic
browser automation.

This is a follow-on architecture plan for the completed Kernel browser POC. It
does not promote the POC runtime to production and does not implement the
feature on this branch.

## Product Shape

The lead view gains a **Browser** surface beside the terminal. A lead with the
browser capability receives a small, typed set of Loom browser tools. Tool
activity is reflected in the visible browser surface. Human input remains
enabled at all times and invalidates stale agent work rather than requiring a
separate takeover mode.

```text
Lead conversation                         Lead view
       |                                      |
       | Loom browser tool call               | pointer / keyboard / annotation
       v                                      v
+------------------------------------------------------------------+
|                    Loom Browser Controller                       |
| identity | authorization | epoch fence | events | recovery       |
+----------------------------+-------------------------------------+
                             |
                             | private typed adapter calls
                             v
                  +-------------------------+
                  | AgentBrowserAdapter     |
                  | agent-browser MCP stdio |
                  +------------+------------+
                               |
                               | private CDP connection
                               v
                  Browser session / selected page
```

There is one control plane. Agent tools, human input, and annotations all pass
through `BrowserController`; no product path invokes the `agent-browser` CLI
directly.

## Architectural Decisions

### 1. Loom defines the capability; agent-browser implements it

Add a deep Loom module with a narrow public interface:

```go
type BrowserController interface {
    Snapshot(ctx context.Context, scope Scope, req SnapshotRequest) (Snapshot, error)
    Navigate(ctx context.Context, scope Scope, req NavigateRequest) (PageState, error)
    Click(ctx context.Context, scope Scope, req ClickRequest) (PageState, error)
    Fill(ctx context.Context, scope Scope, req FillRequest) (PageState, error)
    Press(ctx context.Context, scope Scope, req PressRequest) (PageState, error)
    Scroll(ctx context.Context, scope Scope, req ScrollRequest) (PageState, error)
    Capture(ctx context.Context, scope Scope, req CaptureRequest) (Capture, error)
}
```

The exact Go API can change during design review, but these constraints do not:

- no generic `run`, shell, JavaScript evaluation, or raw CDP method;
- no `agent-browser` command strings, session names, or raw refs in requests;
- all calls carry an authenticated Loom scope and expected control epoch;
- mutations return normalized Loom state and normalized failures;
- the adapter can be replaced without changing lead prompts, frontend APIs, or
  persisted event shapes.

The initial tool set is intentionally small: `snapshot`, `navigate`, `click`,
`fill`, `press`, `scroll`, and `capture`. Tabs and isolated browser identities
are app lifecycle operations first; expose them as lead tools only after their
authorization and UX are proven.

### 2. Wrap agent-browser through its typed MCP server

Run `agent-browser mcp` as an owned child process and communicate over stdio.
Do not generate shell commands around the CLI. Start with the smallest tool
profile that satisfies the Loom contract and explicitly disable capabilities
Loom does not expose.

`AgentBrowserAdapter` is responsible for:

- starting, health-checking, and terminating the exact child process it owns;
- binding the child to one Loom `BrowserSessionID` and its private CDP endpoint;
- translating Loom operations to agent-browser MCP calls;
- translating agent-browser output and errors to Loom types;
- mapping transient agent-browser element refs to opaque Loom refs;
- treating page content and adapter output as untrusted data;
- reconnecting after adapter failure without destroying the browser identity;
- invalidating every snapshot and element ref after reconnect or navigation.

Use one adapter process per active browser identity for the first implementation.
Pooling is a later optimization and must not weaken isolation.

### 3. Keep identity and lifetime concepts separate

Do not use one overloaded "browser session" value for every lifecycle.

| Concept | Meaning | Lifetime |
|---|---|---|
| `BrowserSessionID` | Isolated browser identity/profile owned by Loom | Until user closes it or policy expires it |
| `BrowserPageID` | A tab/page within that identity | Until page closes |
| `BrowserControllerRunID` | One adapter process attachment | Restartable and ephemeral |
| `BrowserControlEpoch` | Fence for human/agent ordering on a page | Increments on human input and authority changes |
| `BrowserSnapshotID` | Immutable semantic view used to resolve refs | Until invalidated by page change, input, or reconnect |
| `BrowserElementRef` | Opaque Loom reference scoped to one snapshot | Never durable across snapshots |
| conversation ID | Lead conversation allowed to use the browser | Independent of the browser identity |

A browser can outlive an adapter restart. A conversation can attach to an
existing browser only through an explicit Loom authorization. Provider-native
session IDs are never browser identity.

### 4. Make human input an automatic fence

Every human pointer, keyboard, paste, scroll, tab selection, and annotation
start goes through `BrowserController` and advances that page's control epoch.
The user never needs to click **Take control**.

Agent flow:

1. `snapshot` returns `snapshotId`, `pageId`, `pageRevision`, and
   `controlEpoch` with opaque refs.
2. A mutating operation supplies the snapshot/ref and `expectedControlEpoch`.
3. The controller validates authorization, page identity, ref freshness, and
   the epoch atomically immediately before adapter dispatch.
4. Any human input makes an older operation stale. The controller rejects it
   as `browser_control_changed`; the lead must take a fresh snapshot.
5. If human input arrives after dispatch begins, cancel when the adapter can
   prove cancellation. Otherwise mark the outcome `unknown`, reconcile page
   state, and never retry a mutation automatically.

Epoch state belongs in the Loom controller, not in the frontend and not in
agent-browser. The frontend may display it but cannot authoritatively mutate it.

### 5. Inject a Loom tool server into controlled leads

Expose one Loom-owned browser tool server to lead runtimes. The tool server
authenticates the caller and forwards only typed Loom requests to
`BrowserController`.

Integration belongs in controlled lead startup, currently reached through
`internal/cli/backends/harness_lead_runtime.go` and the Codex controlled runtime
under `internal/leadcontrol/`. Preserve the existing rule that host MCP/plugin
configuration is scrubbed. Generate task-owned provider configuration that
adds only Loom's broker endpoint and capability token.

Provider specifics stay below this boundary:

- Codex receives the Loom tool server through its controlled app-server/profile
  configuration.
- Claude and other harness-backed leads receive the equivalent generated
  provider configuration when that backend can enforce it.
- A backend that cannot securely inject the capability reports it as
  unavailable; Loom must not silently fall back to a shell command or prompt
  instruction.

The lead prompt advertises the product capability and current browser identity,
not setup instructions. Tool calls and results are normalized into the lead
conversation event stream so the UI can explain what happened.

### 6. Keep browser lifecycle in Loom

The browser runtime manager owns browser creation, page creation, selection,
shutdown, and recovery. The controller consumes that manager rather than
starting arbitrary containers itself.

For multiple browser apps:

- one `BrowserSessionID` represents one isolated identity/profile;
- multiple pages can share that identity;
- the user selects which browser/page is attached to the lead;
- lead calls must always include the attached identity resolved server-side;
- changing the attached browser advances the old and new page epochs;
- no operation may address a port, process, container, or target ID supplied by
  the lead.

The Kernel/Colima arrangement remains prototype evidence only. Production
runtime selection, packaging, resource limits, and upgrade strategy are a
separate decision behind the same controller interface.

### 7. Make annotations a Loom event, not an agent-browser feature

The browser surface owns selection UX. When the user completes an annotation,
the controller records a normalized payload:

- workspace, lead, conversation, browser, page, and snapshot identities;
- URL and page title;
- DOM-backed element descriptor and document/viewport bounds;
- user note;
- cropped image artifact reference;
- page revision and control epoch;
- capture timestamp and provenance.

The event is attached to the lead conversation as user-authored context. The
lead can inspect the same page with a fresh snapshot, but cannot forge a
user-authored annotation. Images are stored through Loom's artifact boundary,
not embedded in prompts as unbounded data.

### 8. Add the Browser surface at the lead-view seam

`internal/webui/frontend/src/components/AgentDetailMain/AgentDetailMain.tsx`
currently owns the lead's main terminal surface. Extend this area with
Terminal/Browser tabs (or the existing panel abstraction if it lands first).

The Browser surface must show:

- isolated browser identities and their pages, with **New browser** and
  **New tab** actions owned by the app;
- the selected page and connection state;
- visible agent activity such as “Lead is clicking Submit”;
- immediate human interaction with no takeover switch;
- annotation mode and captured annotation history;
- recoverable stale-action, adapter-restarting, and disconnected states.

It must not show a copyable CLI command, CDP URL, agent-browser session, raw
element ref, or provider-specific tool name.

## API and Event Contract

Define the server contract before wiring a real adapter. Suggested commands:

```text
GET  /api/workspaces/{ws}/agents/{lead}/browser-sessions
POST /api/workspaces/{ws}/agents/{lead}/browser-sessions
POST /api/workspaces/{ws}/agents/{lead}/browser-attachments
POST /api/workspaces/{ws}/browser-sessions/{session}/pages
POST /api/workspaces/{ws}/browser-sessions/{session}/pages/{page}/input
POST /api/workspaces/{ws}/browser-sessions/{session}/pages/{page}/annotations
```

Agent tools should call the controller in-process or through an authenticated
internal transport rather than these user-facing routes. Generate frontend
types from the canonical schema rather than defining parallel TypeScript
shapes.

Normalized events should include:

```text
browser.session.created
browser.page.created
browser.page.selected
browser.attachment.changed
browser.snapshot.created
browser.agent_action.started
browser.agent_action.completed
browser.agent_action.rejected
browser.human_input.observed
browser.annotation.created
browser.controller.restarting
browser.controller.recovered
browser.session.failed
```

Each event carries the Loom identities needed for projection and correlation,
not raw adapter payloads. Secrets, CDP endpoints, keystrokes, and page contents
must not enter logs by default.

## Failure Semantics

Use a small stable error vocabulary:

| Error | Meaning | Recovery |
|---|---|---|
| `browser_not_attached` | Conversation has no authorized browser | User attaches one |
| `browser_control_changed` | Human input or attachment change invalidated the action | Lead snapshots again |
| `browser_snapshot_stale` | Page/revision/ref no longer matches | Lead snapshots again |
| `browser_action_unknown` | Dispatch occurred but completion is not provable | Reconcile, do not retry automatically |
| `browser_controller_restarting` | Private adapter is recovering | Retry only read after recovery |
| `browser_unavailable` | Browser runtime or capability is unavailable | Surface actionable UI state |
| `browser_policy_denied` | Scope, URL, download, or permission policy refused the request | Do not retry unchanged |

Adapter crashes must not silently destroy the browser. Browser crashes must not
be reported as adapter crashes. Lead runtime loss, browser runtime loss, and
tool-server loss are distinct health states.

## Security Boundary

- Bind browser control endpoints to loopback or a private Unix socket.
- Issue a short-lived capability scoped to workspace, lead, conversation,
  browser attachment, and allowed operations.
- Resolve session, page, CDP endpoint, and adapter process server-side.
- Maintain origin/navigation policy centrally; do not trust the adapter alone.
- Deny downloads, file URLs, browser extensions, permission prompts, arbitrary
  local-network access, and JavaScript evaluation in the first release.
- Pin and probe the supported agent-browser version. Refuse incompatible tool
  schemas visibly rather than guessing.
- Apply output size limits, timeouts, cancellation, and redaction at the Loom
  boundary.
- Treat snapshots and page text as untrusted prompt content and retain their
  provenance.
- Preserve the existing isolation of generated lead profiles from the user's
  host MCP/plugin configuration.

## Implementation Slices

### Slice 1: Contract and fake adapter

Create the controller types, state machine, stable errors, and a fake adapter.
Do not launch a browser or agent-browser yet.

Likely areas:

- new `internal/browsercontrol/` package;
- canonical API schema and generated frontend types;
- controller unit tests for identity, authorization, epoch fencing, stale refs,
  cancellation, and unknown outcomes.

Acceptance criteria:

- every public mutation requires a scope, page, expected epoch, and idempotency
  key where appropriate;
- human input invalidates queued and undispatched agent mutations atomically;
- adapter types do not leak through the controller interface;
- contract tests cover two browser identities with independent epochs.

### Slice 2: Private agent-browser MCP adapter

Implement the owned stdio child and the minimal Loom-to-MCP translation.

Likely areas:

- `internal/browsercontrol/agentbrowser/`;
- runtime dependency/version probe;
- task-owned process supervision and structured diagnostics.

Acceptance criteria:

- no subprocess invocation uses a shell;
- one adapter is bound to one server-resolved browser identity;
- raw agent-browser refs remain inside the adapter;
- navigation, snapshot, click, fill, press, scroll, and capture pass contract
  tests against a real task-owned browser;
- adapter restart preserves the browser identity and invalidates old refs.

### Slice 3: Unified human and agent control plane

Move the proven POC input and annotation behavior behind `BrowserController`.
Remove the product path that talks directly to CDP or the CLI wrapper.

Acceptance criteria:

- sustained human input and agent interaction affect the same visible page;
- a human event arriving before dispatch rejects the stale agent action;
- an event racing an in-flight action yields a proved result or `unknown`;
- annotations carry stable Loom identities and enter the lead conversation;
- no **Take control** state is required.

### Slice 4: Controlled lead capability injection

Add the Loom tool server and generate provider-specific controlled profile
configuration, beginning with Codex while keeping the Loom contract provider
neutral. Add other supported leads only after capability enforcement is proven.

Likely areas:

- `internal/leadcontrol/` for tool-call normalization and conversation events;
- `internal/cli/backends/harness_lead_runtime.go` and backend profile builders;
- controlled profile materialization used by local/container startup;
- deployment startup code that currently strips host MCP/plugin configuration.

Acceptance criteria:

- the lead discovers Loom browser tools without receiving shell instructions;
- only the broker capability is injected; host MCP configuration remains absent;
- a lead cannot access a browser outside its scoped attachment;
- unsupported backends display “browser capability unavailable” and never
  receive a weaker fallback;
- normalized tool events render identically across supported providers.

### Slice 5: Lead Browser UI

Add the browser surface to the real lead view and connect it to generated APIs
and the event stream.

Likely areas:

- `internal/webui/frontend/src/components/AgentDetailMain/`;
- new focused browser components and state hooks;
- web UI handlers/services for lifecycle, input, and annotations.

Acceptance criteria:

- users can create and select multiple isolated browsers and tabs;
- direct pointer/keyboard interaction is always enabled;
- lead activity and stale/recovery states are visible but non-blocking;
- users can annotate the page and see the annotation delivered to the lead;
- no implementation-specific command, endpoint, or ref is exposed;
- keyboard navigation, focus, resize, clipboard policy, and accessibility are
  covered in the real Tauri shell.

### Slice 6: Recovery, security, and soak proof

Exercise the end-to-end capability under process, browser, runtime, and desktop
restarts before choosing a production browser runtime.

Acceptance criteria:

- adapter crash/restart, browser crash, and lead restart have distinct tested
  behavior;
- recovery enumerates only resources owned by the Loom runtime;
- stale tools cannot mutate a reattached or replaced browser;
- two isolated browsers remain independent under simultaneous lead/user use;
- logs and transcripts contain no CDP secrets or unbounded page data;
- resource and latency measurements are recorded for the production runtime
  decision;
- the Loom desktop E2E suite proves user-visible browser interaction rather
  than calling the controller or adapter directly.

## Test Strategy

1. **Pure state-machine tests** for epochs, page revisions, attachments,
   idempotency, cancellation, and unknown outcomes.
2. **Adapter contract tests** run against fake and real agent-browser MCP
   servers using identical cases.
3. **Runtime integration tests** prove selected-page binding, multiple browser
   isolation, reconnect, and ref invalidation.
4. **Provider tests** prove generated controlled profiles expose only Loom's
   tool server and normalize calls/results consistently.
5. **Desktop E2E tests** use the shipped UI to create a browser, let a lead act,
   interrupt with human input, create an annotation, and recover from adapter
   restart.
6. **Security tests** attempt scope swapping, stale capability reuse, raw target
   injection, oversized output, disallowed navigation, and secret leakage.

The current POC CLI wrapper tests are not sufficient evidence because direct
CLI control bypasses the authoritative epoch fence.

## Migration from the Current POC

1. Keep the existing POC as feasibility evidence while Slice 1 defines the new
   Loom contract.
2. Replace `scripts/agent-browser-control.sh` usage with adapter contract tests.
3. Remove the copyable command and direct CLI language from the prototype UI
   once the broker path can perform the same actions.
4. Route POC human input and annotations through the controller to prove the
   single control plane before touching the production lead view.
5. Rebuild the feature in production packages; do not copy the prototype's
   Colima/Kernel process model into Loom.
6. Retain a direct CLI only as an explicitly unsupported developer diagnostic,
   outside the product and outside lead prompts.

## Explicit Non-Goals

- Exposing an arbitrary command runner to a lead.
- Teaching leads how to invoke `agent-browser`.
- Passing through agent-browser's entire MCP tool catalog.
- Raw CDP access, JavaScript evaluation, or browser-extension installation.
- Reusing user-owned browser profiles or host MCP configuration.
- Declaring Kernel, Colima, Podman, Docker, or another runtime production-ready
  based on the existing POC.
- Persisting element refs as durable identifiers.
- Automatic retry of browser mutations whose outcome is unknown.

## Delivery Order and Dependencies

```text
Contract + fake adapter
        |
        v
agent-browser MCP adapter -----> runtime/version probe
        |
        v
unified human/agent control plane
        |
        +-------------> controlled lead tool injection
        |                         |
        +-------------> lead Browser UI
                                  |
                                  v
                         recovery/security/E2E proof
                                  |
                                  v
                      production runtime decision
```

Slices 4 and 5 can proceed in parallel after Slice 3 fixes the contract. The
production runtime decision is deliberately last: it should consume a stable
Loom interface and measured evidence rather than dictate the public API.

## Definition of Done

The capability is complete when a user can open or select an isolated browser
in the real lead view, ask the lead to operate it without shell instructions,
interact with the page at any time, annotate an element, and observe correctly
fenced, recoverable behavior. The same Loom contract must survive an
agent-browser adapter restart, must not expose CDP or host configuration, and
must be verified through the packaged Tauri application.
