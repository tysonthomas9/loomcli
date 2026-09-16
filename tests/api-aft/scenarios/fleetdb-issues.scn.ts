// Scenario: fleet-db's OWN issue surface, driven directly at /api/v1/{ws}/issues/...
//
// Why this family and not loom's: fleet-db is where the issue actually lives. loom's
// /api/workspaces/{ws}/issues is a compat facade over it, so every fact this pack
// writes through fleet-db is immediately observable through BOTH observers at the next
// checkpoint -- which is exactly the input the L2 seam differential needs. A field that
// fleet-db stores and loom's facade drops (tests/aft/FINDINGS.md 1.13 is that shape of
// bug) can only be seen by writing on one side and reading on both.
//
// It is also the deepest family in either spec: create, batch create, batch close,
// update, close, reopen, claim, release, release-lock, assign, defer, undefer,
// metadata, labels, comments, dependencies, children, count, list, search, ready,
// blocked, deferred, history and point-in-time reconstruction all hang off it. Every
// request shape below was read out of the Go handler and its request struct, not the
// spec: fleet-db/api/openapi.yaml has a dangling $ref and stale enums, and every
// decode goes through `dec.DisallowUnknownFields()`
// (fleet-db/internal/api/request.go:137), so one invented field is a 400, not a
// tolerated extra.
//
// There is not a single assertion here, and there must not be. State moves; the
// invariant registry checks.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";

export const name = "fleet-db's own issue surface: create, batch, deps, claim, defer, close, reopen";

type IssueLike = { id?: unknown; updated_at?: unknown };

/**
 * fleet-db writes the issue BARE at the response root (WriteJSON(w, 201, issue) --
 * fleet-db/internal/api/idempotency.go:166), unlike loom which wraps it. unwrap()
 * tolerates both, so one reader serves either observer.
 */
function idOf(body: unknown): string {
  const o = unwrap(body, "issue") as IssueLike | null;
  return typeof o?.id === "string" ? o.id : "";
}

function updatedAtOf(body: unknown): string {
  const o = unwrap(body, "issue") as IssueLike | null;
  return typeof o?.updated_at === "string" ? o.updated_at : "";
}

/** BatchCreateResponse is {issues, count} (fleet-db/internal/api/issues.go:189). */
function idsOf(body: unknown): string[] {
  const list = unwrap(body, "issues");
  if (!Array.isArray(list)) return [];
  return list.map((x) => idOf(x)).filter((x) => x !== "");
}

/**
 * fleet-db ships rate limiting ON by default: 100 tokens/second, burst 200, keyed by
 * client IP (fleet-db/internal/config/config.go:226). Every checkpoint costs about ten
 * fleet-db requests -- a read-back per tracked issue through each observer, the
 * mutation log, and admin/verify -- and loom's own reads land on the same 127.0.0.1
 * bucket, so a family this deep drains the burst in under two seconds and the rest of
 * the pack answers 429. Un-paced, that fills the transcript with rate-limit errors that
 * LOOK like fleet-db defects while actually being the harness out-running the limiter,
 * which is exactly the kind of noise that gets a capture-and-diff harness switched off.
 * 150ms per checkpoint replenishes more than a checkpoint costs.
 *
 * This is a workaround for a real gap, not a fact of life: loom's own e2e harness
 * raises the same limit by hand (loomcli/internal/driver/await_e2e_test.go:410), while
 * the embedded single-user spawn every developer actually runs does not.
 */
const pace = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 150));

