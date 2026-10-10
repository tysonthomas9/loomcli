/**
 * TaskChangesTab — review a task, not a revision (D29, P2.23). One review bar
 * at the top decides the whole task; below it, the task's diff per repo: each
 * repo's newest revision against the layer below it in the lead's stack (what
 * that repo's PR contains), one section per repo ordered by repo name. Earlier
 * attempts are kept on the server (and the CLI), not listed here; a rerun
 * after a rejection shows the rejection reason above its diff.
 */

import { useEffect, useState } from "react";

import { DiffFileViewer } from "@/components/AgentDetailPanel";
import {
  getTaskDiff,
  getTaskRevisions,
  type ReviewRevision,
  type RevisionDiff,
  type TaskDiff,
} from "@/hooks/api";

import styles from "./PRFilesTab.module.css";
import own from "./TaskChangesTab.module.css";
import { newestRevisions, ReviewBar } from "./sections/ReviewBar";

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

/**
 * Why the attempt on screen exists: the newest earlier rejection of this
 * change, while the current attempt still awaits review (S7).
 */
export function rejectionReason(
  revisions: ReviewRevision[],
  newest: ReviewRevision,
): string {
  if (newest.verdict) return "";
  const rejected = revisions
    .filter(
      (r) =>
        r.change_id === newest.change_id &&
        r.number < newest.number &&
        r.verdict === "reject" &&
        r.verdict_reason,
    )
    .sort((a, b) => b.number - a.number)[0];
  return rejected?.verdict_reason ?? "";
}

export function TaskChangesTab({
  workspaceId,
  taskId,
  lead,
  taskStatus,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
  taskStatus?: string | undefined;
}): JSX.Element {
  const [revisions, setRevisions] = useState<ReviewRevision[] | null>(null);
  const [diffs, setDiffs] = useState<TaskDiff[] | null>(null);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);

  // A different task starts clean; a reload after a verdict keeps the view.
  useEffect(() => {
    setRevisions(null);
    setDiffs(null);
  }, [workspaceId, taskId, lead]);

  useEffect(() => {
    let active = true;
    setError("");
    getTaskRevisions(workspaceId, taskId, lead)
      .then(async (items) => {
        if (!active) return;
        setRevisions(items);
        if (items.length === 0) return;
        const result = await getTaskDiff(workspaceId, taskId, lead);
        if (active) setDiffs(result);
      })
      .catch((err: unknown) => {
        if (active) setError(errorText(err, "Could not load changes"));
      });
    return () => {
      active = false;
    };
  }, [workspaceId, taskId, lead, version]);

  if (error) {
    return (
      <div className={styles.message} role="alert">
        Could not load changes: {error}
      </div>
    );
  }
  if (revisions === null) {
    return <div className={styles.message}>Loading changes…</div>;
  }
  if (revisions.length === 0) {
    return <div className={styles.message}>No code changes yet.</div>;
  }

  // One change per repo: one section each, ordered by repo name.
  const changes = newestRevisions(revisions).sort(
    (a, b) =>
      a.repo.localeCompare(b.repo) || a.change_id.localeCompare(b.change_id),
  );
  const multiRepo = changes.length > 1;

  return (
    <div className={styles.wrap} data-testid="task-changes-tab">
      <ReviewBar
        workspaceId={workspaceId}
        taskId={taskId}
        lead={lead}
        taskStatus={taskStatus}
        current={changes}
        diffs={diffs}
        onChanged={() => setVersion((v) => v + 1)}
      />
      {changes.map((newest) => {
        const diff = diffs?.find((d) => d.change === newest.change_id);
        const rejected = rejectionReason(revisions, newest);
        return (
          <section
            key={newest.change_id}
            aria-label={multiRepo ? `Repo ${newest.repo}` : undefined}
            data-testid="task-repo-changes"
          >
            {multiRepo && <h3 className={own.repo}>{newest.repo}</h3>}
            {rejected && (
              <div className={own.rejected} data-testid="rejection-reason">
                Retried after you rejected: {rejected}
              </div>
            )}
            {diff ? (
              <DiffFiles files={diff.files} />
            ) : (
              <div className={styles.message}>
                {diffs !== null
                  ? "No diff for this repo yet."
                  : "Loading diff…"}
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

type DiffFile = RevisionDiff["files"][number];

function lineStats(patch: string): { additions: number; deletions: number } {
  let additions = 0;
  let deletions = 0;
  for (const line of patch.split("\n")) {
    if (line.startsWith("+") && !line.startsWith("+++")) additions++;
    else if (line.startsWith("-") && !line.startsWith("---")) deletions++;
  }
  return { additions, deletions };
}

function DiffFiles({ files }: { files: DiffFile[] }): JSX.Element {
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  if (files.length === 0) {
    return <div className={styles.message}>No changes</div>;
  }
  const selected = files.find((f) => f.path === selectedPath) ?? files[0];
  return (
    <div className={styles.panes}>
      <aside className={styles.fileList} aria-label="Changed files">
        {files.map((file) => (
          <button
            key={file.path}
            type="button"
            className={styles.fileRow}
            data-active={file.path === selected?.path || undefined}
            onClick={() => setSelectedPath(file.path)}
            title={file.path}
          >
            <span className={styles.fileName}>{file.path}</span>
          </button>
        ))}
      </aside>
      <section className={styles.diffView}>
        {selected && (
          <>
            <h2 className={styles.monoTitle}>{selected.path}</h2>
            {selected.truncated ? (
              <div className={styles.message}>
                {selected.path} · {selected.patchSize} bytes · too large to show
              </div>
            ) : (
              <DiffFileViewer
                patch={{
                  // A trailing newline would render as an empty context line.
                  patch: (selected.patch ?? "").replace(/\n$/, ""),
                  is_binary: false,
                  is_too_large: false,
                  ...lineStats(selected.patch ?? ""),
                }}
                isLoading={false}
              />
            )}
          </>
        )}
      </section>
    </div>
  );
}
