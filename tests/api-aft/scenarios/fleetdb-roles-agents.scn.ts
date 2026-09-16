// Scenario: fleet-db's agent control plane, driven end to end.
//
// WHY these operations. Everything loom does at runtime bottoms out here: a role is
// the behaviour contract, an agent is a named instance of it, a session is one run of
// that agent, a lease is the mutual-exclusion token that stops two runners claiming
// the same agent, and commands/inbox-messages are the two directions of the control
// channel. None of it is reachable from loom's own API, so without a pack that speaks
// fleet-db directly this whole surface is dark.
//
// It is also the densest source of event ACTIONS in the system. `role.create`,
// `agent.create` and `agent.update` are written to the same immutable stream that the
// L1 verify oracle replays (internal/service/role_service.go:121,
// internal/service/agent_service.go:181), while fleet-db's spec `Action` enum lists
// only a subset of the actions internal/models/event.go defines -- so simply moving
// this state legitimately is expected to make the advisory conformance lane speak up.
//
// There are no checks in this file, by design. State is moved and checkpoint() is
// called; every oracle that has what it needs runs itself.

import { call } from "../src/wire.ts";
import type { World } from "../src/world.ts";

export const name = "fleet-db roles, agents, sessions, leases and the agent control channel";

const ROLE = "aft-reviewer";
const AGENT = "aft-worker";
const SESSION = "aft-session-1";
const LEASE = "aft-lease-1";
const COMMAND = "aft-command-1";
const INBOX = "aft-inbox-1";
const PROFILE = "aft-profile-1";
const SERVICE = "aft-service-1";

/** Pull a server-minted id out of a bare fleet-db entity body; "" when absent. */
function str(v: unknown, key: string): string {
  const o = v !== null && typeof v === "object" ? (v as Record<string, unknown>) : null;
  return typeof o?.[key] === "string" ? (o[key] as string) : "";
}

/**
 * The lease endpoints authenticate with a HEADER, not a body field
 * (internal/api/control_plane.go:737 reads X-Agent-Lease-Token and 400s without it),
 * and World.fleet() takes no headers. Reaching them through the recorder's own call()
 * keeps the exchange in the transcript and under the conformance lane exactly like
 * every other request -- the alternative is leaving four real operations untested.
 *
 * `body` defaults to `{}` rather than being omitted, and that is not cosmetic: the
 * spec declares no requestBody for heartbeat/release, but api.ContentTypeWithSkips
 * (internal/api/middleware.go:150) 415s every POST that carries no Content-Type. A
 * client that follows the spec literally cannot renew a lease -- see the report.
 */
function fleetAuthed(
  w: World,
  method: string,
  path: string,
  headers: Record<string, string>,
  body: unknown = {},
): ReturnType<typeof call> {
  return call(w.rec, { service: "fleetdb", baseUrl: w.stack.fleetUrl, method, path, body, headers });
}

