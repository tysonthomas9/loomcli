# Web Onboarding Spec

**Status:** Draft
**Date:** 2026-05-07
**Related:** `docs/product/local-mode-product-spec.md`,
`docs/product/agent-run-ux-spec.md`,
`docs/product/desktop-app-runtime-spec.md`

## Purpose

Define how a brand-new local-dev user goes from `loom serve` to running their
first agent without leaving the browser. Today the new-user path is a chain
of unguided empty states, dead-end CTAs, and CLI-only configuration. This
spec covers a Web UI redesign that:

- replaces bare empty states with one fixed, resumable first-run flow,
- creates a workspace and attaches the first repo in a single guided step,
- turns backend setup into an install/login/configure workbench with an
  embedded terminal,
- derives onboarding progress from server-visible state wherever possible,
- exposes AI backend status and setup actions without storing credentials,
- preserves the existing terminal `WelcomeBanner` and per-tab UX.

This document is scoped to the **local-dev** audience: open auth, single
user, embedded FleetDB. Cloud/multi-tenant onboarding is out of scope for
this revision.

The product outcome is not "the checklist is complete." The outcome is:

> The user has one workspace, one repo, one ready backend, one configured
> agent, one issue, and one visible agent run.

## Current Gaps

The following are concrete frictions a new user hits today. File references
are anchors for implementation.

| Gap | Where |
|---|---|
| Bare "No workspaces" inline `<p>` with no context | `internal/webui/frontend/src/components/RedirectToWorkspace/RedirectToWorkspace.tsx` (lines 82-130) |
| Two parallel zero-workspace surfaces with different copy and different CTAs | `RedirectToWorkspace` (inline `<p>`) vs. `EmptyState` "no-workspaces" variant (references CLI `loom init`); the latter is currently dead code |
| Empty Kanban board names "New issue" but does not show the button | `internal/webui/frontend/src/components/EmptyWorkspaceBoard/EmptyWorkspaceBoard.tsx` |
| `NoBackendsEmptyState` says "Configure a backend" but Settings has no add flow | `internal/webui/frontend/src/components/TerminalView/layout/NoBackendsEmptyState.tsx` |
| Backend setup only reports status; it does not help install CLIs or run login flows | `/api/backends`, `useBackends()` |
| `EmptyState` "no-workspaces" variant references CLI `loom init` but is unused in the zero-workspace flow | `internal/webui/frontend/src/components/EmptyState/EmptyState.tsx` (lines 22-110) |
| `Template` workspace type is a permanent dead-end ("Coming soon" + disabled) | `internal/webui/frontend/src/components/CreateWorkspaceModal/CreateWorkspaceModal.tsx` (lines 636-643) |
| No coordination of first-run state across the app — only two ad-hoc localStorage keys | `loom:last-workspace-id`, `terminal-onboarding-dismissed` |
| Create issue / workspace / agent modal state is owned by `App`, but empty states live lower in the tree | `internal/webui/frontend/src/App.tsx`, `EmptyWorkspaceBoard.tsx` |

## Goals

1. A first-run user follows one fixed path: create workspace with repo,
   verify repo, set up backend, create agent, create issue, run agent.
2. The first workspace step never creates a dead-end empty workspace by
   default; it requires either a local repo path or a Git clone URL.
3. Backend setup can install a missing CLI, run an interactive login, or
   guide environment-variable setup from the browser using an embedded
   terminal and explicit command preview.
4. Onboarding progress is computed server-side with actionable error states,
   so it is coherent across browser tabs without relying on client guesses.
5. The Settings view shows backend readiness and setup actions without
   expanding the trust surface to store API keys at rest.
6. The flow is dismissable per workspace after a workspace exists and never
   blocks direct use of the product.

## Non-Goals

- Storing AI backend API keys inside Loom. The backend health auth-readiness
  boolean remains a read-only signal; keys live in the operator's shell
  environment or the backend CLI's own auth store. Storing keys at rest in
  Loom is an explicit non-goal of this revision and would require a separate
  security review.
- Running package-manager or auth commands silently. Install and login
  actions must show the exact command and require an explicit user click
  before a terminal starts.
