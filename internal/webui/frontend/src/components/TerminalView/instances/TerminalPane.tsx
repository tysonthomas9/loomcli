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
}: TerminalPaneProps) {
  return (
    <>
      <TerminalInstance
        ref={instanceRef}
        sessionName={tab.sessionName}
        isActive={isActive}
        onConnectionStateChange={onConnectionStateChange}
        onReconnectStateChange={onReconnectStateChange}
        onOutput={onOutput}
        onBackendCrash={onBackendCrash}
        onTerminalFocus={onTerminalFocus}
        writable={tab.writable}
        ptyAlive={ptyAlive}
        autoStartStaleSession={autoStartStaleSession}
        autoReconnect={autoReconnect}
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
          />
          <ReconnectingOverlay
            state={reconnectState}
            onReconnect={onReconnect}
          />
        </>
      )}
    </>
  );
}
