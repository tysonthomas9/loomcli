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
  autoReconnect?: boolean | undefined;
  isAutoReconnecting?: boolean | undefined;
  /**
   * True when the auto-reconnect backoff has exhausted its attempts. Shown
   * as a "Session expired" subtext under the Disconnected message so the
   * user knows the client gave up and the Reconnect button is the only way
   * forward. Only meaningful for the `disconnected` state.
   */
  reconnectExpired?: boolean | undefined;
}

// ActionableState variants share the same "message + button + optional
// subtext" shell; only copy, styling, and test IDs vary.
type ActionableState =
  | "session_ended"
  | "spawn_failed"
  | "error"
  | "disconnected";
const actionableStates: Record<
  ActionableState,
  {
    backdrop: string | undefined;
    role: "alert";
    message: string;
    buttonLabel: string;
    buttonAriaLabel: string;
    buttonTestId: string;
    subtext?: string;
  }
> = {
  session_ended: {
    backdrop: styles.backdrop,
    role: "alert",
    message: "Session ended",
    buttonLabel: "Start new session",
    buttonAriaLabel: "Start new terminal session",
    buttonTestId: "terminal-restart-button",
    subtext:
      "The shell backing this tab is no longer running. Starting a new session will spawn a fresh shell (scrollback is lost).",
  },
  spawn_failed: {
    backdrop: styles.errorBackdrop,
    role: "alert",
    message: "Shell keeps exiting",
    buttonLabel: "Try again",
    buttonAriaLabel: "Try starting the terminal session again",
    buttonTestId: "terminal-spawn-failed-button",
    subtext:
      "Every attempt started and exited within seconds, so retrying was stopped. The shell's own error is printed above.",
  },
  error: {
    backdrop: styles.errorBackdrop,
    role: "alert",
    message: "Connection failed",
    buttonLabel: "Reconnect",
    buttonAriaLabel: "Reconnect to terminal",
    buttonTestId: "terminal-reconnect-button",
  },
  disconnected: {
    backdrop: styles.backdrop,
    role: "alert",
    message: "Disconnected",
    buttonLabel: "Reconnect",
    buttonAriaLabel: "Reconnect to terminal",
    buttonTestId: "terminal-reconnect-button",
    subtext: "Auto-reconnecting...",
  },
};

export function TerminalConnectionOverlay({
  connectionState,
  hasConnected,
  onReconnect,
  autoReconnect = true,
  isAutoReconnecting = false,
  reconnectExpired = false,
}: TerminalConnectionOverlayProps): JSX.Element | null {
  const reconnectButtonRef = useRef<HTMLButtonElement>(null);

  // Auto-focus reconnect button when entering an actionable state. Only
  // focus if the button is visible (offsetParent null for display:none
  // ancestors — how inactive tabs hide — prevents stealing focus from the
  // active tab).
  useEffect(() => {
    if (connectionState in actionableStates) {
      const btn = reconnectButtonRef.current;
      if (btn && btn.offsetParent !== null) {
        btn.focus();
      }
    }
  }, [connectionState]);

  if (connectionState === "connected" || connectionState === "crashed")
    return null;

  // Reconnecting in background — leave the terminal visible; tab dot shows status.
  if (connectionState === "connecting" && hasConnected) return null;

  if (connectionState === "connecting") {
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

  const cfg = actionableStates[connectionState];
  // For the disconnected state the subtext reflects the auto-reconnect
  // loop's progress: "Session expired" once the backoff gave up (the
  // Reconnect button is now the only way forward), otherwise the existing
  // "Auto-reconnecting..." while the loop is still retrying. Error and
  // session_ended keep their own static subtext.
  const subtext =
    connectionState === "disconnected"
      ? reconnectExpired
        ? "Session expired"
        : autoReconnect && isAutoReconnecting
          ? cfg.subtext
          : undefined
      : cfg.subtext;
  return (
    <div
      className={`${styles.overlay} ${cfg.backdrop}`}
      role={cfg.role}
      data-testid="terminal-connection-overlay"
    >
      <div className={styles.content}>
        <div className={styles.message}>{cfg.message}</div>
        <button
          ref={reconnectButtonRef}
          type="button"
          className={styles.reconnectButton}
          onClick={onReconnect}
          aria-label={cfg.buttonAriaLabel}
          data-testid={cfg.buttonTestId}
        >
          {cfg.buttonLabel}
        </button>
        {subtext && <div className={styles.subtext}>{subtext}</div>}
      </div>
    </div>
  );
}