- Supporting every package manager or operating system in the first pass.
  Backend setup metadata is curated per known backend and may start with the
  most common macOS/local-dev install path plus manual fallback instructions.
- Cloud / shared-deployment onboarding (OIDC, invited-user flows).
- A CLI `loom doctor` command. The server-side computation is factored to
  enable this later, but it is out of scope here.
- Replacing or subsuming the per-tab `WelcomeBanner` in `TerminalView`.
  That banner is scoped to the terminal layer and is complementary.

## Onboarding Flow

The onboarding flow defines six steps in fixed order. A step is blocked when
any lower-order prerequisite is incomplete. The UI may render the steps as a
compact progress rail, but the primary surface is a guided flow, not a passive
checklist.

| # | Step | Complete when | CTA |
|---|---|---|---|
| 1 | Create workspace with repo | The active workspace exists and has at least one repo | Opens the fixed `WorkspaceRepoWizard` |
| 2 | Verify repo | The selected repo passes readiness checks (`complete`) or has only non-blocking issues (`warning`) | Shows repo checks and repair actions |
| 3 | Set up AI backend | At least one backend is installed and authenticated | Opens `BackendSetupPanel` |
| 4 | Create agent | The active workspace has at least one agentdef | Opens prefilled `CreateAgentModal` |
| 5 | Create first issue | `IssueService.ListIssues(limit=1)` returns at least one issue for the workspace | Opens `CreateIssueModal` |
| 6 | Run first agent | A run/session exists for the first issue or an agent is visibly running/has completed | Starts the selected agent |

### Step 1: WorkspaceRepoWizard

Onboarding does not expose the current three workspace-type choices. It shows
one fixed path:

1. Workspace name.
2. Repo source:
   - local path, with desktop folder picker when available,
   - Git clone URL.
3. Optional branch override.
4. Create workspace.

An "empty workspace" path can remain available elsewhere in the product, but
first-run onboarding should not steer new users into an empty shell that cannot
run an agent.

If a user already has a workspace with no repos, the same step resumes as
"Add a repo to this workspace" and uses the same repo-source UI. The
completion signal remains "active workspace has at least one repo."

### Step 2: Repo Verification

Repo verification is a preflight preview before backend/agent setup. It should
check:

- repo path exists on the server host,
- path is a git repo,
- default branch is known,
- Loom can read the repo,
- Loom can create or use an agent worktree,
- remote exists when push-oriented flows require it.

Each failed check must name the exact failing repo and the repair action. This
step is allowed to return `warning` when the repo is usable for a first local
run but has limited later behavior, such as no remote.

### Step 3: Backend Setup Workbench

Backend setup is a real workbench, not just status badges. For each backend,
the UI shows:

- backend display name and short description,
- `installed`, `authenticated`, and `ready` status,
- status message from the server,
- setup actions: install, login, configure environment, refresh.

Install and login actions open an inline terminal panel with command preview:

```text
Install Codex CLI

Command:
<curated install command for this backend>

[Run in terminal]
```

The terminal is useful because install and auth flows are often interactive:
package managers may prompt, and CLIs such as Codex, Claude, Gemini, or
OpenCode may launch browser/device login flows. The browser does not store or
proxy credentials; it only starts a local terminal session with an explicit
operator-approved command.

Environment-variable setup is handled differently. The UI may show the env var
name, copy buttons, and shell-rc snippets, but setting `OPENAI_API_KEY` or
similar inside the terminal does not update the already-running Loom server
process. The UI must say "restart Loom after changing shell env." See
the **Refresh Semantics** section below for the canonical rule.

### Steps 4-5: Reuse existing flows

Steps 4 (Create agent) and 5 (Create first issue) reuse the existing
`CreateAgentModal` and `CreateIssueModal` with workspace, repo, and
backend pre-selected from server state. They get no dedicated subsection
because their UI is unchanged; the only new behavior is the prefill and
the post-success transition back to the onboarding flow. Prefill rules:
the first agent uses the workspace's default backend (selected in step
3); the first issue is associated with `active_repo`.

