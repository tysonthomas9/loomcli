/**
 * TerminalView instances sub-barrel.
 */

export { TerminalInstance } from "./TerminalInstance";
export type {
  ConnectionState,
  ContextMenuEvent,
  SearchResultInfo,
  TerminalInstanceProps,
  TerminalInstanceHandle,
} from "./TerminalInstance";

export { TerminalPane } from "./TerminalPane";
export type { TerminalPaneProps } from "./TerminalPane";

export { TerminalPaneArea } from "./TerminalPaneArea";

export { connectWebSocket, encodeResize } from "./terminalConnection";
export { getTerminalTheme } from "./terminalTheme";

export { useConnectionState } from "./useConnectionState";
export { useSessionSeeding } from "./useSessionSeeding";
export { useClipboard } from "./useClipboard";

export { ReconnectingOverlay } from "./ReconnectingOverlay";
export type {
  ReconnectingOverlayProps,
  ReconnectOverlayState,
} from "./ReconnectingOverlay";

export { TerminalConnectionOverlay } from "./TerminalConnectionOverlay";
export type { TerminalConnectionOverlayProps } from "./TerminalConnectionOverlay";

export { CrashOverlay } from "./CrashOverlay";
export type { CrashOverlayProps } from "./CrashOverlay";