export async function run(w: World): Promise<void> {
  const ws = w.workspace;
  const v1 = `/api/v1/${ws}`;

  // Every fleet-db entity below re-reads its workspace first
  // (internal/service/role_service.go:94, agent_service.go:146), so the workspace has
  // to exist before anything else is attempted. Created through loom, which is the
  // path a real operator uses; the packs share one workspace, so a repeat is a no-op
  // or a conflict rather than a problem.
  await w.loom("POST", "/api/workspaces", { name: ws, type: "empty" });
  await w.checkpoint("workspace-ready");

  // --- roles ---------------------------------------------------------------
  //
  // Field set from api.CreateRoleRequest (internal/api/roles.go:31). kind, executor
  // and effort are closed vocabularies validated in models/role.go:296-300; the
  // values here are the legal ones, because the point of this pack is to move real
  // state, not to probe the 400 path.
  await w.fleet("POST", `${v1}/roles`, {
    name: ROLE,
    description: "api-aft: reviews changes produced by the pack's agent",
    kind: "worker",
    executor: "turn",
    effort: "medium",
    backend: "claude",
    prompt: "Review the diff and report defects.",
    model: "claude-sonnet-4-5",
    labels: ["api-aft"],
    max_priority: 3,
    max_concurrency: 2,
    read_only: true,
    allowed_tools: ["Read", "Grep"],
    denied_tools: ["Bash"],
  });
  // The role name is the path parameter for /roles/{name}; binding it is what lets
  // the read sweep replay that GET against state that genuinely exists.
  w.bind("roles.name", ROLE);
  await w.checkpoint("role-created");

  await w.fleet("GET", `${v1}/roles`);
  await w.fleet("GET", `${v1}/roles/${ROLE}`);

  // input_policy never round-trips through models.Role.Validate on a PATCH, so
  // roles.go:216 checks it at the edge instead -- worth driving, since it is the one
  // update field with its own validation path. Default "ask" plus a per-kind
  // override exercises both halves of ValidateRoleInputPolicy.
  await w.fleet("PATCH", `${v1}/roles/${ROLE}`, {
    description: "api-aft: reviewer, revised",
    effort: "high",
    max_concurrency: 4,
    input_policy: { default: "ask", kinds: { file_write: "deny", web_fetch: "allow" } },
    max_run_duration: 900,
  });
  await w.checkpoint("role-updated");

  // clear_* booleans are a distinct code path from setting a value: they drop the
  // pointer field entirely (roles.go:57). A field that quietly survives its own
  // clear flag is exactly the dropped-write class this harness exists to catch.
  await w.fleet("PATCH", `${v1}/roles/${ROLE}`, {
    clear_max_run_duration: true,
    clear_concurrency: true,
  });
  await w.checkpoint("role-cleared");

  // --- agents --------------------------------------------------------------
  //
  // CreateAgent resolves role_name against a stored role (agent_service.go:172
  // validateReferences), so this only works because the role above was really
  // created. repos/repo_groups are left unset: they are validated against
  // repoNamePattern and a hermetic run has no repos registered.
  await w.fleet("POST", `${v1}/agents`, {
    name: AGENT,
    role_name: ROLE,
    auto: false,
    backend: "claude",
    fallback_backends: ["codex"],
    mode: "service",
    desired_state: "idle",
    max_concurrency: 1,
    task_filter: "type:task",
  });
  w.bind("agents.name", AGENT);
  await w.checkpoint("agent-created");

  await w.fleet("GET", `${v1}/agents`);
  await w.fleet("GET", `${v1}/agents/${AGENT}`);

  // GetAgent overlays a DERIVED live status over the stored record
  // (agent_service.go:213 overlayLiveStatus) that is never persisted. That makes the
  // agent read the single most interesting seam in this pack: the same entity is
  // about to acquire a session and a lease, which is precisely the join the overlay
  // computes from.
  await w.fleet("PATCH", `${v1}/agents/${AGENT}`, {
    desired_state: "running",
    auto: true,
    max_concurrency: 2,
  });
  await w.checkpoint("agent-updated");

  // --- agent sessions ------------------------------------------------------
  //
  // kind and status are closed enums (models/control_plane.go:137, :763). Sending
  // them explicitly rather than letting the server default is deliberate: the
  // defaulting branch is already covered by omission elsewhere, and a client that
  // states its intent is the shape loom's runner actually sends.
  const sess = await w.fleet("POST", `${v1}/agent-sessions`, {
    session_id: SESSION,
    agent_id: AGENT,
    kind: "task",
    status: "queued",
    phase: "plan",
    attempt: 1,
    metadata: { harness: "api-aft" },
  });
  const sessionId = str(sess.body, "session_id") || SESSION;
  w.bind("agent-sessions.session_id", sessionId);
  await w.checkpoint("session-created");

  await w.fleet("GET", `${v1}/agent-sessions`);
  await w.fleet("GET", `${v1}/agent-sessions/${sessionId}?limit=10`);

  await w.fleet("PATCH", `${v1}/agent-sessions/${sessionId}`, {
    status: "running",
    phase: "execute",
    summary: "api-aft drove this session through its lifecycle",
  });
  await w.checkpoint("session-running");

  // Heartbeat takes no body at all: the handler synthesizes
  // AgentSessionUpdate{LastHeartbeat: now} (control_plane.go:280).
  await w.fleet("POST", `${v1}/agent-sessions/${sessionId}/heartbeat`, {});
  await w.checkpoint("session-heartbeat");

  // --- agent leases --------------------------------------------------------
  //
  // A lease is created UNDER a session, not standalone: the session_id comes from
  // the path (control_plane.go:698), which is why models.AgentLease.Validate can
  // require it. The server mints the token; the client never chooses it.
  const leaseRes = await w.fleet("POST", `${v1}/agent-sessions/${sessionId}/leases`, {
    lease_id: LEASE,
    agent_id: AGENT,
    ttl_seconds: 300,
  });
  const leaseId = str(leaseRes.body, "lease_id") || LEASE;
  const leaseToken = str(leaseRes.body, "token");
  w.bind("agent-leases.lease_id", leaseId);
  await w.checkpoint("lease-created");

  await w.fleet("GET", `${v1}/agent-leases`);
  await w.fleet("GET", `${v1}/agent-leases/${leaseId}`);

  if (leaseToken) {
    // Renew, then release. Both are token-gated, and renewing before releasing means
    // the release runs against a lease whose fencing state has already advanced --
    // the ordering a real runner produces, and the one where a stale fencing token
    // would show up.
    await fleetAuthed(w, "POST", `${v1}/agent-leases/${leaseId}/heartbeat?ttl_seconds=600`, {
      "X-Agent-Lease-Token": leaseToken,
    });
    await w.checkpoint("lease-renewed");

    await fleetAuthed(w, "POST", `${v1}/agent-leases/${leaseId}/release`, {
      "X-Agent-Lease-Token": leaseToken,
    });
    await w.checkpoint("lease-released");
  }

  // --- agent ownership leases ----------------------------------------------
  //
  // A different lease family with a different key: this one is keyed by AGENT, one
  // lease per agent (control_plane.go:959 reads it by agent_id, not lease_id), and
  // it answers "who owns this agent" rather than "who holds this session". Note it
  // answers 200 on acquire where the sibling lease create answers 201.
  const own = await fleetAuthed(
    w,
    "POST",
    `${v1}/agent-ownership-leases/${AGENT}/acquire`,
    {},
    { owner_id: "api-aft-owner", runtime_provider: "local", ttl_seconds: 300 },
  );
  const ownToken = str(own.body, "token");
  w.bind("agent-ownership-leases.agent_id", AGENT);
  await w.checkpoint("ownership-lease-acquired");

  await w.fleet("GET", `${v1}/agent-ownership-leases`);
  await w.fleet("GET", `${v1}/agent-ownership-leases/${AGENT}`);

  if (ownToken) {
    const hdr = { "X-Agent-Ownership-Lease-Token": ownToken };
    await fleetAuthed(w, "POST", `${v1}/agent-ownership-leases/${AGENT}/heartbeat`, hdr);
    await w.checkpoint("ownership-lease-renewed");
    await fleetAuthed(w, "POST", `${v1}/agent-ownership-leases/${AGENT}/release`, hdr);
    await w.checkpoint("ownership-lease-released");
  }

  // --- agent commands ------------------------------------------------------
  //
  // Control-plane -> agent. The server is authoritative about status on create and
  // rejects anything but "queued" (control_plane.go:1015), so the lifecycle has to
  // be walked with ack and complete rather than written in one shot.
  const cmd = await w.fleet("POST", `${v1}/agent-commands`, {
    command_id: COMMAND,
    target_agent_id: AGENT,
    session_id: sessionId,
    type: "pause",
    payload: { reason: "api-aft drove a full command lifecycle" },
  });
  const commandId = str(cmd.body, "command_id") || COMMAND;
  w.bind("agent-commands.command_id", commandId);
  await w.checkpoint("command-queued");

  await w.fleet("GET", `${v1}/agent-commands`);
  await w.fleet("GET", `${v1}/agent-commands/${commandId}`);

  // ack records the acknowledging actor from the X-Actor header, which the wire
  // layer already sends on every call (src/wire.ts:49) -- so acked_by should come
  // back as "api-aft" and not empty.
  await w.fleet("POST", `${v1}/agent-commands/${commandId}/ack`, {});
  await w.checkpoint("command-acked");

  await w.fleet("POST", `${v1}/agent-commands/${commandId}/complete`, {
    status: "succeeded",
    result: "paused",
  });
  await w.checkpoint("command-completed");

  // --- agent inbox messages ------------------------------------------------
  //
  // The other direction of the same channel, and the more interesting queue: it has
  // a claim step with its own lease TTL, so a message moves queued -> claimed ->
  // delivered. Outcome is a closed set handled in storage (internal/storage/
  // control_plane.go:1061): delivered, retry, failed -- anything else is a 409-class
  // invalid transition.
  const msg = await w.fleet("POST", `${v1}/agent-inbox-messages`, {
    inbox_message_id: INBOX,
    target_agent_id: AGENT,
    session_id: sessionId,
    body: "api-aft: please review FLEET-1",
    source_kind: "api-aft",
    source_ref: "scenarios/fleetdb-roles-agents",
    dedupe_key: "api-aft-inbox-1",
  });
  const inboxId = str(msg.body, "inbox_message_id") || INBOX;
  w.bind("agent-inbox-messages.inbox_message_id", inboxId);
  await w.checkpoint("inbox-message-queued");

  await w.fleet("GET", `${v1}/agent-inbox-messages`);
  await w.fleet("GET", `${v1}/agent-inbox-messages/${inboxId}`);

  await w.fleet("POST", `${v1}/agent-inbox-messages/claim-next`, {
    target_agent_id: AGENT,
    session_id: sessionId,
    claimed_by: "api-aft",
    lease_ttl_ms: 60000,
  });
  await w.checkpoint("inbox-message-claimed");

  await w.fleet("POST", `${v1}/agent-inbox-messages/${inboxId}/complete`, {
    outcome: "delivered",
    delivered_thread_id: "api-aft-thread-1",
  });
  await w.checkpoint("inbox-message-delivered");

  // --- worker profiles -----------------------------------------------------
  //
  // The scheduling half of the same story: a profile says how many workers of a role
  // may run and where. `role` here is a plain string with no referential check
  // (models/platform.go:233 only requires it non-empty), unlike agent.role_name --
  // an asymmetry worth having on the record.
  const profile = await w.fleet("POST", `${v1}/worker-profiles`, {
    profile_id: PROFILE,
    name: "api-aft reviewer pool",
    role: ROLE,
    backend: "claude",
    runtime_policy: { placement: "local" },
    max_parallel: 2,
    labels: ["api-aft"],
    capabilities: ["review"],
    enabled: true,
    metadata: { owner: "api-aft" },
  });
  const profileId = str(profile.body, "profile_id") || PROFILE;
  w.bind("worker-profiles.profile_id", profileId);
  await w.checkpoint("worker-profile-created");

  await w.fleet("GET", `${v1}/worker-profiles?enabled=true`);
  await w.fleet("GET", `${v1}/worker-profiles/${profileId}`);

  await w.fleet("PATCH", `${v1}/worker-profiles/${profileId}`, {
    max_parallel: 3,
    enabled: false,
    capabilities: ["review", "triage"],
  });
  await w.checkpoint("worker-profile-updated");

  // --- agent services ------------------------------------------------------
  //
  // A long-lived service rather than a one-shot run. Validate enforces EXACTLY ONE
  // of role_name or a driver ref (models/platform.go:345), so sending role_name
  // alone is the only legal shape without a registered driver -- which a hermetic
  // run does not have.
  const svc = await w.fleet("POST", `${v1}/agent-services`, {
    service_id: SERVICE,
    name: "api-aft review service",
    kind: "support",
    desired_state: "stopped",
    role_name: ROLE,
    profile_name: PROFILE,
    max_instances: 1,
    restart_policy: "on_failure",
    permissions: ["issues:read"],
    metadata: { owner: "api-aft" },
  });
  const serviceId = str(svc.body, "service_id") || SERVICE;
  w.bind("agent-services.service_id", serviceId);
  await w.checkpoint("agent-service-created");

  await w.fleet("GET", `${v1}/agent-services`);
  await w.fleet("GET", `${v1}/agent-services/${serviceId}`);

  await w.fleet("PATCH", `${v1}/agent-services/${serviceId}`, {
    desired_state: "running",
    max_instances: 2,
  });
  await w.checkpoint("agent-service-running");

  // Close the session last so the whole control-plane graph existed simultaneously
  // for every checkpoint above; a terminal session at the end gives the invariants a
  // finished lifecycle to look at rather than only a live one.
  await w.fleet("PATCH", `${v1}/agent-sessions/${sessionId}`, {
    status: "completed",
    phase: "done",
    exit_code: 0,
  });
  await w.checkpoint("session-completed");
}