### Step 6: Run First Agent

The final onboarding action starts the selected agent against the first issue.
Success transitions into the real product surface:

- the agent appears in the sidebar,
- the issue card shows claim/session state,
- the session/log/transcript surface becomes visible when available.

If the run cannot start, the failure should reuse the local-mode preflight
language from `docs/product/local-mode-product-spec.md`: missing backend auth,
missing tool, repo/worktree failure, gate failure, or daemon/runtime issue.

## Server Contract

Two endpoints derive step state from existing services. There is no new
FleetDB schema change for step progress.

```
GET /api/onboarding/status
GET /api/workspaces/{ws}/onboarding/status
```

The top-level endpoint is only for the no-workspace route. The workspace
endpoint is preferred once a workspace exists so workspace scoping flows
through normal route validation and context injection.

**Workspace-scoped response:**

```jsonc
{
  "success": true,
  "data": {
    "workspace_id": "abcd-1234",
    "active_repo": "my-app",       // repo evaluated by step 2; first repo on the workspace, or null
    "steps": [
      {
        "id": "workspace-repo",
        "status": "complete",
        "action": "open_workspace_repo_wizard"
      },
      {
        "id": "verify-repo",
        "status": "warning",
        "action": "open_repo_checks",
        "message": "Repo has no git remote; first local run is still available."
      },
      {
        "id": "setup-backend",
        "status": "actionable",
        "action": "open_backend_setup",
        "message": "Codex is installed but authentication is missing."
      },
      {
        "id": "create-agent",
        "status": "blocked",
        "action": "open_create_agent"
      },
      {
        "id": "create-issue",
        "status": "blocked",
        "action": "open_create_issue"
      },
      {
        "id": "run-agent",
        "status": "blocked",
        "action": "start_first_agent"
      }
    ],
    "all_complete": false
  }
}
```

**Top-level (no-workspace) response.** Returned by `GET /api/onboarding/status`
when the caller has no workspace context yet. Only step 1 is evaluated;
later steps are returned as `blocked` so the frontend can render them as
muted, future-state rows without a second request:

```jsonc
{
  "success": true,
  "data": {
    "workspace_id": null,
    "active_repo": null,
    "steps": [
      { "id": "workspace-repo", "status": "actionable", "action": "open_workspace_repo_wizard" },
      { "id": "verify-repo",    "status": "blocked",    "action": "open_repo_checks" },
      { "id": "setup-backend",  "status": "blocked",    "action": "open_backend_setup" },
      { "id": "create-agent",   "status": "blocked",    "action": "open_create_agent" },
      { "id": "create-issue",   "status": "blocked",    "action": "open_create_issue" },
      { "id": "run-agent",      "status": "blocked",    "action": "start_first_agent" }
    ],
    "all_complete": false
  }
}
```

Step statuses:

| Status | Meaning | Unblocks next step? |
|---|---|---|
| `complete` | The step's durable/server-visible condition is satisfied. | Yes |
| `actionable` | Prerequisites are met and the user can take this step now. | No |
| `blocked` | An earlier required step is incomplete. | No |
| `warning` | The step is usable enough to continue, but has limitations. | Yes |
| `error` | The server tried to evaluate the step and hit a concrete failure. | No |
| `unknown` | The server cannot evaluate the step because a dependency is unavailable. | No |

`actionable` replaces what an earlier draft called `pending`; the term is
chosen to make it clear the user can act now. `warning` is the only non-
`complete` status that unblocks downstream steps — it represents a
deliberate "good enough to continue" decision (e.g. local repo with no
remote). `error` and `unknown` always block downstream steps and surface
their `message` to the user verbatim.

The handler is implemented as a thin shim over a computation function:

```go
func ComputeOnboardingStatus(ctx context.Context, deps OnboardingDeps) (OnboardingStatusData, error)
```

`OnboardingDeps` holds the existing service interfaces (`WorkspaceService`,
`IssueService`, `BackendOps`, repo preflight helpers, and agent/session run
inspection). The function carries no HTTP concerns and is the seam a future
`loom doctor` CLI subcommand can call directly.

