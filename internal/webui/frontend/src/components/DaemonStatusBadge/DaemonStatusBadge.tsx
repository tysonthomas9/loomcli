/**
 * DaemonStatusBadge - Inline, non-blocking indicator of workspace service availability.
 * Replaces the full-page DaemonUnavailableOverlay modal so users can still
 * interact with the UI while the workspace service is reconnecting.
 */

import type { DaemonConnectionMode } from "@/hooks/workspace/useDaemonHealth";

import styles from "./DaemonStatusBadge.module.css";

export interface DaemonStatusBadgeProps {
  /** Whether the workspace service is currently available. */
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
      ? `Workspace service offline \u00B7 ${retryCountdown}s`
      : "Workspace service offline";

  return (
    <button
      className={styles.badge}
      onClick={onRetry}
      type="button"
      title="Click to retry workspace service connection"
      aria-live="polite"
    >
      <span className={styles.dot} aria-hidden="true" />
      <span className={styles.label}>{label}</span>
    </button>
  );
}
