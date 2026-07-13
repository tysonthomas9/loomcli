/**
 * PRFilesTab — the design's PR "Files changed" pane (DiffPane): a two-pane
 * master-detail layout with a 240px file rail (A/M/D badge · name · +/−
 * stats) and a diff viewer for the selected file on the right.
 *
 * Data is loom's real per-agent branch diff (useDiff → /agents/{name}/diff/*)
 * — the PR author's worktree vs its merge-base; rendering reuses
 * GitDiffViewer's unified diff rendering.
 */

import { useEffect, useState, type ReactNode } from "react";

import { GitDiffViewer } from "@/components/DiffView";
import { useDiff } from "@/hooks/terminal";
import type { LoomAgentStatus } from "@/types";

import styles from "./PRFilesTab.module.css";

export interface PRFilesTabProps {
  /** The PR author's live agent (diff source). */
  agent: LoomAgentStatus;
  isActive?: boolean;
  /** Rendered when the worktree diff is empty (e.g. task session diff). */
  emptyFallback?: ReactNode;
}

export function PRFilesTab({
  agent,
  isActive,
  emptyFallback,
}: PRFilesTabProps): JSX.Element {
  const {
    files,
    isLoading,
    error,
    patchErrors,
    patchCache,
    fetchPatch,
    summaryStats,
  } = useDiff({
    agentName: agent.name,
    enabled: isActive ?? true,
    commitSignal: agent.ahead,
  });

  const [selectedPath, setSelectedPath] = useState<string | null>(null);

  // Default-select the first file (design's DiffPane starts on files[0]) and
  // keep the selection valid across refreshes.
  useEffect(() => {
    if (files.length === 0) {
      setSelectedPath(null);
      return;
    }
    setSelectedPath((prev) =>
      prev && files.some((f) => f.path === prev)
        ? prev
        : (files[0]?.path ?? null),
    );
  }, [files]);

  // Fetch the selected file's patch on demand.
  useEffect(() => {
    if (selectedPath) void fetchPatch(selectedPath);
  }, [selectedPath, fetchPatch]);

  if (isLoading) {
    return <div className={styles.message}>Loading diff…</div>;
  }
  if (error && files.length === 0) {
    return <div className={styles.message}>{error.message}</div>;
  }
  if (files.length === 0) {
    if (emptyFallback) {
      return <>{emptyFallback}</>;
    }
    return (
      <div className={styles.message}>
        No changes on {agent.name}&apos;s branch yet.
      </div>
    );
  }

  const selected = files.find((f) => f.path === selectedPath) ?? files[0];
  const patch = selected ? (patchCache.get(selected.path) ?? null) : null;
  const patchError = selected
    ? patchErrors.get(selected.path)?.message
    : undefined;

  return (
    <div className={styles.wrap} data-testid="pr-files-tab">
      {/* Action bar: "Files changed N · +a −d" (design pr-actionbar). */}
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
              <span className={styles.badge} data-status={file.status}>
                {file.status}
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
          ))}
        </aside>

        <section className={styles.diffView}>
          {selected && (
            <>
              <h2 className={styles.monoTitle}>
                <span className={styles.badge} data-status={selected.status}>
                  {selected.status}
                </span>
                {selected.path}
              </h2>
              <GitDiffViewer
                filePath={selected.path}
                patch={patch}
                isLoading={!patch && !patchError}
                {...(patchError !== undefined && { error: patchError })}
              />
            </>
          )}
        </section>
      </div>
    </div>
  );
}
