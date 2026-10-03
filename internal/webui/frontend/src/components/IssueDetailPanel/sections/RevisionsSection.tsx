import { useEffect, useState } from "react";
import {
  applyRevision,
  approveRevisionMerge,
  cancelRevisionMerge,
  createRevisionPR,
  getTaskRevisions,
  submitRevisionVerdict,
  type ReviewRevision,
} from "@/hooks/api";
import { ApiError } from "@/types";
import styles from "./RevisionsSection.module.css";

export function RevisionsSection({
  workspaceId,
  taskId,
  lead,
  onChanged,
  changeId,
  revisions: snapshot,
  verdictsFor,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
  /** Called after a verdict or Apply changes what the task's diff compares with. */
  onChanged?: (() => void) | undefined;
  /** Show only this change (one repo of a cross-repo task). */
  changeId?: string | undefined;
  /**
   * Render this revisions list instead of fetching one. The caller reloads it
   * (via onChanged) after a verdict, so the buttons and the diff the caller
   * shows always come from the same snapshot.
   */
  revisions?: ReviewRevision[] | undefined;
  /**
   * The revision number whose diff is on screen: verdicts are enabled for that
   * exact revision only, and for none while it is null (no diff shown yet).
   * Undefined: no diff gating.
   */
  verdictsFor?: number | null | undefined;
}): JSX.Element {
  const controlled = snapshot !== undefined;
  const [revisions, setRevisions] = useState<ReviewRevision[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [override, setOverride] = useState("");
  const [reason, setReason] = useState("");
  const [follow, setFollow] = useState<Record<string, string>>({});
  const [menu, setMenu] = useState("");

  useEffect(() => {
    if (snapshot) {
      setRevisions(snapshot);
      setLoading(false);
      return;
    }
    let active = true;
    setLoading(true);
    getTaskRevisions(workspaceId, taskId, lead)
      .then((items) => {
        if (active) {
          setRevisions(items);
          setError("");
        }
      })
      .catch((err: unknown) => {
        if (active)
          setError(
            err instanceof Error ? err.message : "Could not load revisions",
          );
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [workspaceId, taskId, lead, snapshot]);

  async function decide(
    revision: ReviewRevision,
    verdict: "approve" | "reject" | "override",
    detail = "",
    approveOnly?: boolean,
    merge?: boolean,
  ) {
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setMenu("");
    setError("");
    try {
      const status = await (verdict === "approve"
        ? submitRevisionVerdict(
            workspaceId,
            revision,
            verdict,
            detail,
            lead,
            Boolean(approveOnly),
            ...(merge ? [true] : []),
          )
        : submitRevisionVerdict(workspaceId, revision, verdict, detail, lead));
      if (status) setFollow((prev) => ({ ...prev, [key]: status }));
      if (!controlled)
        setRevisions(await getTaskRevisions(workspaceId, taskId, lead));
      onChanged?.();
      setOverride("");
      setReason("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not record verdict");
    } finally {
      setBusy("");
    }
  }

  // Create PR for an approved change that is applied but has no PR yet.
  async function createPR(revision: ReviewRevision) {
    if (!lead) return;
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setError("");
    try {
      await createRevisionPR(workspaceId, lead, revision.change_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not create the PR");
    } finally {
      // The PR, or why it is still missing, comes from the server.
      setRevisions(await getTaskRevisions(workspaceId, taskId, lead));
      setBusy("");
    }
  }

  // Approve and merge on an open PR (D29): merges the bottom PR now, or
  // records the approval so it merges after the PRs below it.
  async function approveMerge(revision: ReviewRevision) {
    if (!lead) return;
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setError("");
    try {
      await approveRevisionMerge(workspaceId, revision, lead);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Could not approve the merge",
      );
    } finally {
      setRevisions(await getTaskRevisions(workspaceId, taskId, lead));
      setBusy("");
    }
  }

  async function cancelMerge(revision: ReviewRevision) {
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setError("");
    try {
      await cancelRevisionMerge(workspaceId, revision.change_id);
    } catch (err) {
      setError(
        err instanceof Error ? err.message : "Could not cancel auto-merge",
      );
    } finally {
      setRevisions(await getTaskRevisions(workspaceId, taskId, lead));
      setBusy("");
    }
  }

  async function apply(revision: ReviewRevision) {
    if (!lead) return;
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setError("");
    try {
      await applyRevision(workspaceId, revision, lead);
      setFollow((prev) => ({ ...prev, [key]: "" }));
      // Applied state comes from the server's applied log, never browser state.
      if (!controlled)
        setRevisions(await getTaskRevisions(workspaceId, taskId, lead));
      onChanged?.();
    } catch (err) {
      // 404: the lead agent does not exist, so Apply cannot open its area.
      if (err instanceof ApiError && err.status === 404)
        setFollow((prev) => ({ ...prev, [key]: "" }));
      setError(err instanceof Error ? err.message : "Could not apply revision");
    } finally {
      setBusy("");
    }
  }

  // Review a task, not a revision: only each change's newest revision is
  // reviewable here. Earlier ones are read-only under Changes → History.
  const current = newestRevisions(revisions).filter(
    (r) => !changeId || r.change_id === changeId,
  );

  return (
    <section
      className={styles.section}
      aria-label="Revisions"
      data-testid="revisions-section"
    >
      <h3>Revisions</h3>
      {loading && <p>Loading revisions…</p>}
      {error && <p role="alert">{error}</p>}
      {!loading && revisions.length === 0 && <p>No revisions yet.</p>}
      {current.map((revision) => {
        const key = `${revision.change_id}:${revision.number}`;
        // An attempt that changed nothing closes the task: no review, no
        // apply and no PR, so there is nothing to decide (the server refuses
        // verdicts with no_changes).
        if (revision.no_changes)
          return (
            <div
              className={styles.revision}
              key={key}
              data-testid="revision-no-changes"
            >
              <div>
                <strong>Revision {revision.number}</strong>{" "}
                <code>{revision.head_sha.slice(0, 12)}</code>
              </div>
              <div>No changes</div>
            </div>
          );
        const disabled =
          Boolean(busy) ||
          revision.incomplete ||
          (verdictsFor !== undefined && revision.number !== verdictsFor);
        // The list reports the verdict for this exact revision head, so a new
        // derived revision has none and offers the buttons again.
        // A spent approval was never applied and can no longer apply (e.g. the
        // change was unapplied first): show why and let the reviewer approve
        // again, which re-arms the follow.
        const spent = revision.follow_status === "spent";
        const decided = Boolean(revision.verdict) && !spent;
        // The server reports an approved revision still waiting for a working
        // area, so Apply survives a reload. A follow status from this session
        // (e.g. a 404 from Apply clearing it) takes precedence.
        const needsArea =
          follow[key] !== undefined
            ? follow[key] === "approved_waiting_for_working_area"
            : Boolean(revision.needs_working_area);
        const prOpen = Boolean(revision.pr_number);
        const approved =
          revision.verdict === "approve" ||
          revision.verdict === "override" ||
          revision.verdict === "policy" ||
          // A revision Apply derived onto a moved working area carries the approval.
          revision.verdict === "carried";
        // InWorkingArea: approved and applied with no PR yet (Approve only,
        // or Approve and create PR that could not publish).
        const canCreatePR = approved && revision.applied && !prOpen;
        const merge = mergeState(revision, approved, decided);
        return (
          <div className={styles.revision} key={key}>
            <div>
              <strong>Revision {revision.number}</strong>{" "}
              <code>{revision.head_sha.slice(0, 12)}</code>
            </div>
            <div>
              {revision.incomplete
                ? "Incomplete capture"
                : (revision.verdict ?? "Awaiting review")}
            </div>
            {needsArea && (
              <div className={styles.actions}>
                <span>
                  {lead
                    ? "Approved: Apply to create the lead working area"
                    : "Approved: Apply needs a single workspace lead"}
                </span>
                <button
                  type="button"
                  disabled={Boolean(busy) || !lead}
                  onClick={() => void apply(revision)}
                >
                  Apply
                </button>
              </div>
            )}
            {revision.applied && <div>Applied</div>}
            {prOpen && (
              <div data-testid="revision-pr">
                PR{" "}
                <a href={revision.pr_url} target="_blank" rel="noreferrer">
                  #{revision.pr_number}
                </a>{" "}
                is open
              </div>
            )}
            {!prOpen && revision.publish_reason && (
              <div data-testid="revision-publish-status">
                {revision.publish_status === "not_published"
                  ? revision.publish_reason
                  : `PR not opened yet: ${revision.publish_reason}`}
              </div>
            )}
            {canCreatePR && (
              <div className={styles.actions}>
                <button
                  type="button"
                  data-testid="create-pr"
                  disabled={Boolean(busy) || !lead}
                  onClick={() => void createPR(revision)}
                >
                  Create PR
                </button>
              </div>
            )}
            {spent && (
              <div role="status" data-testid="revision-follow-spent">
                Not applied:{" "}
                {revision.follow_reason || "this approval can no longer apply"}
              </div>
            )}
            {merge.status && (
              <div data-testid="merge-status" className={styles.mergeStatus}>
                {merge.status}
                {merge.canCancel && (
                  <button
                    type="button"
                    data-testid="cancel-auto-merge"
                    disabled={Boolean(busy)}
                    onClick={() => void cancelMerge(revision)}
                  >
                    Cancel auto-merge
                  </button>
                )}
              </div>
            )}
            <div className={styles.actions}>
              <span className={styles.split}>
                {prOpen ? (
                  merge.action !== "none" && (
                    <button
                      type="button"
                      data-testid="approve-merge"
                      disabled={disabled || (merge.action === "merge" && !lead)}
                      onClick={() =>
                        void (merge.action === "merge"
                          ? approveMerge(revision)
                          : decide(revision, "approve", "", false, true))
                      }
                    >
                      {merge.label}
                    </button>
                  )
                ) : (
                  <button
                    type="button"
                    data-testid="approve-create-pr"
                    disabled={disabled || decided}
                    onClick={() => void decide(revision, "approve", "", false)}
                  >
                    Approve and create PR
                  </button>
                )}
                {/* No "Approve only" once the PR is open (D29). */}
                {!prOpen && (
                  <button
                    type="button"
                    aria-label="More approve options"
                    aria-haspopup="menu"
                    aria-expanded={menu === key}
                    data-testid="approve-menu-toggle"
                    disabled={disabled || decided}
                    onClick={() => setMenu(menu === key ? "" : key)}
                  >
                    ▾
                  </button>
                )}
                {menu === key && !prOpen && !decided && (
                  <span role="menu" className={styles.menu}>
                    <button
                      type="button"
                      role="menuitem"
                      data-testid="approve-only"
                      disabled={disabled}
                      onClick={() => void decide(revision, "approve", "", true)}
                    >
                      Approve only
                    </button>
                  </span>
                )}
              </span>
              <button
                type="button"
                disabled={disabled || decided}
                onClick={() => void decide(revision, "reject")}
              >
                Reject
              </button>
              <button
                type="button"
                disabled={disabled || decided}
                onClick={() => {
                  setOverride(key);
                  setReason("");
                }}
              >
                Override
              </button>
            </div>
            {override === key && !decided && (
              <div className={styles.override}>
                <label htmlFor={`override-reason-${revision.number}`}>
                  Override reason
                </label>
                <input
                  id={`override-reason-${revision.number}`}
                  value={reason}
                  onChange={(e) => setReason(e.target.value)}
                />
                <button
                  type="button"
                  disabled={disabled || !reason.trim()}
                  onClick={() =>
                    void decide(revision, "override", reason.trim())
                  }
                >
                  Record override
                </button>
              </div>
            )}
          </div>
        );
      })}
    </section>
  );
}

/** The newest revision of each change, in list order. */
export function newestRevisions(revisions: ReviewRevision[]): ReviewRevision[] {
  const newest = new Map<string, number>();
  for (const r of revisions)
    newest.set(r.change_id, Math.max(newest.get(r.change_id) ?? 0, r.number));
  return revisions.filter((r) => newest.get(r.change_id) === r.number);
}

const activeMerge = ["waiting", "blocked", "merging"];

/**
 * What a revision with an open PR offers (D29): Approve and merge on the
 * bottom PR, Approve, merge after #N on a higher one, and the state of an
 * approval already made. "verdict" approves a new version and its merge in
 * one click; "merge" approves the merge of the version already on the PR.
 */
export function mergeState(
  revision: ReviewRevision,
  approved: boolean,
  decided: boolean,
): {
  action: "none" | "merge" | "verdict";
  label: string;
  status: string;
  canCancel: boolean;
} {
  const below = revision.merge_after ?? [];
  const label =
    below.length > 0
      ? `Approve, merge after #${below[below.length - 1]}`
      : "Approve and merge";
  const status = revision.merge_status ?? "";
  const reason = revision.merge_reason ?? "";
  const after = below.map((n) => `#${n}`).join(", ");
  const text: Record<string, string> = {
    waiting:
      reason.startsWith("merges after") && after
        ? `Approved, merges after ${after}`
        : `Approved, waiting: ${reason}`,
    blocked: `Approved, merge blocked: ${reason}. Retries when it passes.`,
    merging: "Merging…",
    merged: "Merged",
    stale_subject: `Not merged: ${reason}. Approve the new version to merge it.`,
    reapproval_required: `Not merged: ${reason}`,
    cancelled: "Auto-merge cancelled",
  };
  let action: "none" | "merge" | "verdict" = "none";
  if (
    revision.pr_number &&
    !activeMerge.includes(status) &&
    status !== "merged"
  ) {
    // After someone else pushed (stale_subject), only a new version that the
    // human has not decided yet can be approved to merge.
    if (!decided) action = "verdict";
    else if (
      approved &&
      status !== "stale_subject" &&
      revision.head_sha === revision.pr_head
    )
      action = "merge";
  }
  return {
    action,
    label,
    status: revision.pr_number ? (text[status] ?? "") : "",
    canCancel: status === "waiting" || status === "blocked",
  };
}
