import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { CrashOverlay } from "./CrashOverlay";
import { NotesBar } from "@/components/TerminalView/controls";
import {
  ReconnectingOverlay,
  type ReconnectOverlayState,
} from "./ReconnectingOverlay";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import { WelcomeBanner } from "@/components/TerminalView/layout";
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
  dismissedWelcome: boolean;
  onDismissWelcome: () => void;
  onExampleClick: (text: string) => void;
  notes: string;
  onSaveNotes: (text: string) => Promise<void>;
  isMetaLoading: boolean;
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
  dismissedWelcome,
  onDismissWelcome,
  onExampleClick,
  notes,
  onSaveNotes,
  isMetaLoading,
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
        agentName={tab.agentName}
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
          />
          <ReconnectingOverlay
            state={reconnectState}
            onReconnect={onReconnect}
          />
        </>
      )}
      {hasConnected && !dismissedWelcome && (
        <WelcomeBanner
          backendName={tab.backendName}
          isActive={isActive}
          onDismiss={onDismissWelcome}
          onExampleClick={onExampleClick}
        />
      )}
      <NotesBar notes={notes} onSave={onSaveNotes} isLoading={isMetaLoading} />
    </>
  );
}
