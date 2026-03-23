/**
 * Utility for cleaning up terminal tab sessions.
 * Fire-and-forget cleanup: deletes tab metadata for terminal tabs.
 * Used when panel closes or issue changes to prevent orphaned tmux sessions.
 */

import { deleteTabMetadata } from "@/api/terminal";

export interface CleanupTab {
  type?: string;
  metadata?: {
    sessionName?: string;
  };
}

/**
 * Delete metadata for all terminal tabs. Fire-and-forget.
 */
export function cleanupTerminalTabs(tabs: CleanupTab[]): void {
  for (const tab of tabs) {
    if (tab.type === "terminal" && tab.metadata?.sessionName) {
      deleteTabMetadata(tab.metadata.sessionName).catch(() => {});
    }
  }
}
