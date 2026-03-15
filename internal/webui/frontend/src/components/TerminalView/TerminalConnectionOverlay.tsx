/**
 * TerminalConnectionOverlay component.
 * Renders a state-dependent overlay on top of a terminal pane showing
 * connection status (spinner, disconnect message, error) with a reconnect button.
 */

import type { ConnectionState } from "./TerminalInstance";
import styles from "./TerminalConnectionOverlay.module.css";

export interface TerminalConnectionOverlayProps {
  connectionState: ConnectionState;
  hasConnected: boolean;
  onReconnect: () => void;
}

export function TerminalConnectionOverlay({
  connectionState,
  hasConnected,
  onReconnect,
}: TerminalConnectionOverlayProps): JSX.Element | null {
  if (connectionState === "connected" || connectionState === "crashed")
    return null;

  // Reconnecting in background — let terminal remain visible, tab dot shows status
  if (connectionState === "connecting" && hasConnected) return null;

  if (connectionState === "connecting" && !hasConnected) {
    return (
      <div
        className={`${styles.overlay} ${styles.backdrop}`}
        data-testid="terminal-connection-overlay"
      >
        <div className={styles.content}>
          <div className={styles.spinner} />
          <div className={styles.message}>Connecting...</div>
        </div>
      </div>
    );
  }

  if (connectionState === "error") {
    return (
      <div
        className={`${styles.overlay} ${styles.errorBackdrop}`}
        data-testid="terminal-connection-overlay"
      >
        <div className={styles.content}>
          <div className={styles.message}>Connection failed</div>
          <button
            type="button"
            className={styles.reconnectButton}
            onClick={onReconnect}
            data-testid="terminal-reconnect-button"
          >
            Reconnect
          </button>
        </div>
      </div>
    );
  }

  // disconnected
  return (
    <div
      className={`${styles.overlay} ${styles.backdrop}`}
      data-testid="terminal-connection-overlay"
    >
      <div className={styles.content}>
        <div className={styles.message}>Disconnected</div>
        <button
          type="button"
          className={styles.reconnectButton}
          onClick={onReconnect}
          data-testid="terminal-reconnect-button"
        >
          Reconnect
        </button>
        <div className={styles.subtext}>Auto-reconnecting...</div>
      </div>
    </div>
  );
}
