// Scenario: the fleet-db control surface an operator (not an agent) touches.
//
// Everything here sits OUTSIDE the issue lifecycle that the rest of the suite
// exercises, and that is exactly why it is worth driving. These are the endpoints
// that provision the tenancy (admin workspaces, API keys), the ones that carry the
// disaster-recovery story (replay / export / import), and the ones that hold the
// agent-configuration corpus (skills, skill packs, skill-materialization leases,
// repos). They are written rarely and read constantly, so a regression in them is
// both cheap to ship and expensive to notice.
//
// Two deliberate choices about blast radius:
//
//   1. The replay/export/import trio runs against a workspace this pack CREATES,
//      never against APIAFT. recovery.FullReplay() flushes all derived state for a
//      workspace and rebuilds it from the event log
//      (fleet-db/internal/recovery/service.go:91) -- asynchronously. Pointing that
//      at the shared workspace would race the L1 verify oracle every other pack
//      depends on, and a half-rebuilt read model would be reported as a fleet-db
//      defect rather than as this pack's fault.
//   2. Everything that must be reachable by the read sweep (skills, repos, packs)
//      is created in APIAFT, because the sweep substitutes the global `workspace`
//      binding and that is APIAFT.
//
// There is not a single check in this file. Every call moves real state and every
// checkpoint hands both observers to the invariant registry.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";

export const name = "fleet-db admin, skills, repos and recovery surfaces";

// Workspace keys are 1-32 uppercase alphanumerics or hyphens, starting with a
// letter, not ending with one (fleet-db/internal/models/workspace.go:71).
const ISOLATED_WS = "APIAFTADMIN";

// Skill and skill-pack names share one stricter pattern -- lowercase, digits and
// internal hyphens only -- because a skill name becomes a directory name verbatim
// when a client materializes it (fleet-db/internal/models/skill.go:28).
const WS_SKILL = "apiaft-admin-pack";
const THROWAWAY_SKILL = "apiaft-throwaway";
// Role names are looser (dots and underscores allowed), so one name can legally be
// BOTH a role and a skill. That matters here: `family()` in src/plan.ts maps every
// /roles/... path to the family "roles", so the single binding `roles.name` is
// consumed as a role name by /roles/{name} and as a skill name by
// /roles/{role}/skills/{name}. Creating a role and a role-scoped skill that share a
// name is the only way to make that binding honest for both readers.
const ROLE = "apiaft-skills";

