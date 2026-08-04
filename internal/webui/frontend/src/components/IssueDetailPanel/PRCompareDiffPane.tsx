import { useEffect, useMemo, useState } from "react";

import type { DiffFilePatch } from "@/api/issues";
import type { PullRequestDiff } from "@/api/workspace";
import { DiffFileViewer } from "@/components/AgentDetailPanel";
import { usePullRequestDiff } from "@/hooks/workspace";

import styles from "./PRCompareDiffPane.module.css";

export interface PRCompareDiffPaneProps {
  workspaceId: string;
  owner: string;
  repo: string;
  number: number;
  refreshKey?: number;
}

type PullRequestDiffFile = PullRequestDiff["files"][number];
const EMPTY_FILES: PullRequestDiff["files"] = [];

function toBadgeStatus(status: string): string {
  switch (status.toLowerCase()) {
    case "added":
    case "a":
      return "A";
    case "removed":
    case "deleted":
    case "d":
      return "D";
    case "renamed":
    case "r":
      return "R";
    case "copied":
    case "c":
      return "C";
    case "modified":
    case "changed":
    case "m":
      return "M";
    default:
      return status.slice(0, 1).toUpperCase() || "?";
  }
}

function toViewerPatch(file: PullRequestDiffFile): DiffFilePatch {
  return {
    patch: file.patch,
    is_binary: false,
    is_too_large: false,
    additions: file.additions,
    deletions: file.deletions,
  };
}

export function PRCompareDiffPane({
  workspaceId,
  owner,
  repo,
  number: pullNumber,
  refreshKey,
}: PRCompareDiffPaneProps): JSX.Element {
  const [selectedPath, setSelectedPath] = useState<string | null>(null);
  const { diff, isLoading, error } = usePullRequestDiff({
    workspaceId,
    owner,
    repo,
    number: pullNumber,
    ...(refreshKey !== undefined ? { refreshKey } : {}),
  });
  const files = diff?.files ?? EMPTY_FILES;

  useEffect(() => {
    if (files.length === 0) {
      setSelectedPath(null);
      return;
    }
    setSelectedPath((previous) =>
      previous && files.some((file) => file.path === previous)
        ? previous
        : (files[0]?.path ?? null),
    );
  }, [files]);

  const summaryStats = useMemo(
    () =>
      files.reduce(
        (stats, file) => ({
          filesChanged: stats.filesChanged + 1,
          additions: stats.additions + file.additions,
          deletions: stats.deletions + file.deletions,
        }),
        { filesChanged: 0, additions: 0, deletions: 0 },
      ),
    [files],
  );
  const selected = files.find((file) => file.path === selectedPath) ?? files[0];
  const selectedPatch = selected ? toViewerPatch(selected) : null;

  if (isLoading) {
    return (
      <div className={styles.wrap} data-testid="pr-compare-diff-pane">
        <div className={styles.message}>Loading diff…</div>
      </div>
    );
  }

  if (error) {
    return (
      <div className={styles.wrap} data-testid="pr-compare-diff-pane">
        <div className={styles.message}>{error}</div>
      </div>
    );
  }

  return (
    <div className={styles.wrap} data-testid="pr-compare-diff-pane">
      <div className={styles.actionBar}>
        <span className={styles.filesLabel}>
          Files changed{" "}
          <span className={styles.filesCount}>{summaryStats.filesChanged}</span>
        </span>
        <span className={styles.stats}>
          {summaryStats.additions > 0 && (
            <span className={styles.add}>+{summaryStats.additions}</span>
          )}
          {summaryStats.deletions > 0 && (
            <span className={styles.del}>−{summaryStats.deletions}</span>
          )}
        </span>
      </div>

      {files.length === 0 ? (
        <div className={styles.message}>
          No files changed in this pull request.
        </div>
      ) : (
        <div className={styles.panes}>
          <aside className={styles.fileList} aria-label="Changed files">
            {files.map((file) => {
              const badgeStatus = toBadgeStatus(file.status);
              return (
                <button
                  key={file.path}
                  type="button"
                  className={styles.fileRow}
                  data-active={file.path === selected?.path || undefined}
                  data-testid="pr-compare-file-row"
                  onClick={() => setSelectedPath(file.path)}
                  title={`${file.path} (${file.status})`}
                >
                  <span className={styles.badge} data-status={badgeStatus}>
                    {badgeStatus}
                  </span>
                  <span className={styles.fileName}>{file.path}</span>
                  <span className={styles.fileStat}>
                    {file.additions > 0 && (
                      <span className={styles.add}>+{file.additions}</span>
                    )}{" "}
                    {file.deletions > 0 && (
                      <span className={styles.del}>−{file.deletions}</span>
                    )}
                  </span>
                </button>
              );
            })}
          </aside>

          <section className={styles.diffView}>
            {selected && (
              <>
                <h2 className={styles.monoTitle}>
                  <span
                    className={styles.badge}
                    data-status={toBadgeStatus(selected.status)}
                  >
                    {toBadgeStatus(selected.status)}
                  </span>
                  {selected.path}
                </h2>
                <DiffFileViewer patch={selectedPatch} isLoading={false} />
              </>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