Server code, not the frontend, owns the status lifecycle. This is intentional:
the server has the workspace context, repo access, backend health, and run
state needed to distinguish `blocked`, `warning`, `error`, and `unknown`.

### Backend Setup Metadata

Backend setup needs metadata beyond the `/api/backends` health booleans
(`installed`, `api_key_set`, `available`). The existing endpoint is
extended in place so the frontend issues a single request and the existing
`useBackends()` hook + `backendsStore` plumbing keeps working. The added
fields (`authenticated`, `ready`, `install_actions`, `login_actions`,
`env_vars`) are all read-only and curated server-side:

```jsonc
{
  "success": true,
  "data": [
    {
      "name": "codex",
      "display_name": "Codex",
      "description": "OpenAI Codex CLI",
      "installed": false,
      "authenticated": false,
      "ready": false,
      "message": "codex binary not found on PATH",
      "install_actions": [
        {
          "id": "npm-global",
          "label": "Install with npm",
          "command": "<curated codex install command>",
          "interactive": true
        }
      ],
      "login_actions": [
        {
          "id": "codex-login",
          "label": "Run codex login",
          "command": "codex login",
          "interactive": true
        }
      ],
      "env_vars": [
        {
          "name": "OPENAI_API_KEY",
          "restart_required": true
        }
      ]
    }
  ]
}
```

Commands must come from curated backend metadata, not from user-controlled
strings. The frontend always shows the command before execution.

## Frontend Architecture

### Domain types

```
src/types/onboarding.ts
  OnboardingStepId     = "workspace-repo" | "verify-repo" | …
  OnboardingStepStatus = "complete" | "actionable" | "blocked" |
                         "warning" | "error" | "unknown"
  OnboardingStep       = { id, order, status, action, message? }
  OnboardingAction     = "open_workspace_repo_wizard" | …
```

### Layer map

```
src/api/onboarding/onboarding.ts          stateless fetcher
src/api/backends/setup.ts                 backend setup metadata fetcher
src/hooks/onboarding/stepRegistry.ts      labels, descriptions, action copy
src/hooks/onboarding/useOnboardingStatus  fetches server status,
                                          manages dismiss flag
src/contexts/OnboardingActionsContext.tsx bridges low-level CTAs to App-owned
                                          modals/navigation/terminal actions
src/components/OnboardingFlow/            fixed first-run flow shell
src/components/WorkspaceRepoWizard/       step 1 guided workspace+repo flow
src/components/RepoChecksPanel/           step 2 repo verification
src/components/BackendSetupPanel/         step 3 status + install/login/env
src/components/BackendSetupTerminal/      command preview + embedded terminal
```

### Surface reuse

`OnboardingFlow` is one component used in two surfaces, controlled by a
`context` prop:

```
context="no-workspace"     rendered by RedirectToWorkspace
context="empty-kanban"     rendered by EmptyWorkspaceBoard
```

In `no-workspace`, only step 1 is interactive because there is no workspace
route yet. After the workspace is created, the user lands in
`/ws/{id}/kanban` and the flow resumes at step 2.

In `empty-kanban`, the flow renders only when the workspace has zero total
issues. It must not render for filtered-empty states; filtered-empty states
should keep the current "clear filters" guidance.

### Action ownership

`OnboardingActionsContext` is the single dispatch point for all onboarding
CTAs, so low-level empty-state components do not need to know how a given
action is realized (modal open, route navigation, terminal launch).

**Provider placement.** `RedirectToWorkspace` renders above `App.tsx` in
the router tree (it is the `/` route handler before any workspace exists).
The provider therefore lives in `router.tsx`, wrapping the entire route
tree, not inside `App.tsx`. Inside the provider, action handlers that
require a workspace (open create-agent, open create-issue, start agent)
are wired by `App.tsx` re-binding them via the provider's setter API once
a workspace context is available; before that, those actions are no-ops
that surface a clear "create a workspace first" message.

The action provider owns:

- opening the workspace+repo wizard,
- opening repo checks,
- navigating to `/ws/{id}/settings#backends`,
- opening backend setup,
- opening create agent,
- opening create issue,
- starting the first agent,
- opening/focusing the setup terminal.

