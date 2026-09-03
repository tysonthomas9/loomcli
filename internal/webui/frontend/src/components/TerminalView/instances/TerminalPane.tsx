import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { CrashOverlay } from "./CrashOverlay";
import {
  ReconnectingOverlay,
  type ReconnectOverlayState,
} from "./ReconnectingOverlay";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import type { TabState } from "@/components/TerminalView/tabs";

export interface TerminalPaneProps {
  tab: TabState;
  isActive: boolean;
  instanceRef: (handle: TerminalInstanceHandle | null) => void;
  onConnectionStateChange: (
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  onReconnectStateChange: (state: ReconnectOverlayState) => void;
  onOutput: () => void;
  onBackendCrash: (reason: string) => void;
  onCrashRestart: () => void;
  onCloseTab: () => void;
  onReconnect: () => void;
  onTerminalFocus: (() => void) | undefined;
  hasConnected: boolean;
  reconnectState: ReconnectOverlayState;
  /**
   * False when the backend reports this tab's PTY is not running — either
   * metadata survived a server restart without the shell, or the shell
   * exited mid-session. Undefined (or true) means the normal "try to
   * connect" path is fine. When false, TerminalInstance should skip its
   * auto-connect and render the session-ended overlay so the user opts
   * in to spawning a new shell.
   */
  ptyAlive?: boolean | undefined;
  /** Automatically replace stale PTYs for tabs where losing old scrollback is acceptable. */
  autoStartStaleSession?: boolean | undefined;
  /** Automatically reconnect after an unexpected WebSocket close. */
  autoReconnect?: boolean | undefined;
  /**
   * Called with the RFC3339 replacement timestamp when the server announces
   * on attach that this tab's shell was replaced across a server restart.
   */
  onSessionReplaced?: ((replacedAt: string) => void) | undefined;
}

export function TerminalPane({
  tab,
  isActive,
  instanceRef,
  onConnectionStateChange,
  onReconnectStateChange,
  onOutput,
  onBackendCrash,
  onCrashRestart,
  onCloseTab,
  onReconnect,
  onTerminalFocus,
  hasConnected,
  reconnectState,
  ptyAlive,
  autoStartStaleSession,
  autoReconnect,
  onSessionReplaced,
}: TerminalPaneProps) {
  // TerminalConnectionOverlay renders its own overlay for the initial
  // connecting spinner and for every actionable state (disconnected /
  // error / session_ended). ReconnectingOverlay is the slim background
  // indicator shown only while the terminal itself stays visible
  // (connecting after a prior successful connect). Rendering both at once
  // — e.g. "Disconnected" + "Auto-reconnecting..." subtext on top of a
  // faint "Reconnecting..." pulse — produced overlapping text and two
  // Reconnect buttons. Suppress ReconnectingOverlay whenever the
  // connection overlay owns the pane; it carries the expired message as
  // subtext so no call-to-action is lost.
  const connectionOverlayVisible =
    tab.connectionState === "connecting"
      ? !hasConnected
      : tab.connectionState === "disconnected" ||
        tab.connectionState === "error" ||
        tab.connectionState === "session_ended";
  return (
    <>
      <TerminalInstance
        ref={instanceRef}
        sessionName={tab.sessionName}
        isActive={isActive}
        backendName={tab.backendName}
        onConnectionStateChange={onConnectionStateChange}
        onReconnectStateChange={onReconnectStateChange}
        onOutput={onOutput}
        onBackendCrash={onBackendCrash}
        onTerminalFocus={onTerminalFocus}
        writable={tab.writable}
        ptyAlive={ptyAlive}
        autoStartStaleSession={autoStartStaleSession}
        autoReconnect={autoReconnect}
        onSessionReplaced={onSessionReplaced}
      />
      {tab.crashReason != null ? (
        <CrashOverlay
          reason={tab.crashReason}
          onRestart={onCrashRestart}
          onCloseTab={onCloseTab}
        />
      ) : (
        <>
          <TerminalConnectionOverlay
            connectionState={tab.connectionState}
            hasConnected={hasConnected}
            onReconnect={onReconnect}
            autoReconnect={autoReconnect}
            isAutoReconnecting={reconnectState === "reconnecting"}
            reconnectExpired={reconnectState === "expired"}
          />
          {!connectionOverlayVisible && (
            <ReconnectingOverlay
              state={reconnectState}
              onReconnect={onReconnect}
            />
          )}
        </>
      )}
    </>
  );
}
