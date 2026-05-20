# Business Plan MVP Gap Analysis

This document compares the current Loom codebase against the MVP implied by
the Agentic Engineering Runtime business plan. It focuses on what can be
presented as existing MVP surface area versus what remains partial or future
roadmap work.

## MVP Comparison Table

| Category | Status | What exists in the codebase | Business-plan interpretation |
| --- | --- | --- | --- |
| Workspace, repo, role, and agent model | Exists | FleetDB-backed workspaces, repositories, roles, agent definitions, and assignments. | Core control-plane model exists for coordinating agent work. |
| CLI product surface | Exists | `loom` CLI with workspace, repo, role, agent, issue, daemon, serve, and epic commands. | Strong technical/operator MVP surface. |
| Local-first runtime | Exists | Embedded/local FleetDB mode and local-mode Podman stack. | Fits the MVP wedge: local-first agentic engineering runtime. |
| Cloud/FleetDB mode | Exists | `LOOM_FLEET_DB_URL` and FleetDB client-backed stores. | Foundation for later SaaS/control-plane deployment. |
| Web UI | Exists | React/Vite UI with kanban, table, graph, monitor, observability, terminal, settings, workspace, files, issues, and agents routes. | Demonstrable product UI exists. |
| Issue tracking | Exists | FleetDB-backed issues, ready/blocked/deferred views, claim/close/status flows, and workspace-scoped APIs. | Work queue and coordination layer exists. |
| Realtime UI plumbing | Exists | SSE event routes and frontend event handling. | Supports live coordination UX. |
| Terminal/session infrastructure | Exists | Web terminal/session APIs, terminal UI, logs, transcripts, and session views. | Evidence and operator workflow foundation exists. |
| Daemon supervision | Exists | Workspace daemon manager, command polling, leases, and supervised agent processes. | Runtime can execute local agents rather than only list tasks. |
| Lead sessions | Exists | Lead session CLI and service code. | Early lead-agent control loop exists. |
| Epic runner core | Exists | Lead-runner design, CLI, assignment flow, and deterministic worker spawning path. | Important differentiator for multi-agent delivery. |
| Codex lead runtime | Exists | Codex app-server runtime integration and control-flow docs. | Commercial demo can focus on Codex-backed delivery. |
| Logs, sessions, diffs, and artifacts | Exists | Session metadata, transcript flags, diff endpoints, and artifact contracts. | Beginning of accountability ledger. |
| Desktop shell | Exists | Desktop app scaffold and installation/runbook docs. | Supports local-first packaging story, but likely not launch-critical. |
| Auth service | Exists | Separate auth service package and Firebase tooling. | Early SaaS/auth foundation. |
| Agent run evidence ledger | Partial | Session metadata, transcripts, diffs, logs, and artifacts exist, but business-level cost/outcome ledger is not complete. | Present this as execution evidence, not yet the full cost-per-merged-PR ledger. |
| PR workflow and outcome tracking | Partial | Git/worktree/session/diff foundations exist, but PR creation, review, merge tracking, and accepted-change attribution are not a polished product loop. | Needed for the north-star metric. |
| Accountability dashboard | Partial | Monitor and observability views exist; business KPI dashboard is not yet the primary product surface. | Product can show runtime status, but not full executive metrics. |
| Hosted/team SaaS | Partial | Web server, auth service, and FleetDB remote mode exist; complete multi-tenant hosted product is not proven from this repo alone. | Treat SaaS as roadmap unless deployment is already operational elsewhere. |
| Epic-runner UX | Partial | Backend and CLI path exist; polished web UX for creating/running epic plans is not yet established. | Good demo story, but likely needs launch packaging. |
| Cost tracking | Partial | Session metadata includes token and estimated cost fields in some paths; no complete provider-normalized cost ledger is evident. | Required for the cost-per-merged-PR promise. |
| Smart model router | Not yet | No clear provider/model router that selects Codex, Claude, Gemini, or OSS workers by task type/cost/risk. | Roadmap item, not MVP claim. |
| Open-model workers | Not yet | Current inspected runtime is primarily Codex/deterministic local dogfood. | Roadmap expansion for margin and provider flexibility. |
| Hosted execution sandboxes | Not yet | Local worktrees and Podman dogfood exist; hosted isolated worker sandboxes are not evident. | Roadmap item for SaaS scale. |
| Provider edition | Not yet | No provider-facing edition/package was evident. | Future strategic product line. |
| Jira/Linear/spec integrations | Not yet | No polished business-facing integrations were evident from the inspected code. | Needed for broader adoption, not required for local MVP. |
| Enterprise self-host package | Not yet | Local and FleetDB modes exist, but a complete enterprise installation/compliance package is not evident. | Roadmap packaging and GTM work. |

## Pending MVP Work