### Dismissal

Dismiss state uses the existing scoped-storage helper:

```
wsSet(workspaceId, "onboarding-dismissed", "1")
```

The key is per-workspace, so dismissing on one workspace does not silence
the flow in another. Dismissal is client-side only by design — a user
clearing browser storage will see the flow again, which is acceptable for the
local-dev audience.

In the no-workspace context (`workspaceId === undefined`), the dismiss
button is hidden: there is no workspace to scope the flag to.

## Settings: BackendSetupPanel

A new panel rendered in `SettingsView`, above the existing "Project
Default Backend" panel. The same panel is also used by onboarding step 3.
For each backend setup entry:

- backend name and short description,
- three status badges: `installed`, `authenticated`, `ready`,
- current health message,
- install/login/configure actions.

Actions:

- **Install** shows the curated install command and starts it in the embedded
  terminal only after the user clicks "Run in terminal."
- **Login** shows the curated auth command and starts it in the embedded
  terminal only after explicit confirmation.
- **Configure env** shows the env var the user may set,
- a copy-to-clipboard button for the variable name,
- a shell-rc snippet,
- a clear restart requirement for env-var changes,
- a "Refresh status" button calling `backendsStore.refreshBackends()` and
  the setup metadata refresher.

Loom does not store or transmit the key. The panel reads the
already-computed auth boolean returned by backend health checks; for
env-var based auth that signal comes from `os.Getenv()` in the server
process, and for file/session based auth (e.g. a CLI auth file) it comes
from disk. The lifecycle rule is unified in **Refresh Semantics**.

## Refresh Semantics

"Refresh" appears in three places in this spec (backend status, repo
verification, onboarding status). It is one operation with one rule: the
server re-reads everything it can re-read at runtime — backend binaries on
PATH, file-based auth (e.g. `~/.config/<backend>/auth.json`), repo state on
disk — and re-reports. It does **not** re-import the server process
environment. Env-var changes therefore require a `loom serve` restart, and
the UI must say so wherever an env-var-based readiness signal is shown.

## Accessibility

The onboarding flow is the first surface a new user touches; it must be
keyboard-navigable end to end. Specifically:

- the active step receives focus on render and is `aria-current="step"`,
- step status is announced via a visually-hidden status region rather
  than relying on color alone,
- the embedded setup terminal's "Run in terminal" button is reachable
  with Tab from the command preview and labels the command as a `<pre>`
  the screen reader can read,
- dismiss is reachable via keyboard and announces the new state.

## Terminal Command Safety

Install and login commands are powerful local actions. The first version uses
these guardrails:

- Commands come from backend setup metadata controlled by Loom, not from API
  query strings, issue text, repo names, or other user-controlled content.
- The exact command is shown before execution.
- The user must click "Run in terminal."
- The command runs in an embedded terminal session, so the user can stop,
  answer prompts, and inspect output.
- Loom does not parse terminal output for API keys.
- Command history is not used as a credential store.
- A failed install/login leaves the step actionable with the terminal exit
  status and backend health message.

## File Plan

### Created

