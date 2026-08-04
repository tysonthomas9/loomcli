/**
 * WorkspaceStatusBadge - Inline, non-blocking indicator of workspace service availability.
 * Replaces the full-page WorkspaceUnavailableOverlay modal so users can still
 * interact with the UI while the workspace service is reconnecting.
 */

import type { WorkspaceConnectionMode } from "@/hooks/workspace/useWorkspaceHealth";

import styles from "./WorkspaceStatusBadge.module.css";

export interface WorkspaceStatusBadgeProps {
  /** Whether the workspace service is currently available. */
  isWorkspaceAvailable: boolean;
  /** Connection mode determines messaging. */
  mode: WorkspaceConnectionMode;
  /** Seconds until next automatic retry. */
  retryCountdown: number;
  /** Last error message from health check. */
  lastError: string | null;
  /** Trigger immediate retry. */
  onRetry: () => void;
}

export function WorkspaceStatusBadge({
  isWorkspaceAvailable,
  mode,
  retryCountdown,
  onRetry,
}: WorkspaceStatusBadgeProps): JSX.Element | null {
  if (isWorkspaceAvailable) return null;

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
