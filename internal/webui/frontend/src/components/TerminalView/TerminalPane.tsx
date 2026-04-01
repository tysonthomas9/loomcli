import type {
  ConnectionState,
  ContextMenuEvent,
  SearchResultInfo,
  TerminalInstanceHandle,
} from "./TerminalInstance";
import { TerminalInstance } from "./TerminalInstance";
import { CrashOverlay } from "./CrashOverlay";
import { NotesBar } from "./NotesBar";
import {
  ReconnectingOverlay,
  type ReconnectOverlayState,
} from "./ReconnectingOverlay";
import { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
import { WelcomeBanner } from "./WelcomeBanner";
import type { TabState } from "./terminalTabUtils";

export interface TerminalPaneProps {
  tab: TabState;
  isActive: boolean;
  instanceRef: (handle: TerminalInstanceHandle | null) => void;
  onConnectionStateChange: (
    state: ConnectionState,
    hasConnected: boolean,
  ) => void;
  onCopyNotify: () => void;
  onPasteRequest: () => void;
  onSearchRequest: () => void;
  onContextMenu: (event: ContextMenuEvent) => void;
  onReconnectStateChange: (state: ReconnectOverlayState) => void;
  onOutput: () => void;
  onSearchResultChange: (result: SearchResultInfo | null) => void;
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
  onCopyNotify,
  onPasteRequest,
  onSearchRequest,
  onContextMenu,
  onReconnectStateChange,
  onOutput,
  onSearchResultChange,
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
        onCopyNotify={onCopyNotify}
        onPasteRequest={onPasteRequest}
        onSearchRequest={onSearchRequest}
        onContextMenu={onContextMenu}
        onReconnectStateChange={onReconnectStateChange}
        onOutput={onOutput}
        onSearchResultChange={onSearchResultChange}
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