export async function run(w: World): Promise<void> {
  const ws = w.workspace;
  const hourOut = new Date(Date.now() + 3_600_000).toISOString();
  const weekOut = new Date(Date.now() + 7 * 86_400_000).toISOString();

  // Every /api/v1/{workspace}/... route is wrapped by WorkspaceValidator, which 404s
  // until the workspace row exists (fleet-db/internal/api/workspace_middleware.go:79).
  // Provision through loom first: it is the operator surface, and it makes loom a real
  // second observer of everything below rather than a stranger that 404s.
  await w.loom("POST", "/api/workspaces", { name: ws, type: "empty" });
  // Then confirm on fleet-db's own side, and fall back to its native admin create
  // (fleet-db/internal/api/workspace.go:50) only if loom's provisioning did not land.
  // Probing first rather than firing a create-and-swallow-the-409 keeps a deliberate
  // conflict out of the transcript, where it would read as a finding to be triaged.
  const wsProbe = await w.fleet("GET", `/api/v1/admin/workspaces/${ws}`);
  if (wsProbe.status !== 200) {
    await w.fleet("POST", "/api/v1/admin/workspaces", {
      key: ws,
      name: "api-aft fleet-db issue surface",
      description: "Created by scenarios/fleetdb-issues.scn.ts",
    });
  }
  await pace();
  await w.checkpoint("fleet-workspace-ready");

  // --- the hub issue ---------------------------------------------------------
  //
  // Tracked FIRST on purpose. sweep.ts lookup() takes bindings[0] for `issues.id`, so
  // whichever issue is tracked first across the whole suite is the one every
  // /issues/{id}/... GET gets replayed against. Making THIS pack's first-tracked issue
  // the richest one in the workspace -- children, a dependency, a label, a metadata
  // key, a comment, and a claim lock still held when the pack ends -- is the difference
  // between the sweep scoring real 200s and scoring a page of honest-but-empty 404s on
  // /lock, /deps, /children and /at/{timestamp}.
  const hubRes = await w.fleet("POST", `/api/v1/${ws}/issues`, {
    title: "api-aft hub: fleet-db issue surface",
    description: "Hub issue for the fleet-db issue-surface pack.",
    type: "epic",
    priority: 1,
    labels: ["api-aft"],
    metadata: { origin: "api-aft" },
    due_at: weekOut,
  });
  const hub = idOf(hubRes.body);
  w.track(hub);
  await pace();
  await w.checkpoint("hub-created");

  // Without a hub there is nothing honest left to drive: every remaining path needs a
  // real id, and inventing one would exercise the 404 branch while scoring as coverage.
  if (!hub) return;

  // parent_id is validated against a real issue before the id is minted
  // (fleet-db/internal/service/issue_service.go:132), so this must follow the hub.
  const childRes = await w.fleet("POST", `/api/v1/${ws}/issues`, {
    title: "api-aft child: inherits the hub",
    description: "Child of the hub; exercises parent_id and GET /children.",
    type: "task",
    priority: 2,
    parent_id: hub,
  });
  const child = idOf(childRes.body);
  w.track(child);
  await pace();
  await w.checkpoint("child-created");

  // Batch create takes {issues: [CreateIssueRequest...]} and answers 201 with
  // {issues, count} (fleet-db/internal/api/issues.go:139). Titles differ from every
  // other create in this pack because the soft-duplicate guard fingerprints
  // (actor, title, resolved type) and would hand back the EXISTING issue with a 200
  // instead of minting a new one (issue_service.go:256) -- a silent id collision that
  // would make two variables point at one row.
  const batchRes = await w.fleet("POST", `/api/v1/${ws}/issues/batch`, {
    issues: [
      { title: "api-aft blocker: gates the child", type: "bug", priority: 0 },
      { title: "api-aft worker: claim and defer subject", type: "feature", priority: 3 },
    ],
  });
  const [blocker, worker] = idsOf(batchRes.body);
  w.track(blocker);
  w.track(worker);
  await pace();
  await w.checkpoint("batch-created");

  // --- annotate the hub ------------------------------------------------------
  // Each of these returns the whole updated issue rather than 204, so the seam gets a
  // full re-read for free (labels.go:66, metadata.go:69).
  await w.fleet("POST", `/api/v1/${ws}/issues/${hub}/labels`, { label: "seam" });
  // A real label on a real issue -- the DELETE /labels/{label} path parameter.
  w.bind("issues.label", "seam");

  // Keys travel in the URL path, so they are constrained to [A-Za-z0-9._-]+
  // (fleet-db/internal/models/metadata.go:24). "aft.run" is inside that charset.
  await w.fleet("PUT", `/api/v1/${ws}/issues/${hub}/metadata/aft.run`, { value: "fleetdb-issues" });
  w.bind("issues.key", "aft.run");

  await w.fleet("POST", `/api/v1/${ws}/issues/${hub}/comments`, {
    body: "Annotated by the api-aft fleet-db issue-surface pack.",
  });
  await pace();
  await w.checkpoint("hub-annotated");

  // --- the dependency graph --------------------------------------------------
  //
  // Two edge kinds on purpose. `related` is bidirectional and explicitly does NOT
  // affect readiness (models/dependency.go:52), so the hub stays claimable; `blocks`
  // does, so the child leaves the ready set and enters the blocked set. Driving both
  // in one workspace is what makes GET /ready and GET /blocked mean something at the
  // next checkpoint instead of both being empty.
  if (blocker) {
    await w.fleet("POST", `/api/v1/${ws}/issues/${hub}/deps`, {
      depends_on_id: blocker,
      type: "related",
    });
    // Dependency identity is the composite (issue, depends_on, type) -- there is no
    // separate dep id (models/dependency.go:88), so the path parameter IS the blocker.
    w.bind("issues.depends_on_id", blocker);
  }
  if (child && blocker) {
    await w.fleet("POST", `/api/v1/${ws}/issues/${child}/deps`, {
      depends_on_id: blocker,
      type: "blocks",
    });
  }
  await pace();
  await w.checkpoint("deps-wired");

  // --- the worker issue's lifecycle -----------------------------------------
  if (worker) {
    // PATCH refuses `closed` and `in_progress` outright: both have dedicated endpoints
    // that do more than move the field, so a plain write would leave a half-made
    // transition behind (models/status.go ValidateSettableStatus). `review` is one of
    // the four that a plain status write may set.
    await w.fleet("PATCH", `/api/v1/${ws}/issues/${worker}`, {
      status: "review",
      priority: 1,
      notes: "Moved to review by the fleet-db issue-surface pack.",
      description: "Updated description, to see whether both observers carry it.",
    });
    await pace();
    await w.checkpoint("worker-in-review");

    // assign is a distinct verb from claim: it changes the assignee WITHOUT taking a
    // lock or moving status (claim.go:36 -> issues.go assignIssue). The request field
    // is a *string so "" can mean explicit unassign, which is why the handler rejects
    // an absent `assignee` with 400 rather than treating it as a clear.
    await w.fleet("POST", `/api/v1/${ws}/issues/${worker}/assign`, { assignee: "api-aft-worker" });
    await pace();
    await w.checkpoint("worker-assigned");

    // defer_until must be strictly in the future or the command 400s
    // (service/commands.go DeferIssueCmd.Validate).
    await w.fleet("POST", `/api/v1/${ws}/issues/${worker}/defer`, { defer_until: hourOut });
    await w.fleet("GET", `/api/v1/${ws}/issues/deferred`);
    await pace();
    await w.checkpoint("worker-deferred");

    // undefer's handler decodes no body at all, so the honest request is bodyless --
    // but a bodyless POST is answered 415 missing_content_type, because the
    // ContentType middleware (fleet-db/internal/api/middleware.go:141) demands the
    // header on every POST/PUT/PATCH before any handler sees the request, whether or
    // not that handler reads a body. `{}` is the shape a client is forced into.
    await w.fleet("POST", `/api/v1/${ws}/issues/${worker}/undefer`, {});
    await pace();
    await w.checkpoint("worker-undeferred");

    // Claim is the atomic lock-plus-assign-plus-transition path (a Lua script in
    // storage). lock_ttl is in SECONDS and 0 means "use the 300s default".
    await w.fleet("POST", `/api/v1/${ws}/issues/${worker}/claim`, { lock_ttl: 120 });
    await w.fleet("GET", `/api/v1/${ws}/issues/${worker}/lock`);
    await pace();
    await w.checkpoint("worker-claimed");

    // release unwinds the whole claim (status back to open, assignee cleared, lock
    // dropped) and answers 204 with no body.
    await w.fleet("POST", `/api/v1/${ws}/issues/${worker}/release`, {});
    await pace();
    await w.checkpoint("worker-released");
  }

  // --- the blocker's lifecycle ----------------------------------------------
  if (blocker) {
    await w.fleet("POST", `/api/v1/${ws}/issues/${blocker}/claim`, { lock_ttl: 60 });

    // release-lock is deliberately NOT release: it drops only the distributed lock and
    // leaves status and assignee alone (claim.go:126). It is the supervisor's path for
    // an agent that already transitioned the issue itself, so afterwards this issue is
    // still in_progress while holding no lock -- a state `release` can never produce.
    await w.fleet("POST", `/api/v1/${ws}/issues/${blocker}/release-lock`, {});
    await pace();
    await w.checkpoint("blocker-lock-released");

    // Close from in_progress. The handler reads the issue back and returns it, and a
    // doubled close is success rather than a conflict (issues.go closeIssue), so the
    // second call here is a deliberate idempotency probe, not a mistake.
    await w.fleet("POST", `/api/v1/${ws}/issues/${blocker}/close`, {
      reason: "api-aft: blocker resolved",
    });
    await w.fleet("POST", `/api/v1/${ws}/issues/${blocker}/close`, {
      reason: "api-aft: deliberate second close, probing idempotency",
    });
    // Closing the blocker should unblock the child: /blocked and /ready both move here,
    // and INV-CLOSEDAT gets a real closed issue to check closed_at against.
    await pace();
    await w.checkpoint("blocker-closed");

    await w.fleet("POST", `/api/v1/${ws}/issues/${blocker}/reopen`, {});
    await pace();
    await w.checkpoint("blocker-reopened");
  }

  // Batch close takes issue_ids (snake_case) and answers 204 with NO body, unlike the
  // single close which answers 200 with the issue (issues.go:227). The asymmetry is
  // worth having on the transcript.
  if (child) {
    await w.fleet("POST", `/api/v1/${ws}/issues/batch/close`, {
      issue_ids: [child],
      reason: "api-aft: batch close",
    });
    await pace();
    await w.checkpoint("child-batch-closed");
  }

  // --- leave the hub claimed -------------------------------------------------
  //
  // Last write on purpose: the read sweep replays GET /issues/{id}/lock against
  // bindings[0], which is the hub. Leaving a live lock here is the difference between
  // that operation being genuinely exercised and it 404ing on "no lock exists".
  const hubClaim = await w.fleet("POST", `/api/v1/${ws}/issues/${hub}/claim`, { lock_ttl: 900 });

  // A point in time at which the hub provably existed, taken from the issue's own
  // updated_at rather than a clock read -- so /issues/{id}/at/{timestamp} reconstructs
  // a real issue instead of hitting ErrNoCreateEvent (history.go getAtTimestamp).
  const hubAt = updatedAtOf(hubClaim.body) || updatedAtOf(hubRes.body);
  if (hubAt) w.bind("issues.timestamp", hubAt);
  await pace();
  await w.checkpoint("hub-claimed");

  // --- reads the sweep cannot reach -----------------------------------------
  //
  // The sweep replays each GET once with no query string. These carry the filters,
  // and a filter is where a projection bug shows: a wrong index makes the unfiltered
  // list right and the filtered one wrong.
  await w.fleet("GET", `/api/v1/${ws}/issues?status=open&limit=10`);
  await w.fleet("GET", `/api/v1/${ws}/issues?type=epic&priority=1`);
  await w.fleet("GET", `/api/v1/${ws}/issues?label=api-aft`);
  await w.fleet("GET", `/api/v1/${ws}/issues?parent_id=${hub}`);
  await w.fleet("GET", `/api/v1/${ws}/issues/search?q=api-aft&limit=20`);
  await w.fleet("GET", `/api/v1/${ws}/issues/count`);
  await w.fleet("GET", `/api/v1/${ws}/issues/count?group_by=status`);
  await w.fleet("GET", `/api/v1/${ws}/issues/count?group_by=priority`);
  // ready accepts CSV filters and rejects priority + max_priority together
  // (ready.go parseReadyFilter), so max_priority alone is the legal shape here.
  await w.fleet("GET", `/api/v1/${ws}/issues/ready?max_priority=3`);
  await w.fleet("GET", `/api/v1/${ws}/issues/blocked`);
  // The workspace-root aliases loomcli's FleetBackend actually constructs
  // (ready.go:37) -- a different route registration reaching the same handler, so a
  // divergence between the two is a real defect and worth one call each.
  await w.fleet("GET", `/api/v1/${ws}/ready`);
  await w.fleet("GET", `/api/v1/${ws}/blocked`);
  await w.fleet("GET", `/api/v1/${ws}/labels`);
  await w.fleet("GET", `/api/v1/${ws}/issues/${hub}/children`);
  await w.fleet("GET", `/api/v1/${ws}/issues/${hub}/deps?type=related`);
  await w.fleet("GET", `/api/v1/${ws}/issues/${hub}/history?limit=50`);
  if (hubAt) {
    await w.fleet("GET", `/api/v1/${ws}/issues/${hub}/at/${encodeURIComponent(hubAt)}`);
    await w.fleet("GET", `/api/v2/${ws}/issues/${hub}/at/${encodeURIComponent(hubAt)}`);
  }
  await pace();
  await w.checkpoint("issue-surface-read-back");

  // The same reads through loom's facade, on the same ids, at the same instant. The
  // seam differential is only as good as the pairs it is given, and these are the
  // richest pairs this pack can hand it.
  await w.loom("GET", `/api/workspaces/${ws}/issues`);
  await w.loom("GET", `/api/workspaces/${ws}/ready`);
  await pace();
  await w.checkpoint("loom-facade-cross-read");
}
