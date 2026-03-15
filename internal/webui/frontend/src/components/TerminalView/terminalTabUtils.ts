/**
 * Terminal tab utility functions.
 * Constants and helpers for tab naming, backend detection, and session naming.
 */

import type { ConnectionState } from "./TerminalInstance";

export const MAX_TABS = 8;

/** Brand colors for each known backend. */
export const BACKEND_BRAND_COLORS: Record<string, string> = {
  claude: "#D97706",
  codex: "#22c55e",
  opencode: "#3B82F6",
};

export interface TabState {
  id: string;
  label: string;
  sessionName: string;
  connectionState: ConnectionState;
  backendName: string;
}

/**
 * Extract backend name from a session name.
 * Parses `lead-{backend}-{n}` pattern; falls back to defaultBackend.
 */
export function getBackendFromSessionName(
  sessionName: string,
  defaultBackend?: string,
): string {
  const match = sessionName.match(/^lead-(.+)-\d+$/);
  if (match?.[1]) return match[1];
  return defaultBackend ?? "unknown";
}

/**
 * Generate an auto-incremented tab name for a given backend.
 * Returns `lead-{backend}-{n}` where n is max existing number + 1.
 */
export function generateTabName(
  backend: string,
  existingTabs: TabState[],
): string {
  const prefix = `lead-${backend}-`;
  let max = 0;
  for (const tab of existingTabs) {
    if (tab.sessionName.startsWith(prefix)) {
      const num = parseInt(tab.sessionName.slice(prefix.length), 10);
      if (!isNaN(num) && num > max) {
        max = num;
      }
    }
  }
  return `${prefix}${max + 1}`;
}

/**
 * Sanitize an issue ID into a valid session name.
 * Replaces dots with dashes, strips non-alphanumeric/hyphen/underscore chars.
 */
export function sanitizeSessionName(issueId: string): string {
  return issueId.replace(/\./g, "-").replace(/[^a-zA-Z0-9_-]/g, "");
}
