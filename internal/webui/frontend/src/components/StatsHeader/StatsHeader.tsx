/**
 * StatsHeader - Displays real-time issue statistics.
 * Shows Open, In Progress, Ready, and Closed counts with color-coded badges.
 */

import { memo } from 'react'
import type { Statistics } from '@/types'
import { StatBadge } from './StatBadge'
import styles from './StatsHeader.module.css'

/**
 * Props for the StatsHeader component.
 */
export interface StatsHeaderProps {
  /** Statistics data to display */
  stats: Statistics | null
  /** Whether data is loading */
  loading?: boolean
  /** Error from stats fetch */
  error?: Error | null
  /** Callback to retry fetch on error */
  onRetry?: () => void
  /** Additional CSS class */
  className?: string
}

/**
 * StatsHeader component showing key project metrics.
 * Displays loading skeletons while fetching and error state on failure.
 */
export const StatsHeader = memo(function StatsHeader({
  stats,
  loading,
  error,
  onRetry,
  className,
}: StatsHeaderProps): JSX.Element {
  const classNames = [styles.statsHeader, className].filter(Boolean).join(' ')

  // Loading state: show skeleton badges
  if (loading && !stats) {
    return (
      <div className={classNames} data-testid="stats-header-loading">
        <div className={styles.skeleton} />
        <div className={styles.skeleton} />
        <div className={styles.skeleton} />
        <div className={styles.skeleton} />
      </div>
    )
  }

  // Error state: show compact error with retry
  if (error && !stats) {
    return (
      <div className={classNames} data-testid="stats-header-error">
        <button
          className={styles.error}
          onClick={onRetry}
          title={error.message}
          type="button"
          aria-label="Retry loading statistics"
        >
          <span aria-hidden="true">⚠</span>
          <span>Stats unavailable</span>
        </button>
      </div>
    )
  }

  // No data yet (shouldn't happen in normal flow)
  if (!stats) {
    return <div className={classNames} data-testid="stats-header-empty" />
  }

  // Success state: show stat badges
  return (
    <div className={classNames} data-testid="stats-header">
      <StatBadge label="Open" value={stats.open_issues} variant="open" />
      <StatBadge label="In Progress" value={stats.in_progress_issues} variant="progress" />
      <StatBadge label="Ready" value={stats.ready_issues} variant="ready" />
      <StatBadge label="Closed" value={stats.closed_issues} variant="closed" />
    </div>
  )
})
