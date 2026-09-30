import { useEffect, useState } from "react";
import {
  applyRevision,
  getTaskRevisions,
  submitRevisionVerdict,
  type ReviewRevision,
} from "@/hooks/api";
import styles from "./RevisionsSection.module.css";

export function RevisionsSection({
  workspaceId,
  taskId,
}: {
  workspaceId: string;
  taskId: string;
}): JSX.Element {
  const [revisions, setRevisions] = useState<ReviewRevision[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [override, setOverride] = useState("");
  const [reason, setReason] = useState("");
  const [applied, setApplied] = useState("");

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
    setBusy(`${revision.change_id}:${revision.number}`);
    setError("");
    try {
      await submitRevisionVerdict(workspaceId, revision, verdict, detail);
      setRevisions(await getTaskRevisions(workspaceId, taskId));
      setOverride("");
      setReason("");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not record verdict");
    } finally {
      setBusy("");
    }
  }

  async function applySelected(revision: ReviewRevision) {
    setBusy(`${revision.change_id}:${revision.number}`);
    setError("");
    setApplied("");
    try {
      await applyRevision(workspaceId, revision);
      setApplied(
        `Applied revision ${revision.number} to the local working area`,
      );
    } catch (err) {
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
      {applied && <p role="status">{applied}</p>}
      {!loading && revisions.length === 0 && <p>No revisions yet.</p>}
      {revisions.map((revision) => {
        const key = `${revision.change_id}:${revision.number}`;
        const disabled = Boolean(busy) || revision.incomplete;
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
            <div className={styles.actions}>
              <button
                type="button"
                disabled={disabled}
                onClick={() => void applySelected(revision)}
              >
                Apply
              </button>
              <button
                type="button"
                disabled={disabled}
                onClick={() => void decide(revision, "approve")}
              >
                Approve
              </button>
              <button
                type="button"
                disabled={disabled}
                onClick={() => void decide(revision, "reject")}
              >
                Reject
              </button>
              <button
                type="button"
                disabled={disabled}
                onClick={() => {
                  setOverride(key);
                  setReason("");
                }}
              >
                Override
              </button>
            </div>
            {override === key && (
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
