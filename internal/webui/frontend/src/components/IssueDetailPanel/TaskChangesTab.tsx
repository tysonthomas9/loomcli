/**
 * TaskChangesTab — review a task, not a revision (D29). Loads the task's
 * revisions first, then shows one diff: the newest revision against the layer
 * below it in the lead's stack (what its PR contains). Verdicts act on the
 * newest revision; earlier revisions are read-only under History.
 */

import { useEffect, useState } from "react";

import { DiffFileViewer } from "@/components/AgentDetailPanel";
import {
  getRevisionDiff,
  getTaskDiff,
  getTaskRevisions,
  type ReviewRevision,
  type RevisionDiff,
  type TaskDiff,
} from "@/hooks/api";

import styles from "./PRFilesTab.module.css";
import own from "./TaskChangesTab.module.css";
import { newestRevisions, RevisionsSection } from "./sections/RevisionsSection";

const COMPARE_LABEL: Record<TaskDiff["compare"], string> = {
  layer: "against the layer below it in the stack",
  trunk: "against trunk",
  base: "against its base (not applied yet)",
};

function errorText(err: unknown, fallback: string): string {
  return err instanceof Error && err.message ? err.message : fallback;
}

export function TaskChangesTab({
  workspaceId,
  taskId,
  lead,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
}): JSX.Element {
  const [revisions, setRevisions] = useState<ReviewRevision[] | null>(null);
  const [diff, setDiff] = useState<TaskDiff | null>(null);
  const [error, setError] = useState("");
  const [showHistory, setShowHistory] = useState(false);
  const [viewing, setViewing] = useState<ReviewRevision | null>(null);

  useEffect(() => {
    let active = true;
    setRevisions(null);
    setDiff(null);
    setError("");
    setShowHistory(false);
    setViewing(null);
    getTaskRevisions(workspaceId, taskId, lead)
      .then(async (items) => {
        if (!active) return;
        setRevisions(items);
        if (items.length === 0) return;
        const result = await getTaskDiff(workspaceId, taskId, lead);
        if (active) setDiff(result);
      })
      .catch((err: unknown) => {
        if (active) setError(errorText(err, "Could not load changes"));
      });
    return () => {
      active = false;
    };
  }, [workspaceId, taskId, lead]);

  if (error) {
    return (
      <div className={styles.message} role="alert">
        Could not load changes: {error}
      </div>
    );
  }
  if (revisions === null) {
    return <div className={styles.message}>Loading revisions…</div>;
  }
  if (revisions.length === 0) {
    return <div className={styles.message}>No revisions yet.</div>;
  }

  const current = new Set(newestRevisions(revisions));
  const history = revisions.filter((r) => !current.has(r));

  return (
    <div className={styles.wrap} data-testid="task-changes-tab">
      <div className={own.review}>
        <RevisionsSection
          workspaceId={workspaceId}
          taskId={taskId}
          lead={lead}
        />
      </div>
      {viewing ? (
        <>
          <div className={styles.actionBar}>
            <span className={styles.filesLabel}>
              Revision {viewing.number} (read-only history), against its base
            </span>
            <button type="button" onClick={() => setViewing(null)}>
              Back to the task diff
            </button>
          </div>
          <RevisionDiffView workspaceId={workspaceId} revision={viewing} />
        </>
      ) : (
        <>
          <div className={styles.actionBar}>
            <span className={styles.filesLabel} data-testid="task-diff-label">
              {diff
                ? `Revision ${diff.revision} ${COMPARE_LABEL[diff.compare]}`
                : "Task diff"}
            </span>
            {history.length > 0 && (
              <button
                type="button"
                aria-expanded={showHistory}
                onClick={() => setShowHistory((open) => !open)}
              >
                History ({history.length})
              </button>
            )}
          </div>
          {showHistory && (
            <ul aria-label="Revision history" className={own.history}>
              {history.map((r) => (
                <li key={`${r.change_id}:${r.number}`}>
                  <button type="button" onClick={() => setViewing(r)}>
                    Revision {r.number}
                  </button>{" "}
                  <code>{r.head_sha.slice(0, 12)}</code> · {r.outcome}
                  {r.superseded && " · replaced by a newer revision"}
                  {r.verdict && ` · ${r.verdict}`}
                  {r.date && ` · ${new Date(r.date).toLocaleString()}`}
                </li>
              ))}
            </ul>
          )}
          {diff ? (
            <DiffFiles files={diff.files} />
          ) : (
            <div className={styles.message}>Loading diff…</div>
          )}
        </>
      )}
    </div>
  );
}

function RevisionDiffView({
  workspaceId,
  revision,
}: {
  workspaceId: string;
  revision: ReviewRevision;
}): JSX.Element {
  const [diff, setDiff] = useState<RevisionDiff | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    let active = true;
    setDiff(null);
    setError("");
    getRevisionDiff(workspaceId, revision)
      .then((result) => {
        if (active) setDiff(result);
      })
      .catch((err: unknown) => {
        if (active) setError(errorText(err, "Could not load diff"));
      });
    return () => {
      active = false;
    };
  }, [workspaceId, revision]);
  if (error)
    return (
      <div className={styles.message} role="alert">
        Could not load diff: {error}
      </div>
    );
  if (!diff) return <div className={styles.message}>Loading diff…</div>;
  return <DiffFiles files={diff.files} />;
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
                  patch: selected.patch ?? "",
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