```
internal/webui/handlers/onboarding/onboarding.go
internal/webui/handlers/onboarding/onboarding_test.go
internal/webui/handlers/backends/setup.go
internal/webui/handlers/backends/setup_test.go
internal/webui/frontend/src/types/onboarding.ts
internal/webui/frontend/src/api/onboarding/onboarding.ts
internal/webui/frontend/src/api/onboarding/index.ts
internal/webui/frontend/src/api/backends/setup.ts
internal/webui/frontend/src/hooks/onboarding/stepRegistry.ts
internal/webui/frontend/src/hooks/onboarding/useOnboardingStatus.ts
internal/webui/frontend/src/hooks/onboarding/index.ts
internal/webui/frontend/src/hooks/onboarding/__tests__/useOnboardingStatus.test.ts
internal/webui/frontend/src/contexts/OnboardingActionsContext.tsx
internal/webui/frontend/src/components/OnboardingFlow/OnboardingFlow.tsx
internal/webui/frontend/src/components/OnboardingFlow/OnboardingFlow.module.css
internal/webui/frontend/src/components/OnboardingFlow/index.ts
internal/webui/frontend/src/components/OnboardingFlow/__tests__/OnboardingFlow.test.tsx
internal/webui/frontend/src/components/WorkspaceRepoWizard/WorkspaceRepoWizard.tsx
internal/webui/frontend/src/components/WorkspaceRepoWizard/WorkspaceRepoWizard.module.css
internal/webui/frontend/src/components/WorkspaceRepoWizard/index.ts
internal/webui/frontend/src/components/RepoChecksPanel/RepoChecksPanel.tsx
internal/webui/frontend/src/components/RepoChecksPanel/RepoChecksPanel.module.css
internal/webui/frontend/src/components/BackendSetupPanel/BackendSetupPanel.tsx
internal/webui/frontend/src/components/BackendSetupPanel/BackendSetupPanel.module.css
internal/webui/frontend/src/components/BackendSetupPanel/index.ts
internal/webui/frontend/src/components/BackendSetupPanel/__tests__/BackendSetupPanel.test.tsx
internal/webui/frontend/src/components/BackendSetupTerminal/BackendSetupTerminal.tsx
internal/webui/frontend/src/components/BackendSetupTerminal/BackendSetupTerminal.module.css
```

### Modified

```
internal/webui/app/routes.go
internal/webui/app/server_app.go        (handler wiring)
internal/webui/handlermux/handlers.go   (struct field)
internal/webui/frontend/src/App.tsx     (onboarding action provider)
internal/webui/frontend/src/components/RedirectToWorkspace/RedirectToWorkspace.tsx
internal/webui/frontend/src/components/EmptyWorkspaceBoard/EmptyWorkspaceBoard.tsx
internal/webui/frontend/src/components/SettingsView/SettingsView.tsx
internal/webui/frontend/src/components/CreateWorkspaceModal/CreateWorkspaceModal.tsx
internal/webui/frontend/src/components/EmptyState/EmptyState.tsx        (remove dead "no-workspaces" variant)
internal/webui/frontend/src/router.tsx                                  (mount OnboardingActionsContext)
internal/webui/frontend/src/types/index.ts
internal/webui/frontend/src/hooks/workspace/useBackends.ts
```

Net new code is approximately 1800-2400 LOC including tests, depending on how
much of the terminal setup path can reuse `TerminalView` internals.

## Build Sequence

1. **Server status endpoint and tests.** Implement
   `ComputeOnboardingStatus` with explicit statuses and error handling.
   Register both `/api/onboarding/status` and
   `/api/workspaces/{ws}/onboarding/status`.
2. **Backend setup metadata.** Add curated install/login/env metadata for
   known backends. Extend or pair with `/api/backends` so the frontend can
   display `installed`, `authenticated`, `ready`, setup commands, and env
   vars.
3. **Repo verification.** Add repo readiness checks used by onboarding
   step 2. Keep checks read-only except for explicit user-triggered repair
   actions.
4. **Frontend types and fetchers.** Land onboarding and backend setup
   domain types, stateless API clients, and exports.
5. **Onboarding actions context.** Wire `App.tsx` as the owner of modal,
   navigation, terminal, and start-agent actions.
6. **WorkspaceRepoWizard.** Build the fixed workspace+repo first step and
   use it from `RedirectToWorkspace`.
7. **OnboardingFlow.** Build the resumable flow shell with step status,
   current action, warnings/errors, dismissal, and action dispatch.
8. **BackendSetupPanel and terminal.** Implement command preview,
   terminal launch, refresh, env-var guidance, and Settings integration.
9. **Create agent / issue integration.** Prefill first-agent and first-issue
   flows with workspace/repo/backend context.
10. **Run first agent.** Wire the final action to the local agent-run path
    and transition to the real UI when run/session state appears.
11. **Sanity sweep.** `go test ./internal/webui/...`, relevant vitest
    filters, and Playwright e2e covering no-workspace → first visible run.

## Test Plan

