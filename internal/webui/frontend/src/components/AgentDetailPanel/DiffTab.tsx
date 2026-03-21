/**
 * DiffTab - orchestrates the diff viewer for an agent's worktree changes.
 * Uses useDiff hook for data, renders summary bar, file list with DiffFileRow,
 * and inline DiffFileViewer for expanded files.
 */

import { useState, useEffect } from "react";

import type { LoomAgentStatus } from "@/types";
import { useDiff } from "@/hooks/useDiff";

import { DiffFileRow } from "./DiffFileRow";
import { DiffFileViewer } from "./DiffFileViewer";
import styles from "./DiffTab.module.css";

interface DiffTabProps {
  agent: LoomAgentStatus;
  isActive?: boolean;
}

export function DiffTab({ agent, isActive }: DiffTabProps): JSX.Element {
  const {
    files,
    isLoading,
    error,
    viewedFiles,
    markViewed,
    patchCache,
    fetchPatch,
    summaryStats,
  } = useDiff({ agentName: agent.name, enabled: isActive ?? true });

  const [expandedFiles, setExpandedFiles] = useState<Set<string>>(new Set());

  // Reset expanded files when agent changes
  useEffect(() => {
    setExpandedFiles(new Set());
  }, [agent.name]);

  function handleToggleExpand(path: string) {
    setExpandedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(path)) {
        next.delete(path);
      } else {
        next.add(path);
        // Trigger patch fetch when expanding
        fetchPatch(path);
      }
      return next;
    });
  }

  if (isLoading) {
    return <div className={styles.loading}>Loading diff…</div>;
  }

  // Show top-level error only when file list failed to load.
  // When files loaded but error is set, it's a patch-fetch error — show inline.
  if (error && files.length === 0) {
    return <div className={styles.error}>{error.message}</div>;
  }

  if (!error && files.length === 0) {
    return <div className={styles.emptyState}>No changes</div>;
  }

  return (
    <>
      {/* Summary Bar */}
      <div className={styles.summaryBar}>
        <span className={styles.summaryCount}>
          {summaryStats.filesChanged} file
          {summaryStats.filesChanged !== 1 ? "s" : ""} changed
        </span>
        {summaryStats.additions > 0 && (
          <span className={styles.statAdd}>+{summaryStats.additions}</span>
        )}
        {summaryStats.deletions > 0 && (
          <span className={styles.statDel}>-{summaryStats.deletions}</span>
        )}
      </div>

      {/* File List */}
      <div className={styles.fileList}>
        {files.map((file) => {
          const isExpanded = expandedFiles.has(file.path);
          const cachedPatch = patchCache.get(file.path) ?? null;

          return (
            <div key={file.path}>
              <DiffFileRow
                file={file}
                isExpanded={isExpanded}
                isViewed={viewedFiles.has(file.path)}
                onToggleExpand={() => handleToggleExpand(file.path)}
                onToggleViewed={() => markViewed(file.path)}
              />
              {isExpanded && (
                <DiffFileViewer
                  patch={cachedPatch}
                  isLoading={!cachedPatch && !error}
                  error={!cachedPatch && error ? error.message : undefined}
                />
              )}
            </div>
          );
        })}
      </div>
    </>
  );
}
