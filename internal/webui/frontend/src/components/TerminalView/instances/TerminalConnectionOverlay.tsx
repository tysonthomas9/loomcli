/**
 * TerminalConnectionOverlay component.
 * Renders a state-dependent overlay on top of a terminal pane showing
 * connection status (spinner, disconnect message, error) with a reconnect button.
 */

import { useEffect, useRef } from "react";

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
  const reconnectButtonRef = useRef<HTMLButtonElement>(null);

  // Auto-focus reconnect button when entering error/disconnected state.
  // Only focus if the button is visible (offsetParent is null for display:none ancestors,
  // which is how inactive tabs are hidden — prevents stealing focus from the active tab).
  useEffect(() => {
    if (connectionState === "error" || connectionState === "disconnected") {
      const btn = reconnectButtonRef.current;
      if (btn && btn.offsetParent !== null) {
        btn.focus();
      }
    }
  }, [connectionState]);

  if (connectionState === "connected" || connectionState === "crashed")
    return null;

  // Reconnecting in background — let terminal remain visible, tab dot shows status
  if (connectionState === "connecting" && hasConnected) return null;

  if (connectionState === "connecting" && !hasConnected) {
    return (
      <div
        className={`${styles.overlay} ${styles.backdrop}`}
        role="status"
        aria-live="polite"
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
        role="alert"
        data-testid="terminal-connection-overlay"
      >
        <div className={styles.content}>
          <div className={styles.message}>Connection failed</div>
          <button
            ref={reconnectButtonRef}
            type="button"
            className={styles.reconnectButton}
            onClick={onReconnect}
            aria-label="Reconnect to terminal"
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
      role="alert"
      data-testid="terminal-connection-overlay"
    >
      <div className={styles.content}>
        <div className={styles.message}>Disconnected</div>
        <button
          ref={reconnectButtonRef}
          type="button"
          className={styles.reconnectButton}
          onClick={onReconnect}
          aria-label="Reconnect to terminal"
          data-testid="terminal-reconnect-button"
        >
          Reconnect
        </button>
        <div className={styles.subtext}>Auto-reconnecting...</div>
      </div>
    </div>
  );
}