export async function run(w: World): Promise<void> {
  // The shared workspace is not a given. A filtered run (`run fleetdb-admin`) loads
  // only this pack, and nothing in the harness itself creates APIAFT -- preflight
  // probes but never writes. So every pack has to be able to stand its own world up.
  // This is the shape loom's real client sends
  // (loomcli/internal/webui/service/workspace_types.go:12); re-sending it when the
  // workspace already exists is how the reference pack behaves too.
  await w.loom("POST", "/api/workspaces", { name: w.workspace, type: "empty" });
  await w.checkpoint("workspace-ready");

  // ---- Admin tenancy -------------------------------------------------------
  // key + name are the only required fields; created_at/updated_at are stamped by
  // the handler, not the caller (fleet-db/internal/api/workspace.go:99).
  await w.fleet("POST", "/api/v1/admin/workspaces", {
    key: ISOLATED_WS,
    name: "api-aft admin scratch",
    description: "Isolated target for replay/export/import so APIAFT is never flushed.",
    default_branch: "main",
    design_format: "markdown",
  });
  await w.fleet("GET", "/api/v1/admin/workspaces");
  await w.fleet("GET", `/api/v1/admin/workspaces/${ISOLATED_WS}`);
  // The shared workspace exists in fleet-db too (preflight's write probe put issues
  // in it), so it is a legitimate second binding for {key} -- and a far stronger
  // subject for the integrity read the sweep will run.
  await w.fleet("GET", `/api/v1/admin/workspaces/${w.workspace}`);
  w.bind("admin.key", w.workspace);
  w.bind("admin.key", ISOLATED_WS);

  // Only the mutable fields; `key` is immutable after creation.
  await w.fleet("PATCH", `/api/v1/admin/workspaces/${ISOLATED_WS}`, {
    description: "Renamed by api-aft to prove PATCH lands.",
    default_branch: "trunk",
  });
  await w.checkpoint("admin-workspace-provisioned");

  // ---- API keys ------------------------------------------------------------
  // {id} in /admin/apikeys/{id} is the ACTOR ID, not a key id: the handler scans
  // the keystore for a matching Actor.ID (fleet-db/internal/api/apikey.go:225). The
  // raw key itself is returned once and never again, so it cannot be a path binding.
  const kept = "api-aft-kept-actor";
  const ephemeral = "api-aft-ephemeral-actor";
  await w.fleet("POST", "/api/v1/admin/apikeys", {
    actor_id: kept,
    display_name: "api-aft kept key",
    // 0 means permanent; anything else must be 60..31536000 seconds.
    ttl_seconds: 3600,
  });
  // workspace and role must travel together or not at all (apikey.go:122) -- this
  // is the scoped-provisioning lane, which also writes an ACL role.
  await w.fleet("POST", "/api/v1/admin/apikeys", {
    actor_id: ephemeral,
    display_name: "api-aft ephemeral key",
    workspace: ISOLATED_WS,
    role: "viewer",
  });
  w.bind("admin.id", kept);

  await w.fleet("GET", "/api/v1/admin/apikeys");
  await w.fleet("GET", `/api/v1/admin/apikeys/${kept}`);
  // Revoke drops the key AND the scoped ACL role, and answers 204 even when nothing
  // matched -- it is documented as idempotent (apikey.go:315).
  await w.fleet("DELETE", `/api/v1/admin/apikeys/${ephemeral}`);
  await w.checkpoint("apikeys-provisioned");

  // ---- Repos ---------------------------------------------------------------
  // remote/default_branch are defaulted server-side to origin/main before
  // validation, so a minimal body is a real client shape (repos.go:97).
  await w.fleet("POST", `/api/v1/${w.workspace}/repos`, {
    name: "api-aft-repo",
    remote_url: "https://github.com/tysonthomas9/fleet-db.git",
    groups: ["infra"],
  });
  w.bind("repos.name", "api-aft-repo");
  await w.fleet("GET", `/api/v1/${w.workspace}/repos`);
  await w.fleet("GET", `/api/v1/${w.workspace}/repos/api-aft-repo`);
  await w.fleet("PATCH", `/api/v1/${w.workspace}/repos/api-aft-repo`, {
    default_branch: "develop",
    groups: ["infra", "control-plane"],
  });
  await w.checkpoint("repo-created");

  // ---- Roles + skills ------------------------------------------------------
  // A role needs only a name to validate (fleet-db/internal/models/role.go:313);
  // everything else is optional. Created here so `roles.name` denotes something
  // real on BOTH paths that consume it.
  await w.fleet("POST", `/api/v1/${w.workspace}/roles`, {
    name: ROLE,
    description: "api-aft role scope for skill reads",
    kind: "worker",
  });
  w.bind("roles.role", ROLE);
  w.bind("roles.name", ROLE);

  // There is no created_by field on the wire: provenance comes from the
  // authenticated actor / X-Actor, never the body, so a writer cannot claim to be
  // someone else (fleet-db/internal/api/skills.go:82).
  await w.fleet("POST", `/api/v1/${w.workspace}/skills`, {
    name: WS_SKILL,
    description: "Workspace-scoped skill created by api-aft.",
    content: "# api-aft admin pack\n\nWorkspace scope.\n",
    files: [
      { path: "references/notes.md", content: "reference material\n" },
      { path: "scripts/check.sh", content: "#!/bin/sh\nexit 0\n", executable: true },
    ],
    source: "api-aft",
  });
  w.bind("skills.name", WS_SKILL);
  // SKILL.md is the reserved path that addresses the skill's own body rather than a
  // bundled file, so it is present on every skill that has content (skills.go:718).
  w.bind("skills.path", "SKILL.md");

  await w.fleet("GET", `/api/v1/${w.workspace}/skills`);
  await w.fleet("GET", `/api/v1/${w.workspace}/skills?scope=workspace`);
  await w.fleet("GET", `/api/v1/${w.workspace}/skills/${WS_SKILL}`);
  await w.checkpoint("workspace-skill-created");

  // PUT is the provenance-guarded upsert. A body that names a skill must agree with
  // the path or the write is rejected outright (skills.go:301) -- send the same name.
  await w.fleet("PUT", `/api/v1/${w.workspace}/skills/${WS_SKILL}`, {
    name: WS_SKILL,
    description: "Workspace-scoped skill, upserted by api-aft.",
    content: "# api-aft admin pack\n\nUpserted.\n",
    source: "api-aft",
  });
  // PATCH never round-trips through Skill.Validate, so `files` here REPLACES the
  // whole set rather than merging (skills.go:96).
  await w.fleet("PATCH", `/api/v1/${w.workspace}/skills/${WS_SKILL}`, {
    description: "Workspace-scoped skill, patched by api-aft.",
    files: [{ path: "references/notes.md", content: "reference material, revised\n" }],
  });
  await w.checkpoint("workspace-skill-updated");

  // ---- Per-document skill lane --------------------------------------------
  // An editor saves one file at a time, so the write it wants is "this document".
  // The precondition rides in If-Match, which this harness's wire layer does not
  // send -- an unconditional PUT is still the shape a first-write client uses.
  await w.fleet("GET", `/api/v1/${w.workspace}/skills/${WS_SKILL}/files/SKILL.md`);
  await w.fleet("GET", `/api/v1/${w.workspace}/skills/${WS_SKILL}/files/references/notes.md`);
  await w.fleet("PUT", `/api/v1/${w.workspace}/skills/${WS_SKILL}/files/references/api.md`, {
    content: "# api reference\n\nWritten one document at a time.\n",
    source: "api-aft",
  });
  await w.checkpoint("skill-file-written");

  // DELETE carries no body, so `source` travels as a query parameter -- recorded,
  // not trusted (skills.go:697).
  await w.fleet("DELETE", `/api/v1/${w.workspace}/skills/${WS_SKILL}/files/references/api.md?source=api-aft`);

  // force-upsert is its own route rather than a query flag, because the authorizer
  // binds one permission to one route pattern and cannot read a query string
  // (skills.go:210). Driving it proves the privileged lane is wired, not just the
  // ordinary one.
  await w.fleet("POST", `/api/v1/${w.workspace}/skills/${WS_SKILL}/force-upsert`, {
    name: WS_SKILL,
    description: "Workspace-scoped skill, force-upserted by api-aft.",
    content: "# api-aft admin pack\n\nForce-upserted.\n",
    source: "api-aft",
  });
  await w.checkpoint("skill-force-upserted");

  // ---- Role-scoped skills --------------------------------------------------
  // The name matches ROLE on purpose; see the constant's comment.
  await w.fleet("POST", `/api/v1/${w.workspace}/roles/${ROLE}/skills`, {
    name: ROLE,
    description: "Role-scoped skill created by api-aft.",
    content: "# role scope\n\nOwned by the role lane.\n",
    source: "api-aft",
  });
  w.bind("roles.path", "SKILL.md");
  await w.fleet("GET", `/api/v1/${w.workspace}/roles/${ROLE}/skills`);
  await w.fleet("GET", `/api/v1/${w.workspace}/roles/${ROLE}/skills/${ROLE}`);
  await w.fleet("GET", `/api/v1/${w.workspace}/roles/${ROLE}/skills/${ROLE}/files/SKILL.md`);
  await w.fleet("PATCH", `/api/v1/${w.workspace}/roles/${ROLE}/skills/${ROLE}`, {
    description: "Role-scoped skill, patched by api-aft.",
  });
  await w.checkpoint("role-skill-created");

  // A create-then-delete pair on a name nothing binds: the delete lane needs
  // exercising, and doing it to a bound skill would leave the sweep reading a 404.
  await w.fleet("POST", `/api/v1/${w.workspace}/skills`, {
    name: THROWAWAY_SKILL,
    description: "Created only to exercise the delete lane.",
    content: "# throwaway\n",
    source: "api-aft",
  });
  await w.fleet("DELETE", `/api/v1/${w.workspace}/skills/${THROWAWAY_SKILL}?source=api-aft`);
  await w.checkpoint("skill-deleted");

  // ---- Skill packs ---------------------------------------------------------
  // repo_url must use a transport a git client actually speaks; a file:// or bare
  // local path is refused because it would sync different content per machine
  // (fleet-db/internal/models/skill_pack.go:222).
  await w.fleet("POST", `/api/v1/${w.workspace}/skill-packs`, {
    name: "api-aft-pack",
    repo_url: "https://github.com/tysonthomas9/fleet-db.git",
    ref: "main",
    path: "docs/skills",
    description: "Skill pack registered by api-aft.",
  });
  w.bind("skill-packs.name", "api-aft-pack");
  await w.fleet("GET", `/api/v1/${w.workspace}/skill-packs`);
  await w.fleet("GET", `/api/v1/${w.workspace}/skill-packs/api-aft-pack`);
  // The last-sync fields arrive together under record_sync because they describe one
  // event; a caller able to set the status alone would leave the record asserting an
  // outcome with no time and no commit behind it (skills.go:143).
  await w.fleet("PATCH", `/api/v1/${w.workspace}/skill-packs/api-aft-pack`, {
    description: "Skill pack, sync recorded by api-aft.",
    record_sync: {
      status: "ok",
      commit: "0000000000000000000000000000000000000000",
      skills: [WS_SKILL],
    },
  });
  await w.checkpoint("skill-pack-synced");

  // ---- Skill-materialization leases ---------------------------------------
  // Ephemeral (Redis-TTL) coordination, not event-sourced: acquire returns the token
  // that renew and release must present. No GET exists, so nothing here is bound --
  // the lease is released at the end of this block and would 404 for a later reader.
  const lease = await w.fleet("POST", `/api/v1/${w.workspace}/skill-materialization-leases`, {
    holder: "api-aft",
    target_key: `skill:${WS_SKILL}`,
    ttl_seconds: 60,
  });
  const token = (unwrap(lease.body, "lease") as { token?: string } | null)?.token;
  const target = encodeURIComponent(`skill:${WS_SKILL}`);
  if (token) {
    await w.fleet("PUT", `/api/v1/${w.workspace}/skill-materialization-leases/${target}`, {
      token,
      ttl_seconds: 120,
    });
    await w.fleet("DELETE", `/api/v1/${w.workspace}/skill-materialization-leases/${target}`, { token });
  }
  await w.checkpoint("lease-cycle-complete");

  // ---- History -------------------------------------------------------------
  // History reads the event log directly rather than the read model, so it is the
  // one surface that can disagree with the projection without verify noticing.
  // Create through loom so the seam differential (L2) has both observers.
  const created = await w.loom("POST", `/api/workspaces/${w.workspace}/issues`, {
    title: "api-aft: history subject",
    issue_type: "task",
    priority: 2,
    description: "Created so the history and point-in-time lanes have a real subject.",
  });
  const issue = unwrap(created.body, "issue") as { id?: string } | null;
  if (issue?.id) {
    w.track(issue.id);
    await w.loom("PATCH", `/api/workspaces/${w.workspace}/issues/${issue.id}`, {
      status: "in_progress",
      description: "Second event, so the timeline has more than a create.",
    });
    await w.checkpoint("history-subject-created");

    await w.fleet("GET", `/api/v1/${w.workspace}/issues/${issue.id}/history`);
    // since must be "0" or a stream ID; limit is capped at 200 (history.go:131,185).
    await w.fleet("GET", `/api/v1/${w.workspace}/issues/${issue.id}/history?limit=200&since=0`);
    // The action filter takes fully-qualified event actions ("issue.create"), not
    // the bare verb: models.Action.IsValid rejects "create" outright
    // (fleet-db/internal/models/event.go:18).
    await w.fleet("GET", `/api/v1/${w.workspace}/issues/${issue.id}/history?action=issue.create,issue.update`);
    // A timestamp AFTER both writes reconstructs to a state that really existed, so
    // this binding is derived from created state rather than invented.
    const at = new Date(Date.now() + 1000).toISOString();
    w.bind("issues.timestamp", at);
    await w.fleet("GET", `/api/v1/${w.workspace}/issues/${issue.id}/at/${encodeURIComponent(at)}`);
    await w.fleet("GET", `/api/v2/${w.workspace}/issues/${issue.id}/history`);
  }
  await w.checkpoint("history-read");

  // ---- Recovery, on the isolated workspace only ---------------------------
  // Import refuses once the target stream is non-empty
  // (fleet-db/internal/recovery/import.go:61), and it is workspace-scoped, so the
  // workspace must already exist to get past the validator. Creating one appends
  // workspace.create to that same stream -- which means the precondition can never
  // hold for any workspace reachable over HTTP, and this call is expected to answer
  // 409. It is driven anyway, because "the only reachable outcome is the error" is a
  // fact about the endpoint worth having in the transcript.
  await w.fleet("POST", `/api/v1/${ISOLATED_WS}/admin/import`);
  // Export answers application/x-ndjson, so the recorder logs a parse error on a 200
  // -- that is the endpoint's real contract, not a fault.
  await w.fleet("POST", `/api/v1/${ISOLATED_WS}/admin/export`);

  // Replay is 202-and-poll: it flushes derived state and rebuilds from the log in a
  // goroutine (recovery.go:66). Poll to completion BEFORE the next checkpoint, so no
  // oracle ever observes a half-rebuilt read model.
  await w.fleet("POST", `/api/v1/${ISOLATED_WS}/admin/replay`, {});
  for (let i = 0; i < 20; i++) {
    const status = await w.fleet("GET", `/api/v1/${ISOLATED_WS}/admin/replay/status`);
    if ((status.body as { done?: boolean } | null)?.done) break;
    await new Promise((r) => setTimeout(r, 250));
  }
  await w.checkpoint("recovery-replayed");
}
