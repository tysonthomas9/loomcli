// Scenario: the loom issue surface beyond create/close.
//
// create-readback-close.scn.ts proves the happy path exists. Everything that makes an
// issue tracker a tracker rather than a list -- dependency edges, the ready/blocked
// derivations that fall out of those edges, labels, comments, the atomic claim, the
// review decision, and the cross-workspace move -- lives on endpoints it never
// touches. Those are also the endpoints where the seam is most likely to lie: each is
// a loom-side composition over several fleet-db calls (move = create-in-target +
// comment-on-source + force-close-source, claim = lock + status update + re-read), so
// a partial failure can still answer 200 while the two observers have already drifted.
//
// The pack builds a small dependency graph out of real issues and walks it through
// those compositions, checkpointing after every state change so the L1/L2/L3 oracles
// see the seam mid-composition and not only at rest.
//
// No assertions here by design (scripts/check-no-asserts.mjs). What this file owes the
// suite is honest, real state -- created, never fabricated -- and the checkpoints that
// let the registry run against it.

import type { World } from "../src/world.ts";
import { unwrap } from "../src/world.ts";
import { call } from "../src/wire.ts";

export const name = "loom issue surface: deps, comments, labels, claim, review, move";

/** Second workspace, needed only as the move target. */
const MOVE_WS = "APIAFTMOVE";

/**
 * fleet-db meters every caller through one IP-keyed token bucket -- rate 100/s,
 * burst 200 (fleet-db/internal/config/config.go:226) -- and loom's embedded instance
 * is no exception. One loom write fans out to several fleet-db calls, so an
 * unthrottled pack drains the bucket in about a second and everything after that is
 * a 429 the harness would otherwise report as a pile of phantom findings. Pacing
 * before each checkpoint keeps the pack inside the budget. It hides nothing: the
 * limiter's answer is a 429 that loom relabels as a 503 with kind "unavailable",
 * which src/sweep.ts's PC-NO-5XX treats as declared degradation -- so an unpaced pack
 * does not fail loudly, it quietly stops testing anything.
 */
const pace = (): Promise<void> => new Promise((r) => setTimeout(r, 350));

/**
 * Create one issue and return its real id, or "" if creation failed. Returning ""
 * rather than a placeholder is deliberate: a fabricated id would exercise the 404
 * path and score it as coverage (src/sweep.ts), which is worse than an honest gap.
 *
 * Required fields are title/issue_type/priority (IssueCreateRequest,
 * internal/webui/handlers/issues/issues.go:82).
 */
async function create(w: World, body: Record<string, unknown>): Promise<string> {
  const res = await w.loom("POST", `/api/workspaces/${w.workspace}/issues`, {
    issue_type: "task",
    priority: 2,
    ...body,
  });
  const issue = unwrap(res.body, "issue") as { id?: unknown } | null;
  return typeof issue?.id === "string" ? issue.id : "";
}

