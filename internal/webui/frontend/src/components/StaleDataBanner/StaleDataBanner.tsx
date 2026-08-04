/**
 * StaleDataBanner - Shows a warning banner when the connection
 * has been down for >5 seconds or is permanently lost.
 */

import { useState, useEffect, useRef } from "react";

import styles from "./StaleDataBanner.module.css";

export interface StaleDataBannerProps {
  /** Timestamp (ms) when disconnection started */
  disconnectedSince: number;
  /** Retry callback */
  onRetry?: () => void;
  /** Whether the connection has been permanently lost */
  connectionLost?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format elapsed seconds into a human-readable string.
 */
function formatElapsed(seconds: number): string {
  if (seconds < 60) {
    return `${seconds}s`;
  }
  const minutes = Math.floor(seconds / 60);
  const remainingSeconds = seconds % 60;
  return `${minutes}m ${remainingSeconds}s`;
}

export function StaleDataBanner({
  disconnectedSince,
  onRetry,
  connectionLost = false,
  className,
}: StaleDataBannerProps): JSX.Element {
  const [elapsed, setElapsed] = useState(() =>
    Math.floor((Date.now() - disconnectedSince) / 1000),
  );
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    // Update elapsed time every second
    setElapsed(Math.floor((Date.now() - disconnectedSince) / 1000));
    intervalRef.current = setInterval(() => {
      setElapsed(Math.floor((Date.now() - disconnectedSince) / 1000));
    }, 1000);

    return () => {
      if (intervalRef.current) {
        clearInterval(intervalRef.current);
      }
    };
  }, [disconnectedSince]);

  const rootClassName = [styles.banner, className].filter(Boolean).join(" ");

  return (
    <div
      className={rootClassName}
      role="alert"
      aria-live="assertive"
      data-lost={connectionLost ? "true" : undefined}
    >
      <span className={styles.icon} aria-hidden="true">
        {"\u26A0"}
      </span>
      <span className={styles.message}>
        {connectionLost
          ? "Connection lost"
          : `Reconnecting \u2014 data may be stale (${formatElapsed(elapsed)})`}
      </span>
      {onRetry && connectionLost && (
        <button
          type="button"
          className={styles.retryButton}
          onClick={onRetry}
          aria-label="Retry connection"
        >
          Retry
        </button>
      )}
    </div>
  );
}