| Layer | Coverage |
|---|---|
| Go unit | `ComputeOnboardingStatus` for: no workspace, workspace without repo, repo warning, backend missing CLI, backend missing auth, no agent, no issues, no run, complete run |
| Go unit | Backend setup metadata returns only curated commands and no secret values |
| TS unit | `useOnboardingStatus` status mapping, dismiss read/write, error state handling |
| TS unit | `OnboardingFlow` renders current step, blocked/warning/error states, and dispatches actions |
| TS unit | `BackendSetupPanel` status display, command preview, terminal launch callback, copy/refresh handlers |
| Integration | Hit workspace-scoped onboarding endpoint with seeded workspaces; assert response shape and field semantics |
| E2E (Playwright) | Fresh `loom serve`, no workspaces → create workspace with repo → verify repo → set up/detect backend → create agent → create issue → start agent → visible run/session |
| E2E (Playwright) | Filtered-empty Kanban does not render onboarding; true empty workspace does |

## Risks

**Server lifecycle vs. env vars.** Env-var based auth readiness reflects the
server process environment at startup. A user who exports a key in a different
shell after `loom serve` is running will still see that backend as
unauthenticated until restart. The panel must not imply refresh can import a
changed environment. Refresh only re-checks the current process environment,
binaries, and file-based auth.

**Install command trust.** One-click install is convenient but sensitive.
Commands must be curated in code, previewed, and run only after explicit user
confirmation in a terminal. No command may be assembled from repo paths,
issue text, backend messages, or query params.

**Interactive auth UX.** Login commands may open browser windows, device-code
prompts, or long-running terminal sessions. The terminal panel needs clear
running, failed, canceled, and refresh states.

**First-run depends on agent execution plumbing.** The final step requires a
working UI-started local agent path. If that path is not ready, the onboarding
MVP should either ship behind the same feature gate or stop before claiming
"running first agent."

**Test coverage on `RedirectToWorkspace`.** The current test asserts a
literal "Create Workspace" button label. The new component renders the
same label, so the assertion can be migrated to
`getByRole('button', { name: /create workspace/i })` without semantic
change.

**`EmptyWorkspaceBoard` text contract.** Existing tests assert specific
heading and subtitle text via `getByText`. The redesign keeps these
elements and renders the flow as a sibling only for true empty-workspace
states, so filtered-empty tests should keep their current assertions.

**Step 4 ("Create an agent") proxy metric.** Completion is checked via
`workspace.agents.length > 0`, not a live session. A workspace that has
an agentdef but has never run it shows step 4 complete. The trade-off
is acceptable because step 6 separately verifies a run/session signal.

**`workspace.repo_count` shape.** Step 1 needs repo presence. Prefer
workspace-scoped `GetWorkspace` data because it includes repos; avoid relying
only on the list-summary shape.

## Out-of-Scope, Acknowledged

- **i18n.** The codebase does not currently use a translation framework;
  onboarding strings live as literals in the new components. If/when i18n
  lands, the step registry (`stepRegistry.ts`) is the single point that
  needs to switch to a translation function.
- **Telemetry.** Loom does not emit user-event telemetry today. The
  `useOnboardingStatus` hook is the natural seam to add it later without
  threading instrumentation through every component.
- **Windows.** Backend install commands are curated for macOS/Linux in
  the first pass. A Windows path requires per-platform metadata and is
  deferred to a follow-up.

## Open Questions

1. Which install command should be the default per backend and platform
   (`npm`, `brew`, direct binary, or manual docs link)?
2. Should the flow allow an advanced empty-workspace escape hatch, or keep
   that available only outside first-run onboarding?
3. Should the flow auto-dismiss when `run-agent` completes, or stay visible
   until the user dismisses it? Default in this spec: auto-collapse after the
   first visible run and keep a "Show setup" affordance in Settings.
4. Does the redesign want any analytics? Loom does not currently emit
   user-event telemetry; the hook is a clean place to add it later
   without spreading instrumentation across components.
5. Should `loom init` print the URL of the new onboarding screen on
   completion, to bridge CLI-first users into the web flow? Decision
   deferred — handled by a future CLI-onboarding spec.