export async function run(w: World): Promise<void> {
  // Type "empty" is the only synchronously-created kind: "clone" returns 202 with a
  // job id (internal/webui/handlers/workspace/create.go:26), which would leave the
  // move target unresolvable at the moment the move runs.
  await w.loom("POST", "/api/workspaces", { name: w.workspace, type: "empty" });
  await w.loom("POST", "/api/workspaces", { name: MOVE_WS, type: "empty" });
  await w.checkpoint("issue-surface-workspaces");

  // A blocker and a dependent. Labels go in at create time so the label-mutation
  // checkpoints later have a prior value to diff against rather than a nil.
  const blocker = await create(w, {
    title: "api-aft: blocker",
    description: "Nothing may proceed until this closes.",
    labels: ["api-aft", "blocker"],
  });
  const dependent = await create(w, {
    title: "api-aft: dependent",
    description: "Waits on the blocker.",
    labels: ["api-aft"],
  });
  w.track(blocker);
  w.track(dependent);
  await w.checkpoint("issue-pair-created");

  await dependencyEdge(w, dependent, blocker);
  await commentsAndLabels(w, dependent);

  if (blocker) {
    // Claim is the atomic one: lock + assign + force status=in_progress in a single
    // server-side step (issueServiceImpl.ClaimIssue). It runs on the BLOCKER, not the
    // dependent, because ensureClaimable() refuses an issue with an open ready-work
    // dependency -- claiming the dependent here would exercise the 409 path while
    // looking in the coverage table like it exercised the success path.
    await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${blocker}/claim`, {});
    await pace();
    await w.checkpoint("blocker-claimed");
  }

  await removeEdge(w, dependent, blocker);
  await reviewDecisions(w, dependent, blocker);
  await moveAndReopen(w);
  await readHistory(w, dependent);
}

/**
 * Add the edge, then read the three views that only mean anything once an edge
 * exists. Driving them here rather than leaving them to the read sweep is the whole
 * point: the sweep hits ready/blocked/graph whether or not the graph has any edges
 * in it, and an empty graph agrees with almost any implementation.
 */
async function dependencyEdge(w: World, dependent: string, blocker: string): Promise<void> {
  if (!dependent || !blocker) return;
  // dep_type defaults to "blocks" server-side (issue_impl.go:533); send it anyway so
  // the transcript records what was asked for rather than what was assumed.
  await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${dependent}/dependencies`, {
    depends_on_id: blocker,
    dep_type: "blocks",
  });
  // RemoveDependency's {depId} is the *depends-on* issue id, not an edge id: it is
  // passed straight through as backend.DepRemoveParams.ToID
  // (internal/webui/service/issue_impl.go:558). The blocker's id is therefore the
  // real, created value that belongs in this binding.
  w.bind("issues.depId", blocker);
  await pace();
  await w.checkpoint("dependency-added");

  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/dependencies`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/ready`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/blocked`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/graph?status=all&include_closed=true`);
}

async function commentsAndLabels(w: World, dependent: string): Promise<void> {
  if (!dependent) return;
  // The handler accepts "text" or "body" and prefers "text" (CommentRequest.Content,
  // internal/webui/handlers/issues/comments.go:26). GET .../comments is registered
  // (module.go:56) but absent from api/openapi.yaml, so reading it back is also the
  // only way this run can observe that half of the pair at all.
  await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${dependent}/comments`, {
    text: "api-aft: a comment written through the loom API.",
  });
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/comments`);
  await pace();
  await w.checkpoint("comment-added");

  // Labels have no endpoint of their own: they are three verbs on the PATCH body
  // (add_labels / remove_labels / set_labels, internal/webui/handlers/issues/issues.go:65).
  // add+remove in one request is the interesting case -- an order-dependent server
  // would show here, because "api-aft" is being removed while others are added.
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/issues/${dependent}`, {
    add_labels: ["needs-triage", "api-aft-labels"],
    remove_labels: ["api-aft"],
  });
  await pace();
  await w.checkpoint("labels-mutated");

  // set_labels is a replace, not a merge: whatever survived the add/remove above must
  // be gone after this. The filtered list is the independent readback -- label
  // storage and the label *index* the list filter queries are not the same thing, so
  // a write that lands in one and not the other is only visible from this angle.
  await w.loom("PATCH", `/api/workspaces/${w.workspace}/issues/${dependent}`, {
    set_labels: ["api-aft-final"],
  });
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues?labels=api-aft-final`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues?labels=api-aft`);
  await pace();
  await w.checkpoint("labels-replaced");
}

/**
 * Dropping the edge should move the dependent out of `blocked`, and -- since the
 * blocker was the only thing holding it -- into `ready`. Both views are re-read after
 * the checkpoint so the transcript carries a before/after pair around one write.
 */
async function removeEdge(w: World, dependent: string, blocker: string): Promise<void> {
  if (!dependent || !blocker) return;
  await w.loom("DELETE", `/api/workspaces/${w.workspace}/issues/${dependent}/dependencies/${blocker}`);
  await pace();
  await w.checkpoint("dependency-removed");
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/dependencies`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/blocked`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/ready`);
}

