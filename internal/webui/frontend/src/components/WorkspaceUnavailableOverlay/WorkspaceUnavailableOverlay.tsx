/**
 * WorkspaceUnavailableOverlay - Full-page overlay shown when the workspace service is unavailable.
 * Renders on top of AppLayout content, allowing Settings navigation via callback.
 * Distinguishes between never-connected and lost-connection states.
 */

import { useRef } from "react";

import type { WorkspaceConnectionMode } from "@/hooks/workspace";
import { useFocusTrap } from "@/hooks";

import styles from "./WorkspaceUnavailableOverlay.module.css";

export interface WorkspaceUnavailableOverlayProps {
  /** Connection mode determines messaging and UI. */
  mode: WorkspaceConnectionMode;
  /** Seconds until next automatic retry. */
  retryCountdown: number;
  /** Last error message from health check. */
  lastError: string | null;
  /** Trigger immediate retry. */
  onRetry: () => void;
  /** Navigate to settings view. */
  onSettingsClick: () => void;
}

export function WorkspaceUnavailableOverlay({
  mode,
  retryCountdown,
  lastError,
  onRetry,
  onSettingsClick,
}: WorkspaceUnavailableOverlayProps): JSX.Element {
  const overlayRef = useRef<HTMLDivElement>(null);

  // Focus trap: keeps Tab/Shift+Tab within the overlay
  useFocusTrap(overlayRef, true);

  const isNeverConnected = mode === "never_connected";
  const isStarting = mode === "starting";

  const titleText = isStarting
    ? "Workspace loading\u2026"
    : isNeverConnected
      ? "Connecting to workspace service\u2026"
      : "Connection to workspace service lost";

  const descriptionText = isStarting
    ? "The workspace service is starting up. This may take a few minutes for large repositories."
    : null;

  return (
    <div
      className={styles.overlay}
      role="dialog"
      aria-modal="true"
      aria-labelledby="workspace-overlay-title"
      ref={overlayRef}
      tabIndex={-1}
    >
      <div className={styles.content}>
        <div className={styles.spinner} aria-hidden="true">
          <div className={styles.spinnerRing} />
        </div>

        <h2 id="workspace-overlay-title" className={styles.title}>
          {titleText}
        </h2>

        {descriptionText && (
          <p className={styles.description}>{descriptionText}</p>
        )}

        {!isNeverConnected && !isStarting && lastError && (
          <p className={styles.errorDetail}>{lastError}</p>
        )}

        {retryCountdown > 0 && (
          <p className={styles.countdown}>
            Retrying in {retryCountdown}s\u2026
          </p>
        )}
        {retryCountdown === 0 && !isNeverConnected && !isStarting && (
          <p className={styles.countdown}>Retrying\u2026</p>
        )}

        {!isNeverConnected && !isStarting && (
          <button
            className={styles.retryButton}
            onClick={onRetry}
            type="button"
          >
            Retry Now
          </button>
        )}

        <button
          className={styles.settingsLink}
          onClick={onSettingsClick}
          type="button"
        >
          Open Settings
        </button>
      </div>
    </div>
  );
}
