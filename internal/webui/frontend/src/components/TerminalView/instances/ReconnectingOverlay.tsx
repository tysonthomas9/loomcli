import styles from "./ReconnectingOverlay.module.css";

export type ReconnectOverlayState = "reconnecting" | "expired" | null;

export interface ReconnectingOverlayProps {
  state: ReconnectOverlayState;
  attemptCount?: number;
  onReconnect?: () => void;
}

export function ReconnectingOverlay({
  state,
  attemptCount = 0,
  onReconnect,
}: ReconnectingOverlayProps): JSX.Element | null {
  if (state === null) return null;

  if (state === "expired") {
    return (
      <div className={styles.overlay} data-testid="reconnecting-overlay">
        <div className={styles.content}>
          <div className={styles.message}>Session expired</div>
          <button
            type="button"
            className={styles.reconnectButton}
            onClick={onReconnect}
            data-testid="reconnect-expired-button"
          >
            Reconnect
          </button>
        </div>
      </div>
    );
  }

  // reconnecting
  return (
    <div className={styles.overlay} data-testid="reconnecting-overlay">
      <div className={styles.content}>
        <div className={styles.pulse} />
        <div className={styles.message}>
          Reconnecting{attemptCount > 0 ? ` (attempt ${attemptCount})` : ""}...
        </div>
      </div>
    </div>
  );
}
