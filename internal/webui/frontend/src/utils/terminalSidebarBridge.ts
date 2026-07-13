/**
 * Syncs global Terminal view tab state to the workspace sidebar without
 * lifting all tab state into App.
 */

import type { ConnectionState } from "@/components/TerminalView/instances";

export const TERMINAL_SIDEBAR_SYNC_EVENT = "loom:terminal-sidebar-sync";
export const TERMINAL_SIDEBAR_SELECT_EVENT = "loom:terminal-sidebar-select";
export const TERMINAL_SIDEBAR_NEW_TAB_EVENT = "loom:terminal-sidebar-new-tab";

export interface TerminalSidebarTab {
  id: string;
  label: string;
  backendName: string;
  connectionState: ConnectionState;
  pinned?: boolean;
}

export interface TerminalSidebarState {
  tabs: TerminalSidebarTab[];
  activeTabId: string;
}

export function publishTerminalSidebarState(state: TerminalSidebarState): void {
  window.dispatchEvent(
    new CustomEvent<TerminalSidebarState>(TERMINAL_SIDEBAR_SYNC_EVENT, {
      detail: state,
    }),
  );
}

export function requestTerminalTabSelect(tabId: string): void {
  window.dispatchEvent(
    new CustomEvent<{ tabId: string }>(TERMINAL_SIDEBAR_SELECT_EVENT, {
      detail: { tabId },
    }),
  );
}

export function requestTerminalNewTab(): void {
  window.dispatchEvent(new CustomEvent(TERMINAL_SIDEBAR_NEW_TAB_EVENT));
}
