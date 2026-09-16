// Scenario: register agent DEFINITIONS through the loom API, then drive the
// workspace configuration surface that surrounds them.
//
// Why this pack exists: `{name}` was the single largest binding gap in the plan.
// Six safe-read loom operations (agents/{name}/logs, /git/status, /git/diff-stat,
// /diff/commits, /diff/files, /diff/file) and every agent mutation are addressed by
// agent name, and nothing in the suite created an agent, so the read sweep skipped
// all of them as unbindable. Fabricating a name would have exercised the 404 path
// and scored it as coverage; creating a real agent is the only honest fix.
//
// The delicate part is creating an agent WITHOUT starting one. loom's create path
// (internal/webui/svcimpl/agent_service.go:351 CreateAgent) calls
// ensureLocalAgentWorktrees, which shells out to `git worktree add` for any agent
// whose role resolves to RoleKindWorker (internal/domain/role.go:254
// ResolveRoleKind). An INTERACTIVE role short-circuits that at
// agent_service.go:393 and returns before touching git. So every agent here is
// created with kind:"interactive" -- a pure fleet-db assignment record, no
// process, no worktree, no remote. start/restart/spawn/terminal/git-push stay
// untouched: those are the gated host-effecting tier.
//
// No assertions (scripts/check-no-asserts.mjs). Every check comes from the
// invariant registry, auto-dispatched at each checkpoint.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";

export const name = "agent definitions, agent lifecycle intent, and workspace config";

/**
 * A second workspace exists only so the rename can be driven against something
 * disposable. Renaming the shared APIAFT workspace would move the identity every
 * other pack in the run addresses. The key fleet-db derives is the uppercased,
 * punctuation-folded name -- service.WorkspaceKeyFromName
 * (internal/webui/service/workspace_types.go:49) -- so "apiaft-cfg" becomes
 * "APIAFT-CFG", and that key is what the {ws} path parameter must carry.
 */
const CFG_WS_NAME = "apiaft-cfg";
const CFG_WS_KEY = "APIAFT-CFG";

