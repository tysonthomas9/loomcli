/**
 * DaemonStatusBadge - Inline, non-blocking indicator of daemon availability.
 * Replaces the full-page DaemonUnavailableOverlay modal so users can still
 * interact with the UI while the daemon is reconnecting.
 */

import type { DaemonConnectionMode } from "@/hooks/useDaemonHealth";

import styles from "./DaemonStatusBadge.module.css";

export interface DaemonStatusBadgeProps {
  /** Whether the daemon is currently available. */
  isDaemonAvailable: boolean;
  /** Connection mode determines messaging. */
  mode: DaemonConnectionMode;
  /** Seconds until next automatic retry. */
  retryCountdown: number;
  /** Last error message from health check. */
  lastError: string | null;
  /** Trigger immediate retry. */
  onRetry: () => void;
}

export function DaemonStatusBadge({
  isDaemonAvailable,
  mode,
  retryCountdown,
  onRetry,
}: DaemonStatusBadgeProps): JSX.Element | null {
  if (isDaemonAvailable) return null;

  const isNeverConnected = mode === "never_connected";
  const label = isNeverConnected
    ? "Connecting\u2026"
    : retryCountdown > 0
      ? `Daemon offline \u00B7 ${retryCountdown}s`
      : "Daemon offline";

  return (
    <button
      className={styles.badge}
      onClick={onRetry}
      type="button"
      title="Click to retry daemon connection"
      aria-live="polite"
    >
      <span className={styles.dot} aria-hidden="true" />
      <span className={styles.label}>{label}</span>
    </button>
  );
}
