/**
 * Syncs global Terminal view tab state to the workspace sidebar without
 * lifting all tab state into App.
 */

import type { ConnectionState } from "@/components/TerminalView/instances";

export const TERMINAL_SIDEBAR_SYNC_EVENT = "loom:terminal-sidebar-sync";
export const TERMINAL_SIDEBAR_SELECT_EVENT = "loom:terminal-sidebar-select";
export const TERMINAL_SIDEBAR_NEW_TAB_EVENT = "loom:terminal-sidebar-new-tab";
export const TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT =
  "loom:terminal-sidebar-active-group";
export const TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT =
  "loom:terminal-sidebar-worktrees-changed";

export const DEFAULT_TERMINAL_WORKTREE_GROUP_ID = "__workspace__";

export interface TerminalWorktreeMember {
  repoName: string;
  baseBranch?: string;
  baseDetached?: boolean;
  reusedBranch?: boolean;
}

export interface TerminalWorktreeGroup {
  id: string;
  label: string;
  isDefault?: boolean;
  members: TerminalWorktreeMember[];
}

export interface TerminalSidebarTab {
  id: string;
  label: string;
  backendName: string;
  connectionState: ConnectionState;
  pinned?: boolean;
  groupId: string;
}

export interface TerminalSidebarState {
  groups: TerminalWorktreeGroup[];
  tabs: TerminalSidebarTab[];
  activeTabId: string;
  activeGroupId: string;
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

export function requestTerminalGroupSelect(groupId: string): void {
  window.dispatchEvent(
    new CustomEvent<{ groupId: string }>(TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT, {
      detail: { groupId },
    }),
  );
}

export function requestTerminalNewTab(groupId: string): void {
  window.dispatchEvent(
    new CustomEvent<{ groupId: string }>(TERMINAL_SIDEBAR_NEW_TAB_EVENT, {
      detail: { groupId },
    }),
  );
}

export function publishTerminalWorktreesChanged(
  group: TerminalWorktreeGroup,
): void {
  window.dispatchEvent(
    new CustomEvent<TerminalWorktreeGroup>(
      TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
      { detail: group },
    ),
  );
}