export async function run(w: World): Promise<void> {
  // Idempotent: the workspace may already exist from an earlier pack in the run.
  await w.loom("POST", "/api/workspaces", { name: w.workspace, type: "empty" });
  await w.loom("GET", "/api/backends");
  await w.checkpoint("agents-workspace-ready");

  // --- agent definitions ----------------------------------------------------
  //
  // Required fields come from validateAgentCreateInput
  // (internal/webui/svcimpl/agent_service.go:616): workspace_key, a name matching
  // service.ValidStoredAgentName (`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`,
  // internal/webui/service/agent.go:170), and a non-empty role_name. `kind` is
  // constrained to ""|"interactive"|"worker" by the same function -- and the
  // handler (handlers/agents/handlers.go:65) rejects a workspace_key that
  // disagrees with the {ws} path segment, so it must be sent, not omitted.
  const lead = "aft-lead";
  await w.loom("POST", `/api/workspaces/${w.workspace}/agents`, {
    workspace_key: w.workspace,
    name: lead,
    // "lead" is already seeded as an interactive role when the workspace is
    // created, so this create takes the reconcile branch
    // (agent_service.go:496 reconcileExistingAgentRole) rather than the
    // create-the-role branch. Deliberately no `prompt`: reconcile rejects a
    // create whose prompt differs from the stored role, which would turn a
    // re-run into a 400 that says nothing about agents.
    role_name: "lead",
    kind: "interactive",
    auto: false,
    backend: "claude",
    desired_state: "stopped",
  });
  // Bind only after the create; family() in src/plan.ts maps
  // /api/workspaces/{ws}/agents/... to the "agents" family, so the sweep looks up
  // "agents.name" and never substitutes an agent id into some other resource.
  w.bind("agents.name", lead);

  // A second agent under a role that does NOT exist yet, carrying a prompt. This
  // is the other branch: loom creates the role for you (agent_service.go:449
  // ensureAgentRole -> Roles().Create with Kind=interactive) -- one POST that
  // writes two fleet-db entities, which is exactly the kind of multi-write the
  // L1 replay oracle is there to check.
  const scout = "aft-scout";
  await w.loom("POST", `/api/workspaces/${w.workspace}/agents`, {
    workspace_key: w.workspace,
    name: scout,
    role_name: "aft-scout",
    kind: "interactive",
    prompt: "api-aft: interactive scout, definition only, never started.",
    auto: false,
    backend: "claude",
    desired_state: "stopped",
  });
  w.bind("agents.name", scout);

  // Creating an agent also creates its role, so the roles are real state too and
  // "roles.name" is honestly bindable. Deliberately NOT binding "roles.role":
  // /api/v1/{ws}/roles/{role}/skills/{name} would then compose a role that exists
  // with a skill that does not, and score a 404 as coverage.
  w.bind("roles.name", "lead");
  w.bind("roles.name", "aft-scout");

  await w.checkpoint("agents-created");

  // Read the collection back through the surface a client uses. HandleList wraps
  // in dto.NewListResponse, while HandleCreate answers with a bare
  // WorkspaceAgentInfo -- two different envelopes for the same entity, which the
  // conformance lane gets to judge from the real exchange.
  await w.loom("GET", `/api/workspaces/${w.workspace}/agents`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/interactive-prompts`);

  // The same two facts through the other observer. loom never persists agents
  // itself -- Agents().Create writes to fleet-db -- so this is the seam L2 exists
  // to diff, and it is the only way to see the role loom created on our behalf.
  await w.fleet("GET", `/api/v1/${w.workspace}/agents`);
  await w.fleet("GET", `/api/v1/${w.workspace}/roles`);

  // --- partial update -------------------------------------------------------
  //
  // AgentUpdateInput (internal/webui/service/agent.go:95) is all pointers: an
  // omitted field must stay untouched. Sending a strict subset is the only way to
  // find a handler that overwrites unsent fields with zero values -- the failure
  // mode already recorded for issues in tests/aft/FINDINGS.md 1.13.
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/agents/${scout}`, {
    auto: true,
    backend: "codex",
  });
  await w.checkpoint("agent-patched");

  // A second PATCH that only moves desired_state. handlers/agents/handlers.go:98
  // validates it against domain.AgentDesiredState; "idle" is in the accepted set
  // and, unlike "running", asks no daemon to spawn anything.
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/agents/${scout}`, {
    desired_state: "idle",
  });
  await w.checkpoint("agent-desired-state-set");

  // --- lifecycle INTENT, not lifecycle ---------------------------------------
  //
  // stop and yield go through handleLifecycle (handlers/agents/handlers.go:181):
  // they write agent state and enqueue an AgentCommand row for whichever daemon
  // owns the workspace. loom itself spawns nothing here. start and restart are
  // excluded on purpose -- restart is classified host-effecting by src/plan.ts,
  // and start asks for a real process.
  await w.loom("POST", `/api/workspaces/${w.workspace}/agents/${scout}/stop`, {});
  await w.checkpoint("agent-stop-requested");

  // yield carries an optional payload map; lifecycleRequest
  // (handlers/agents/handlers.go:175) merges `payload` over the command's
  // defaults, so this is the one lifecycle shape with a body worth sending.
  await w.loom("POST", `/api/workspaces/${w.workspace}/agents/${lead}/yield`, {
    payload: { reason: "api-aft-drain" },
  });

  // Read the agents back after the lifecycle writes. The lifecycle handlers reply
  // with dto.NewMessageResponse -- a string, not the record -- so the only way to
  // see whether state/desired_state actually moved is to re-read, on both sides.
  await w.loom("GET", `/api/workspaces/${w.workspace}/agents`);
  await w.fleet("GET", `/api/v1/${w.workspace}/agents`);
  await w.checkpoint("agent-yield-requested");

  // The per-agent queue is a registered, read-only route that agents/handlers.go:163
  // answers with ErrNotImplemented in fleet-db store mode -- a 501 by design, and
  // absent from api/openapi.yaml, which is exactly why it is worth driving: the
  // spec cannot tell a client this endpoint is permanently unavailable here.
  await w.loom("GET", `/api/workspaces/${w.workspace}/agents/${scout}/queue`);
  await w.checkpoint("agent-read-surfaces");

  // --- workspace configuration ----------------------------------------------
  //
  // backend is validated against webuiterminal.ValidBackends
  // (internal/webui/terminal/session_command.go:13 -- claude, codex, opencode,
  // gemini, cursor). Moving it and moving it back exercises the write path
  // twice while leaving the shared workspace on the default the rest of the
  // suite expects.
  await w.loom("GET", `/api/workspaces/${w.workspace}/config/backend`);
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/config/backend`, { backend: "codex" });
  await w.checkpoint("workspace-backend-codex");

  await w.loom("GET", `/api/workspaces/${w.workspace}/config/backend`);
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/config/backend`, { backend: "claude" });
  await w.checkpoint("workspace-backend-restored");

  // design_format is a closed two-value set (service.ValidWorkspaceDesignFormat,
  // internal/webui/service/workspace.go:24). Same there-and-back shape.
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/config/design-format`, { design_format: "html" });
  await w.checkpoint("workspace-design-format-html");

  await w.loom("PATCH", `/api/workspaces/${w.workspace}/config/design-format`, { design_format: "markdown" });
  await w.checkpoint("workspace-design-format-restored");

  // --- rename and order, on a workspace nothing else in the run addresses ----
  await w.loom("POST", "/api/workspaces", { name: CFG_WS_NAME, type: "empty" });
  await w.checkpoint("cfg-workspace-created");

  // RenameWorkspace (internal/webui/service/workspace_impl.go:462) changes the
  // display Name and leaves the fleet-db Key alone, so the {ws} path segment
  // keeps resolving through the old key afterwards. That asymmetry is precisely
  // why this is driven on a disposable workspace.
  await w.loom("PATCH", `/api/workspaces/${CFG_WS_KEY}/name`, { new_name: "apiaft-cfg-renamed" });
  // Probe the asymmetry directly: does the OLD key still address the workspace
  // after its name changed? Whichever way it answers is a fact worth recording.
  await w.loom("GET", `/api/workspaces/${CFG_WS_KEY}`);
  await w.checkpoint("cfg-workspace-renamed");

  // Reorder persists a UI preference in local settings
  // (workspace_impl.go:499 ReorderWorkspaces) -- it is a display concern only and
  // touches no workspace content.
  await w.loom("PUT", "/api/workspaces/order", { order: [w.workspace, CFG_WS_KEY] });
  await w.checkpoint("workspace-order-set");

  // --- the observability reads a settings screen makes --------------------
  //
  // Driven explicitly rather than left to the sweep, because they are the reads
  // that should reflect everything above: an agent count, a per-workspace
  // readiness gate, a monitor view scoped to this workspace.
  await w.loom("GET", `/api/workspaces/${w.workspace}/stats`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/readyz`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/monitor/status`);
  await w.loom("GET", "/api/monitor/agents");
  await w.loom("GET", "/api/monitor/stats");
  await w.loom("GET", "/api/config");
  await w.checkpoint("agents-observed");
}
