/**
 * ConnectionBanner displays a warning when showing stale data due to loom server disconnect.
 * Shows last updated time, retry countdown, and manual retry button.
 */

import styles from './ConnectionBanner.module.css';

/**
 * Props for the ConnectionBanner component.
 */
export interface ConnectionBannerProps {
  /** Last successful update time */
  lastUpdated: Date | null;
  /** Seconds until next auto-retry (0 if not waiting) */
  retryCountdown: number;
  /** Whether currently reconnecting */
  isReconnecting: boolean;
  /** Handler for manual retry */
  onRetry: () => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format a relative time string from a date.
 */
function formatRelativeTime(date: Date): string {
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffSec = Math.floor(diffMs / 1000);
  const diffMin = Math.floor(diffSec / 60);
  const diffHour = Math.floor(diffMin / 60);

  if (diffSec < 60) {
    return 'just now';
  } else if (diffMin < 60) {
    return `${diffMin} minute${diffMin === 1 ? '' : 's'} ago`;
  } else if (diffHour < 24) {
    return `${diffHour} hour${diffHour === 1 ? '' : 's'} ago`;
  } else {
    const diffDay = Math.floor(diffHour / 24);
    return `${diffDay} day${diffDay === 1 ? '' : 's'} ago`;
  }
}

/**
 * ConnectionBanner shows a warning banner when displaying stale data.
 */
export function ConnectionBanner({
  lastUpdated,
  retryCountdown,
  isReconnecting,
  onRetry,
  className,
}: ConnectionBannerProps): JSX.Element {
  const rootClassName = [styles.banner, className].filter(Boolean).join(' ');

  const relativeTime = lastUpdated ? formatRelativeTime(lastUpdated) : 'unknown';
  const isStale = lastUpdated && (new Date().getTime() - lastUpdated.getTime()) > 5 * 60 * 1000;

  return (
    <div
      className={rootClassName}
      role="alert"
      aria-live="polite"
      data-stale={isStale ? 'true' : undefined}
    >
      <span className={styles.icon} aria-hidden="true">
        ⚠️
      </span>
      <span className={styles.message}>
        <strong>Disconnected from loom server</strong>
        <span className={styles.detail}>
          Last updated {relativeTime}
        </span>
      </span>
      <div className={styles.actions}>
        {retryCountdown > 0 && (
          <span className={styles.countdown} aria-live="off">
            Retrying in {retryCountdown}s
          </span>
        )}
        <button
          className={styles.retryButton}
          onClick={onRetry}
          disabled={isReconnecting}
          aria-label="Retry connection now"
        >
          {isReconnecting ? 'Connecting...' : 'Retry Now'}
        </button>
      </div>
    </div>
  );
}
