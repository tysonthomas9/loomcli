import { useEffect, useState } from "react";
import {
  applyRevision,
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
  ) {
    const key = `${revision.change_id}:${revision.number}`;
    setBusy(key);
    setError("");
    try {
      const status = await submitRevisionVerdict(
        workspaceId,
        revision,
        verdict,
        detail,
        lead,
      );
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
        const decided = Boolean(revision.verdict);
        // The server reports an approved revision still waiting for a working
        // area, so Apply survives a reload. A follow status from this session
        // (e.g. a 404 from Apply clearing it) takes precedence.
        const needsArea =
          follow[key] !== undefined
            ? follow[key] === "approved_waiting_for_working_area"
            : Boolean(revision.needs_working_area);
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
            <div className={styles.actions}>
              <button
                type="button"
                disabled={disabled || decided}
                onClick={() => void decide(revision, "approve")}
              >
                Approve
              </button>
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
