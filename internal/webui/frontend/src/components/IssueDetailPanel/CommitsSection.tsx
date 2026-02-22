/**
 * CommitsSection component.
 * Displays a list of git commits associated with an issue.
 */

import { useEffect, useState } from 'react';

import { getIssueCommits } from '@/api';
import type { CommitRecord } from '@/types';

import styles from './CommitsSection.module.css';

/**
 * Format a relative timestamp for display.
 */
function formatRelativeTime(timestamp: string): string {
  const date = new Date(timestamp);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffMins = Math.floor(diffMs / 60000);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffMins < 1) return 'just now';
  if (diffMins < 60) return `${diffMins}m ago`;
  if (diffHours < 24) return `${diffHours}h ago`;
  if (diffDays < 30) return `${diffDays}d ago`;
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}

/**
 * Props for the CommitsSection component.
 */
export interface CommitsSectionProps {
  issueId: string;
  className?: string;
}

/**
 * CommitsSection displays git commits associated with an issue.
 * Fetches commits from the API and displays them in a collapsible list.
 */
export function CommitsSection({ issueId, className }: CommitsSectionProps): JSX.Element {
  const [commits, setCommits] = useState<CommitRecord[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isError, setIsError] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    setIsError(false);

    getIssueCommits(issueId)
      .then((data) => {
        if (!cancelled) {
          setCommits(data);
          setIsLoading(false);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setCommits([]);
          setIsError(true);
          setIsLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [issueId]);

  const hasCommits = commits.length > 0;
  const rootClassName = [styles.section, className].filter(Boolean).join(' ');

  // Don't render the section if commits are unavailable (e.g. beads dir not configured)
  if (isError && !isLoading) {
    return <></>;
  }

  return (
    <section className={rootClassName} data-testid="commits-section">
      <h3 className={styles.sectionTitle}>
        Commits{hasCommits ? ` (${commits.length})` : ''}
      </h3>

      {isLoading ? (
        <p className={styles.emptyState}>Loading commits...</p>
      ) : hasCommits ? (
        <ul className={styles.commitList}>
          {commits.map((commit) => (
            <li key={commit.sha} className={styles.commitItem} data-testid="commit-item">
              <div className={styles.commitHeader}>
                <span className={styles.commitSha}>{commit.sha.substring(0, 7)}</span>
                <time className={styles.commitTime} dateTime={commit.timestamp}>
                  {formatRelativeTime(commit.timestamp)}
                </time>
              </div>
              <p className={styles.commitSubject}>{commit.subject}</p>
              <div className={styles.commitMeta}>
                <span className={styles.commitAuthor}>{commit.author}</span>
                {commit.worktree && (
                  <span className={styles.commitWorktree}>{commit.worktree}</span>
                )}
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.emptyState} data-testid="commits-empty">
          No commits recorded yet.
        </p>
      )}
    </section>
  );
}
