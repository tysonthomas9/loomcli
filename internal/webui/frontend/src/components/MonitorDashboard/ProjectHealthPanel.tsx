/**
 * ProjectHealthPanel component.
 * Displays project health metrics including completion progress,
 * issue counts, and bottleneck detection.
 */

import { useMemo } from 'react';
import type { LoomStats, BlockedIssue, Issue } from '@/types';
import styles from './ProjectHealthPanel.module.css';

/**
 * Bottleneck issue with blocking count.
 */
interface Bottleneck {
  id: string;
  title: string;
  priority: number;
  blockingCount: number;
}

/**
 * Props for the ProjectHealthPanel component.
 */
export interface ProjectHealthPanelProps {
  /** Project statistics from useAgents */
  stats: LoomStats;
  /** Blocked issues for bottleneck detection */
  blockedIssues: BlockedIssue[] | null;
  /** Whether data is loading */
  isLoading: boolean;
  /** Callback when a bottleneck issue is clicked */
  onBottleneckClick?: (issue: Pick<Issue, 'id' | 'title'>) => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Derive bottlenecks from blocked issues.
 * Returns issues that block multiple others, sorted by impact.
 */
function deriveBottlenecks(blockedIssues: BlockedIssue[] | null): Bottleneck[] {
  if (!blockedIssues || blockedIssues.length === 0) {
    return [];
  }

  // Build frequency map: blockerId -> { count, info from first blocked issue }
  const blockerMap = new Map<string, { count: number; title?: string; priority?: number }>();

  for (const issue of blockedIssues) {
    for (const blockerId of issue.blocked_by) {
      const existing = blockerMap.get(blockerId);
      if (existing) {
        existing.count++;
      } else {
        blockerMap.set(blockerId, { count: 1 });
      }
    }
  }

  // Find blockers with count > 1 (blocking multiple issues)
  const bottlenecks: Bottleneck[] = [];
  for (const [id, data] of blockerMap) {
    if (data.count > 1) {
      bottlenecks.push({
        id,
        title: data.title || id, // Fallback to ID if title not available
        priority: data.priority ?? 2,
        blockingCount: data.count,
      });
    }
  }

  // Sort by blocking count descending
  bottlenecks.sort((a, b) => b.blockingCount - a.blockingCount);

  // Return top 5
  return bottlenecks.slice(0, 5);
}

/**
 * ProjectHealthPanel displays project health metrics in a compact format.
 */
export function ProjectHealthPanel({
  stats,
  blockedIssues,
  isLoading,
  onBottleneckClick,
  className,
}: ProjectHealthPanelProps): JSX.Element {
  const bottlenecks = useMemo(() => deriveBottlenecks(blockedIssues), [blockedIssues]);

  const rootClassName = className ? `${styles.panel} ${className}` : styles.panel;

  // Calculate percentage for progress bar
  const completionPercent = Math.round(stats.completion);

  return (
    <div className={rootClassName} data-testid="project-health-panel">
      {/* Left Pane: Progress + Issue Counts */}
      <div className={styles.leftPane}>
        <div className={styles.progressContainer}>
          <div className={styles.progressBar}>
            <div
              className={styles.progressFill}
              style={{ width: `${completionPercent}%` }}
              role="progressbar"
              aria-valuenow={completionPercent}
              aria-valuemin={0}
              aria-valuemax={100}
              aria-label="Project completion"
            />
          </div>
          <span className={styles.progressLabel}>{completionPercent}%</span>
        </div>
        <div className={styles.countsGrid}>
          <div className={styles.countItem}>
            <span className={styles.countValue}>{stats.open}</span>
            <span className={styles.countLabel}>Open</span>
          </div>
          <div className={styles.countItem}>
            <span className={styles.countValue}>{stats.closed}</span>
            <span className={styles.countLabel}>Closed</span>
          </div>
          <div className={styles.countItem}>
            <span className={styles.countValue}>{stats.total}</span>
            <span className={styles.countLabel}>Total</span>
          </div>
        </div>
      </div>

      {/* Right Pane: Bottlenecks */}
      <div className={styles.rightPane}>
        <h3 className={styles.sectionLabel}>
          Bottlenecks
          {bottlenecks.length > 0 && (
            <span className={styles.bottleneckCount}>({bottlenecks.length})</span>
          )}
        </h3>
        {isLoading ? (
          <div className={styles.loading}>Loading...</div>
        ) : bottlenecks.length === 0 ? (
          <div className={styles.emptyState}>No bottlenecks detected</div>
        ) : (
          <ul className={styles.bottleneckList}>
            {bottlenecks.map((bottleneck, index) => (
              <li key={bottleneck.id} className={styles.bottleneckItem}>
                <button
                  type="button"
                  className={
                    index === 0 ? styles.bottleneckButtonHighlighted : styles.bottleneckButton
                  }
                  onClick={() =>
                    onBottleneckClick?.({ id: bottleneck.id, title: bottleneck.title })
                  }
                  disabled={!onBottleneckClick}
                  aria-current={index === 0 ? 'true' : undefined}
                >
                  <span className={styles.bottleneckId}>{bottleneck.id}</span>
                  <span className={styles.bottleneckBlockCount}>
                    blocks {bottleneck.blockingCount}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
