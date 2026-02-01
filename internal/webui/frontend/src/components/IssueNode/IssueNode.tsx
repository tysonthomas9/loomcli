/**
 * IssueNode component for React Flow dependency graph.
 * Displays an issue as a node with connection handles.
 */

import { memo } from 'react';
import { Handle, Position, type NodeProps } from '@xyflow/react';
import type { IssueNode as IssueNodeType } from '@/types';
import { BlockedBadge } from '@/components/BlockedBadge';
import styles from './IssueNode.module.css';

export interface IssueNodeProps extends NodeProps<IssueNodeType> {
  // NodeProps provides: id, data, selected, dragging, etc.
  // Extended with chain highlighting
  /** Whether this node is part of a highlighted blocking chain */
  'data-in-chain'?: boolean;
}

/**
 * Format issue ID for display (last 7 chars if long).
 */
function formatIssueId(id: string): string {
  if (!id) return 'unknown';
  if (id.length <= 10) return id;
  return id.slice(-7);
}

/**
 * Get priority level, defaulting to 4 if undefined or out of range.
 */
function getPriorityLevel(priority: number | undefined): 0 | 1 | 2 | 3 | 4 {
  if (priority === undefined || priority === null) return 4;
  if (priority < 0 || priority > 4) return 4;
  return priority as 0 | 1 | 2 | 3 | 4;
}

/**
 * IssueNode renders an issue as a React Flow node in the dependency graph.
 */
function IssueNodeComponent({ data, selected }: IssueNodeProps): JSX.Element {
  const {
    title,
    priority,
    status,
    issueType,
    dependencyCount,
    dependentCount,
    issue,
    isReady,
    blockedCount,
    isRootBlocker,
  } = data;
  const displayId = formatIssueId(issue.id);
  const displayTitle = title || 'Untitled';
  const priorityLevel = getPriorityLevel(priority);

  const rootClassName = selected
    ? `${styles.issueNode} ${styles.selected}`
    : styles.issueNode;

  return (
    <article
      className={rootClassName}
      data-priority={priorityLevel}
      data-status={status || 'unknown'}
      data-is-ready={isReady}
      data-is-root-blocker={isRootBlocker}
      aria-label={`Issue: ${displayTitle}`}
    >
      {/* Target handle for incoming dependencies */}
      <Handle
        type="target"
        position={Position.Left}
        className={styles.handle}
        id="target"
      />

      {/* Blocked count badge - positioned at top-right corner */}
      {blockedCount > 0 && (
        <div className={styles.badgeContainer}>
          <BlockedBadge count={blockedCount} variant="blocks" />
        </div>
      )}

      <header className={styles.header}>
        <span className={styles.id}>{displayId}</span>
        {issueType && (
          <span className={styles.typeBadge} data-type={issueType}>
            {issueType}
          </span>
        )}
        <span
          className={`${styles.priorityBadge} ${styles[`priority${priorityLevel}`]}`}
          data-priority={priorityLevel}
          aria-label={`Priority ${priorityLevel}`}
        >
          P{priorityLevel}
        </span>
      </header>

      <h3 className={styles.title}>{displayTitle}</h3>

      <footer className={styles.footer}>
        <span className={styles.depCount} title="Dependencies">
          {dependencyCount > 0 && `← ${dependencyCount}`}
        </span>
        <span className={styles.depCount} title="Dependents">
          {dependentCount > 0 && `${dependentCount} →`}
        </span>
      </footer>

      {/* Source handle for outgoing dependencies */}
      <Handle
        type="source"
        position={Position.Right}
        className={styles.handle}
        id="source"
      />
    </article>
  );
}

/**
 * Custom equality comparison for memo to prevent unnecessary re-renders.
 * React Flow passes new object references on every pan/zoom, so we compare
 * only the values that affect rendering.
 */
function arePropsEqual(prev: IssueNodeProps, next: IssueNodeProps): boolean {
  return (
    prev.selected === next.selected &&
    prev.data.issue.id === next.data.issue.id &&
    prev.data.title === next.data.title &&
    prev.data.status === next.data.status &&
    prev.data.priority === next.data.priority &&
    prev.data.issueType === next.data.issueType &&
    prev.data.dependencyCount === next.data.dependencyCount &&
    prev.data.dependentCount === next.data.dependentCount &&
    prev.data.isReady === next.data.isReady &&
    prev.data.blockedCount === next.data.blockedCount &&
    prev.data.isRootBlocker === next.data.isRootBlocker
  );
}

// Memoize with custom equality to prevent unnecessary re-renders during pan/zoom
export const IssueNode = memo(IssueNodeComponent, arePropsEqual);
