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
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
}): JSX.Element {
  const [revisions, setRevisions] = useState<ReviewRevision[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [override, setOverride] = useState("");
  const [reason, setReason] = useState("");
  const [follow, setFollow] = useState<Record<string, string>>({});

  useEffect(() => {
    let active = true;
    setLoading(true);
    getTaskRevisions(workspaceId, taskId)
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
  }, [workspaceId, taskId]);

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
      setRevisions(await getTaskRevisions(workspaceId, taskId));
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
      setRevisions(await getTaskRevisions(workspaceId, taskId));
    } catch (err) {
      // 404: the lead agent does not exist, so Apply cannot open its area.
      if (err instanceof ApiError && err.status === 404)
        setFollow((prev) => ({ ...prev, [key]: "" }));
      setError(err instanceof Error ? err.message : "Could not apply revision");
    } finally {
      setBusy("");
    }
  }

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
      {revisions.map((revision) => {
        const key = `${revision.change_id}:${revision.number}`;
        const disabled = Boolean(busy) || revision.incomplete;
        // The list reports the verdict for this exact revision head, so a new
        // derived revision has none and offers the buttons again.
        const decided = Boolean(revision.verdict);
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
            {follow[key] === "approved_waiting_for_working_area" && (
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
