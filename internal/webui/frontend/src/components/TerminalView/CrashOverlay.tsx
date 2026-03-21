/**
 * CrashOverlay component.
 * Inline error overlay displayed over the terminal when the backend process
 * crashes or exits unexpectedly. Shows the failure reason and provides
 * Restart and Close Tab buttons.
 */

import styles from "./CrashOverlay.module.css";

export interface CrashOverlayProps {
  reason: string;
  onRestart: () => void;
  onCloseTab: () => void;
}

export function CrashOverlay({
  reason,
  onRestart,
  onCloseTab,
}: CrashOverlayProps): JSX.Element {
  return (
    <div className={styles.overlay} data-testid="crash-overlay" role="alert">
      <div className={styles.card}>
        <div className={styles.heading}>
          <span className={styles.errorIcon} aria-hidden="true">
            !
          </span>
          Backend Exited
        </div>
        {reason && (
          <div className={styles.reason} data-testid="crash-overlay-reason">
            {reason}
          </div>
        )}
        <div className={styles.actions}>
          <button
            type="button"
            className={styles.restartButton}
            onClick={onRestart}
            data-testid="crash-overlay-restart"
          >
            Restart
          </button>
          <button
            type="button"
            className={styles.closeButton}
            onClick={onCloseTab}
            data-testid="crash-overlay-close"
          >
            Close Tab
          </button>
        </div>
      </div>
    </div>
  );
}
