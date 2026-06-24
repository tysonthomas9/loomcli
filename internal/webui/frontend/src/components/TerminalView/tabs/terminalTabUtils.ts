/**
 * Terminal tab utility functions.
 * Constants and helpers for tab naming, backend detection, and session naming.
 */

import { KNOWN_BACKEND_DEFAULTS } from "@/utils/workspace";

import type { ConnectionState } from "@/components/TerminalView/instances";

// Match the PTY manager's default per-workspace session cap.
export const MAX_TABS = 40;

/** Split view constants */
export const MIN_SPLIT_RATIO = 0.2;
export const MAX_SPLIT_RATIO = 0.8;
export const DEFAULT_SPLIT_RATIO = 0.5;
export const MIN_SPLIT_WIDTH_PX = 900;

/** Brand colors for each known backend, derived from KNOWN_BACKEND_DEFAULTS. */
export const BACKEND_BRAND_COLORS: Record<string, string> = Object.fromEntries(
  Object.entries(KNOWN_BACKEND_DEFAULTS).map(([k, v]) => [k, v.brandColor]),
);

export interface TabState {
  id: string;
  label: string;
  sessionName: string;
  connectionState: ConnectionState;
  backendName: string;
  pinned?: boolean;
  crashReason?: string | null;
  kind?: string;
  role?: string;
  /** When set, this tab represents an agent's PTY-backed terminal session. */
  agentName?: string;
  writable?: boolean;
}

/**
 * True when persisted metadata describes an agent harness PTY.
 * The session-name prefix is a fallback for legacy sessions persisted before
 * kind/agent_id existed; the user-editable label is deliberately NOT
 * consulted, so renaming a plain tab to "agent-…" can't reclassify it.
 */
export function isAgentMetadata(meta: {
  kind?: string;
  agent_id?: string;
  session_name?: string;
}): boolean {
  return (
    meta.kind === "agent" ||
    (meta.agent_id != null && meta.agent_id !== "") ||
    (meta.session_name?.startsWith("agent-") ?? false)
  );
}

/** True when the tab is an agent harness PTY (Agents view only). */
export function isAgentTab(
  tab: Pick<TabState, "kind" | "agentName" | "sessionName">,
): boolean {
  return (
    tab.kind === "agent" ||
    tab.agentName != null ||
    tab.sessionName.startsWith("agent-")
  );
}

/**
 * Extract backend name from a session name.
 * Parses `lead-{backend}-{n}` or `{workspace}--lead-{backend}-{n}` pattern;
 * falls back to defaultBackend.
 */
export function getBackendFromSessionName(
  sessionName: string,
  defaultBackend?: string,
): string {
  const leadIndex = sessionName.lastIndexOf("--lead-");
  const localName =
    leadIndex >= 0 ? sessionName.slice(leadIndex + 2) : sessionName;
  if (localName.startsWith("lead-shell-")) return "shell";
  // Match workspace-prefixed: {workspace}--lead-{backend}-{n}
  const prefixedMatch = sessionName.match(/^.+--lead-(.+)-\d+$/);
  if (prefixedMatch?.[1]) return prefixedMatch[1];
  // Match unprefixed: lead-{backend}-{n}
  const match = sessionName.match(/^lead-(.+)-\d+$/);
  if (match?.[1]) return match[1];
  return defaultBackend ?? "unknown";
}

/**
 * Generate an auto-incremented tab name for a given backend.
 * Returns `{wsPrefix}lead-{backend}-{n}` where n is max existing number + 1.
 * The wsPrefix namespaces tmux sessions per workspace to prevent leakage.
 */
export function generateTabName(
  backend: string,
  existingTabs: TabState[],
  workspace?: string,
): { sessionName: string; label: string } {
  const safeWorkspace = workspace ? sanitizeSessionName(workspace) : "";
  const wsPrefix =
    safeWorkspace && safeWorkspace !== "default" ? `${safeWorkspace}--` : "";
  const prefix = `lead-${backend}-`;
  const fullPrefix = `${wsPrefix}${prefix}`;
  let max = 0;
  for (const tab of existingTabs) {
    // Match both prefixed and unprefixed names within this workspace's tabs
    if (tab.sessionName.startsWith(fullPrefix)) {
      const num = parseInt(tab.sessionName.slice(fullPrefix.length), 10);
      if (!isNaN(num) && num > max) {
        max = num;
      }
    } else if (tab.sessionName.startsWith(prefix)) {
      const num = parseInt(tab.sessionName.slice(prefix.length), 10);
      if (!isNaN(num) && num > max) {
        max = num;
      }
    }
  }
  const n = max + 1;
  return {
    sessionName: `${fullPrefix}${n}`,
    label: `${prefix}${n}`,
  };
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