| Priority | Area | Pending work | Why it matters for launch |
| --- | --- | --- | --- |
| P0 | Proof artifact | Produce a clean demo that shows work moving from issue to agent session to transcript/diff/outcome. | Launch buyers need to see the runtime complete real engineering work. |
| P0 | Positioning | Reframe the launch story around the working local-first runtime, with router/SaaS features positioned as roadmap. | Avoids over-claiming features that are not yet shipped. |
| P0 | Onboarding | Provide one-command setup, seeded sample workspace, and clear prerequisites. | Reduces design-partner friction. |
| P1 | Evidence ledger | Convert session/diff/transcript data into a product-facing run ledger. | Supports accountability and ROI claims. |
| P1 | PR/outcome loop | Connect agent work to PR creation, review status, merge status, and accepted-change attribution. | Enables the north-star metric: cost per merged agent-generated PR. |
| P1 | Epic UX | Make the epic-runner path easy to demo from CLI or UI with clear state transitions. | Shows the differentiator beyond a task board. |
| P2 | Integrations | Add Jira, Linear, GitHub issue/PR, and spec-import integrations. | Important for adoption after the first local MVP. |
| P2 | Cost router | Add provider-normalized cost tracking and routing. | Needed before claiming model-optimization economics. |

## Validation Notes

The table above was first produced from static repository inspection. A runtime
validation pass was then run against the local-mode Podman stack:

```sh
podman compose -f test/local-mode/docker-compose.yml up --build -d
```

The stack started Redis, FleetDB, `loom-local`, and the Caddy-served Web UI at
`http://localhost:8283/ws/LOCALMODE/kanban`. Browser validation used
`agent-browser`.

| Surface | Runtime result | Evidence |
| --- | --- | --- |
| API health/config | Passed | `GET /api/config` returned `{"mode":"open","issue_backend":"fleet"}`. |
| Seeded issue list | Passed | `GET /api/workspaces/LOCALMODE/issues` returned `LOCALMODE-1` in review and `LOCALMODE-2` closed. |
| Agent sessions | Passed | Session APIs returned completed planner and coder sessions with transcript flags. |
| Diff artifact | Passed | Coder session diff contains `local-mode-agent-output.txt`. |
| Kanban | Passed | Browser rendered backlog/open/blocked/review/done columns and the seeded issues. Screenshot: `/private/tmp/loom-local-mode-kanban.png`. |
| Table view | Passed | Browser rendered the two seeded issues in tabular form. Screenshot: `/private/tmp/loom-local-mode-table.png`. |
| Issue detail | Passed | Browser opened `/ws/LOCALMODE/issues/LOCALMODE-2` and showed status, assignee, description, design, activity, and controls. Screenshot: `/private/tmp/loom-local-mode-issue-detail.png`. |
| Agents view | Partial | Browser rendered both agents and task grouping, but exposed a local-mode task-ID mismatch described below. Screenshot: `/private/tmp/loom-local-mode-agents.png`. |
| Monitor view | Partial | Browser rendered project health and agent activity, but also reflected the local-mode task-ID mismatch. Screenshot: `/private/tmp/loom-local-mode-monitor.png`. |
| Graph view | Passed | Browser rendered both seeded issue nodes and graph controls. Screenshot: `/private/tmp/loom-local-mode-graph.png`. |
| Files view | Passed | Browser rendered repo/worktree files including `local-mode-agent-output.txt`. |
| Terminal view | Partial | Browser rendered the terminal shell surface, but the visible session was ended/disconnected and the UI reported no configured Talk-to-Lead backend. Screenshot: `/private/tmp/loom-local-mode-terminal.png`. |
| Settings view | Passed | Browser rendered AI CLI status, backend overrides, FleetDB Redis settings, terminal font settings, and observability link. Screenshot: `/private/tmp/loom-local-mode-settings.png`. |
| Observability view | Partial | Browser rendered metrics panels, but sample local-mode run did not populate completion/utilization metrics. Screenshot: `/private/tmp/loom-local-mode-observability.png`. |
| Browser page errors | Passed | `agent-browser errors` reported no page errors after the walkthrough. |

## Runtime Defect Found

| Severity | Area | Defect | Launch impact |
| --- | --- | --- | --- |
| P0 | Local-mode dogfood stack | `test/local-mode/docker-compose.yml` sets `LOOM_LOCAL_MODE_PLAN_TASK_ID=LM-PLAN-1` and `LOOM_LOCAL_MODE_CODE_TASK_ID=LM-CODE-1`, and the stack logs say those IDs were seeded. The FleetDB-backed API actually exposes generated IDs `LOCALMODE-1` and `LOCALMODE-2`. After the valid `LOCALMODE-2` run completes, the agent/monitor views show repeated coder commits against missing `LM-CODE-1` and close errors for `issue not found`. | This undermines the one-command demo because the UI can show a successful seeded run and a confusing phantom/missing task run at the same time. Fix before launch or override the compose IDs to match FleetDB-created IDs. |