/**
 * The review decision is the only issue endpoint that requires a header:
 * X-Idempotency-Key becomes the DecisionID, and an empty one is a 400 before any
 * mutation (normalizeReviewDecisionParams, internal/webui/service/review_decision.go:73).
 * World.loom() cannot carry headers, so this goes through the same recorded wire
 * primitive World itself uses -- the exchange lands in the transcript and in the
 * conformance lane identically.
 */
async function reviewDecisions(w: World, dependent: string, blocker: string): Promise<void> {
  const decide = (id: string, key: string, body: unknown): Promise<unknown> =>
    call(w.rec, {
      service: "loom",
      baseUrl: w.stack.loomUrl,
      method: "POST",
      path: `/api/workspaces/${w.workspace}/issues/${id}/review-decision`,
      body,
      headers: { "X-Idempotency-Key": key },
    });
  const changes = { decision: "request_changes", reason: "api-aft: exercising the change-request path." };

  if (dependent) {
    // request_changes without a reason is rejected, so the reason is not decoration.
    // One decision writes three things through a single PatchIssue -- status=open, a
    // "needs-revision" label, and a marker appended to notes -- which is exactly the
    // multi-field composition the seam differential exists to watch.
    await decide(dependent, "api-aft-changes-1", changes);
    await pace();
    await w.checkpoint("review-changes-requested");

    // Same key, same body. The service recognises its own marker in notes and reports
    // replayed=true instead of appending a second one. A replay that still mutates is
    // a durability bug no single call can reveal.
    await decide(dependent, "api-aft-changes-1", changes);
    await pace();
    await w.checkpoint("review-changes-replayed");
  }

  if (blocker) {
    // approve closes the issue with the decision reason (applyApproval,
    // internal/webui/service/review_decision.go:107) -- so this is also a close that
    // arrives by a completely different route than POST .../close, and INV-CLOSEDAT
    // gets to check that route too.
    await decide(blocker, "api-aft-approve-1", {
      decision: "approve",
      reason: "api-aft: approved via the review-decision endpoint.",
    });
    await pace();
    await w.checkpoint("review-approved");
  }
}

/**
 * Move is a three-call composition -- create in the target, comment on the source,
 * force-close the source (moveIssueViaBackend, internal/webui/service/issue_move.go:104).
 * The source survives here as a closed issue, which is what keeps it observable by
 * both ledgers after the move; only the copy lives in MOVE_WS. A closed source is
 * then the natural subject for reopen, an endpoint the mux registers (module.go:48)
 * but api/openapi.yaml never documents.
 */
async function moveAndReopen(w: World): Promise<void> {
  const mover = await create(w, {
    title: "api-aft: mover",
    description: "Created to be moved to another workspace.",
  });
  if (!mover) return;
  w.track(mover);

  await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${mover}/move`, {
    target_workspace: MOVE_WS,
  });
  await pace();
  await w.checkpoint("issue-moved");

  // Reopen after the move's force-close. The pair matters for INV-CLOSEDAT: an issue
  // that comes back open still carrying the closed_at it was handed a moment ago is a
  // real defect, and it is only reachable by closing and reopening the same issue.
  await w.loom("POST", `/api/workspaces/${w.workspace}/issues/${mover}/reopen`, {
    reason: "api-aft: reopened after the move closed the source.",
  });
  await pace();
  await w.checkpoint("issue-reopened");
}

/**
 * History surfaces, read last so they see the whole run. `since=` is a distinct code
 * path from the newest-tail form: with a cursor loom returns one oldest-first page
 * and clamps the limit to fleet-db's 200 (api/openapi.yaml:875), while the bare form
 * returns the most recent tail. Journey then reconstructs status spans from the same
 * history, so the three reads together are the only view of whether this run's
 * fifteen-odd writes actually left an ordered, complete audit trail behind them.
 */
async function readHistory(w: World, dependent: string): Promise<void> {
  if (!dependent) return;
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/events?limit=100`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/events?since=&limit=50`);
  await w.loom("GET", `/api/workspaces/${w.workspace}/issues/${dependent}/journey`);
  await pace();
  await w.checkpoint("issue-history-read");
}
