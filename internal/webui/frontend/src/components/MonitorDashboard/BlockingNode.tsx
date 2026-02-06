/**
 * BlockingNode component for BlockingDependenciesCanvas.
 * Rich node card showing issue details, status badges, and blocking relationships.
 */

import { Handle, Position, type NodeProps } from '@xyflow/react';
import { memo } from 'react';

import type { IssueNode as IssueNodeType } from '@/types';
import { formatIssueId } from '@/utils/formatIssueId';

import styles from './BlockingNode.module.css';

/**
 * Derive the node status for styling purposes.
 * - 'blocked': issue is in the blocked set (waiting on others)
 * - 'blocking': issue blocks others (blockedCount > 0 or isRootBlocker)
 * - 'healthy': neither blocking nor blocked
 */
function deriveNodeStatus(
  isBlocked: boolean,
  blockedCount: number,
  isRootBlocker: boolean
): 'healthy' | 'blocking' | 'blocked' {
  if (isBlocked) return 'blocked';
  if (blockedCount > 0 || isRootBlocker) return 'blocking';
  return 'healthy';
}

/**
 * BlockingNode renders a rich card for a node in the BlockingDependenciesCanvas.
 */
function BlockingNodeComponent({ data, selected }: NodeProps<IssueNodeType>): JSX.Element {
  const { title, issue, blockedCount, isRootBlocker, isReady } = data;

  const displayId = formatIssueId(issue.id);
  const displayTitle = title || 'Untitled';
  const description = issue.description || issue.notes || '';
  const isBlocked = !isReady && !data.isClosed;
  const nodeStatus = deriveNodeStatus(isBlocked, blockedCount, isRootBlocker);

  // Extract blocked-by IDs from issue dependencies
  const blockedByIds: string[] = issue.dependencies
    ? issue.dependencies.map((dep) => dep.depends_on_id).filter(Boolean)
    : [];

  const rootClassName = selected
    ? `${styles.blockingNode} ${styles.selected}`
    : styles.blockingNode;

  return (
    <article
      className={rootClassName}
      data-node-status={nodeStatus}
      aria-label={`Issue: ${displayTitle}`}
    >
      <Handle type="target" position={Position.Left} className={styles.handle} id="target" />

      <header className={styles.header}>
        <span className={styles.issueId}>{displayId}</span>
        <span className={styles.statusBadge} data-status={nodeStatus}>
          {nodeStatus === 'blocked'
            ? 'Waiting'
            : nodeStatus === 'blocking'
              ? 'Blocking'
              : 'Healthy'}
        </span>
      </header>

      <h3 className={styles.title}>{displayTitle}</h3>

      {description && <p className={styles.description}>{description}</p>}

      <footer className={styles.footer}>
        {isBlocked && blockedByIds.length > 0 && (
          <span className={styles.blockedBy}>
            Blocked by {blockedByIds.map((id) => formatIssueId(id)).join(', ')}
          </span>
        )}
        {blockedCount > 0 && <span className={styles.blockingCount}>Blocking {blockedCount}</span>}
      </footer>

      <Handle type="source" position={Position.Right} className={styles.handle} id="source" />
    </article>
  );
}

/**
 * Custom equality comparison for memo.
 */
function arePropsEqual(prev: NodeProps<IssueNodeType>, next: NodeProps<IssueNodeType>): boolean {
  return (
    prev.selected === next.selected &&
    prev.data.issue.id === next.data.issue.id &&
    prev.data.title === next.data.title &&
    prev.data.status === next.data.status &&
    prev.data.isReady === next.data.isReady &&
    prev.data.blockedCount === next.data.blockedCount &&
    prev.data.isRootBlocker === next.data.isRootBlocker &&
    prev.data.isClosed === next.data.isClosed &&
    prev.data.issue.description === next.data.issue.description &&
    prev.data.issue.notes === next.data.issue.notes &&
    prev.data.issue.dependencies === next.data.issue.dependencies
  );
}

export const BlockingNode = memo(BlockingNodeComponent, arePropsEqual);
