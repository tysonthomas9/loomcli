/**
 * WorkQueueSection displays issue count summaries grouped by status category.
 * Rendered inside the WorkspaceTree sidebar. Counts are workspace-scoped
 * because the issues array is fetched with the active Workspace header.
 */

import { useState, useCallback, useEffect } from "react";

import { useWorkspaceContext } from "@/hooks/workspace";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import styles from "./WorkQueueSection.module.css";

/** Counts for each Work Queue category. */
export interface WorkQueueCounts {
  backlog: number;
  open: number;
  blocked: number;
  inProgress: number;
  needsReview: number;
  done: number;
}

export interface WorkQueueSectionProps {
  counts: WorkQueueCounts;
}

const SK_WORK_QUEUE_EXPANDED = "work-queue-expanded";

export function WorkQueueSection({
  counts,
}: WorkQueueSectionProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();

  const [isExpanded, setIsExpanded] = useState(() => {
    if (!workspaceId) return true;
    const stored = wsGet(workspaceId, SK_WORK_QUEUE_EXPANDED);
    return stored !== null ? stored === "true" : true;
  });

  // Re-read scoped state when workspace changes (SPA navigation)
  useEffect(() => {
    if (!workspaceId) return;
    const stored = wsGet(workspaceId, SK_WORK_QUEUE_EXPANDED);
    setIsExpanded(stored !== null ? stored === "true" : true);
  }, [workspaceId]);

  useEffect(() => {
    if (workspaceId)
      wsSet(workspaceId, SK_WORK_QUEUE_EXPANDED, String(isExpanded));
  }, [isExpanded, workspaceId]);

  const handleToggle = useCallback(() => {
    setIsExpanded((prev) => !prev);
  }, []);

  return (
    <div className={styles.workQueue}>
      <div
        className={styles.workQueueHeader}
        onClick={handleToggle}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => e.key === "Enter" && handleToggle()}
      >
        <span className={styles.workQueueToggle}>
          {isExpanded ? "\u25BE" : "\u25B8"}
        </span>
        <span>Work Queue</span>
      </div>
      {isExpanded && (
        <div className={styles.workQueueContent}>
          <div className={styles.queueGrid}>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>Backlog</span>
              <span
                className={styles.queueCount}
                data-highlight={counts.backlog > 0}
              >
                {counts.backlog}
              </span>
            </div>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>Open</span>
              <span
                className={styles.queueCount}
                data-highlight={counts.open > 0}
              >
                {counts.open}
              </span>
            </div>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>Blocked</span>
              <span className={styles.queueCount}>{counts.blocked}</span>
            </div>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>In Progress</span>
              <span
                className={styles.queueCount}
                data-highlight={counts.inProgress > 0}
              >
                {counts.inProgress}
              </span>
            </div>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>Needs Review</span>
              <span
                className={styles.queueCount}
                data-highlight={counts.needsReview > 0}
              >
                {counts.needsReview}
              </span>
            </div>
            <div className={styles.queueItem}>
              <span className={styles.queueLabel}>Done</span>
              <span className={styles.queueCount}>{counts.done}</span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
