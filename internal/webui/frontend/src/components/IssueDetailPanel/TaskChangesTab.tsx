/**
 * TaskChangesTab — review a task, not a revision (D29). Loads the task's
 * revisions first, then its diff per repo: each repo's newest revision against
 * the layer below it in the lead's stack (what that repo's PR contains). A
 * cross-repo task gets one section per repo, ordered by repo name, each with
 * the verdict buttons for that repo's newest revision only; earlier revisions
 * are read-only under that repo's History.
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
    return <div className={styles.message}>Loading revisions…</div>;
  }
  if (revisions.length === 0) {
    return <div className={styles.message}>No revisions yet.</div>;
  }

  // One change per repo: one section each, ordered by repo name.
  const changes = newestRevisions(revisions).sort(
    (a, b) =>
      a.repo.localeCompare(b.repo) || a.change_id.localeCompare(b.change_id),
  );
  const multiRepo = changes.length > 1;

  return (
    <div className={styles.wrap} data-testid="task-changes-tab">
      {changes.map((newest) => (
        <RepoChanges
          key={newest.change_id}
          workspaceId={workspaceId}
          taskId={taskId}
          lead={lead}
          newest={newest}
          history={revisions.filter(
            (r) => r.change_id === newest.change_id && r !== newest,
          )}
          diff={diffs?.find((d) => d.change === newest.change_id)}
          diffsLoaded={diffs !== null}
          multiRepo={multiRepo}
          onChanged={() => setVersion((v) => v + 1)}
        />
      ))}
    </div>
  );
}

/** One repo's part of the task: its verdicts, its diff and its History. */
function RepoChanges({
  workspaceId,
  taskId,
  lead,
  newest,
  history,
  diff,
  diffsLoaded,
  multiRepo,
  onChanged,
}: {
  workspaceId: string;
  taskId: string;
  lead?: string | undefined;
  newest: ReviewRevision;
  history: ReviewRevision[];
  diff: TaskDiff | undefined;
  diffsLoaded: boolean;
  multiRepo: boolean;
  onChanged: () => void;
}): JSX.Element {
  const [showHistory, setShowHistory] = useState(false);
  const [viewing, setViewing] = useState<ReviewRevision | null>(null);
  // Verdicts decide on the code shown: only once this repo's diff has loaded,
  // and only for the very revision the diff shows.
  const shown = diff !== undefined && diff.revision === newest.number;

  return (
    <section
      aria-label={multiRepo ? `Repo ${newest.repo}` : undefined}
      data-testid="task-repo-changes"
    >
      {multiRepo && <h3 className={own.repo}>{newest.repo}</h3>}
      <div className={own.review}>
        <RevisionsSection
          workspaceId={workspaceId}
          taskId={taskId}
          lead={lead}
          changeId={newest.change_id}
          locked={!shown}
          onChanged={onChanged}
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
            <ul
              aria-label={
                multiRepo
                  ? `${newest.repo} revision history`
                  : "Revision history"
              }
              className={own.history}
            >
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
            <div className={styles.message}>
              {diffsLoaded ? "No diff for this repo yet." : "Loading diff…"}
            </div>
          )}
        </>
      )}
    </section>
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
