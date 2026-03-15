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
  crashReason?: string | null;
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

/** Regex to match trailing " (N)" counter suffix in a display label. */
const COUNTER_SUFFIX_RE = /\s+\(\d+\)$/;

/**
 * Extract the base name from a tab label by stripping any " (N)" suffix.
 */
export function extractBaseName(label: string): string {
  return label.replace(COUNTER_SUFFIX_RE, "");
}

/**
 * Compute the next duplicate label and session name for a given tab.
 * Returns null if MAX_TABS has been reached.
 *
 * Label format:  "{baseName} (N)"
 * Session format: "{sanitized-baseName}-N"
 */
export function getNextDuplicateName(
  sourceLabel: string,
  existingTabs: TabState[],
): { label: string; sessionName: string } | null {
  if (existingTabs.length >= MAX_TABS) return null;

  const baseName = extractBaseName(sourceLabel);
  // Find the maximum counter among all tabs with the same base name
  let maxCounter = 1; // The original counts as 1
  for (const tab of existingTabs) {
    const tabBase = extractBaseName(tab.label);
    if (tabBase !== baseName) continue;
    const match = tab.label.match(/\((\d+)\)$/);
    if (match?.[1]) {
      const n = parseInt(match[1], 10);
      if (n > maxCounter) maxCounter = n;
    }
  }
  const nextCounter = maxCounter + 1;
  const label = `${baseName} (${nextCounter})`;
  // Session names must match [a-zA-Z0-9_-]+
  const sanitizedBase =
    baseName.replace(/\s+/g, "-").replace(/[^a-zA-Z0-9_-]/g, "") || "tab";
  const sessionName = `${sanitizedBase}-${nextCounter}`;
  return { label, sessionName };
}
