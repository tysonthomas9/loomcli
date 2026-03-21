/**
 * DaemonUnavailableOverlay - Full-page overlay shown when the daemon is unavailable.
 * Renders on top of AppLayout content, allowing Settings navigation via callback.
 * Distinguishes between never-connected and lost-connection states.
 */

import { useRef } from "react";

import type { DaemonConnectionMode } from "@/hooks/useDaemonHealth";
import { useFocusTrap } from "@/hooks";

import styles from "./DaemonUnavailableOverlay.module.css";

export interface DaemonUnavailableOverlayProps {
  /** Connection mode determines messaging and UI. */
  mode: DaemonConnectionMode;
  /** Seconds until next automatic retry. */
  retryCountdown: number;
  /** Last error message from health check. */
  lastError: string | null;
  /** Trigger immediate retry. */
  onRetry: () => void;
  /** Navigate to settings view. */
  onSettingsClick: () => void;
}

export function DaemonUnavailableOverlay({
  mode,
  retryCountdown,
  lastError,
  onRetry,
  onSettingsClick,
}: DaemonUnavailableOverlayProps): JSX.Element {
  const overlayRef = useRef<HTMLDivElement>(null);

  // Focus trap: keeps Tab/Shift+Tab within the overlay
  useFocusTrap(overlayRef, true);

  const isNeverConnected = mode === "never_connected";

  return (
    <div
      className={styles.overlay}
      role="dialog"
      aria-modal="true"
      aria-labelledby="daemon-overlay-title"
      ref={overlayRef}
      tabIndex={-1}
    >
      <div className={styles.content}>
        <div className={styles.spinner} aria-hidden="true">
          <div className={styles.spinnerRing} />
        </div>

        <h2 id="daemon-overlay-title" className={styles.title}>
          {isNeverConnected
            ? "Connecting to daemon\u2026"
            : "Connection to daemon lost"}
        </h2>

        {!isNeverConnected && lastError && (
          <p className={styles.errorDetail}>{lastError}</p>
        )}

        {retryCountdown > 0 && (
          <p className={styles.countdown}>
            Retrying in {retryCountdown}s\u2026
          </p>
        )}
        {retryCountdown === 0 && !isNeverConnected && (
          <p className={styles.countdown}>Retrying\u2026</p>
        )}

        {!isNeverConnected && (
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
