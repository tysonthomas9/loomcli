/**
 * @vitest-environment jsdom
 */

import { describe, expect, it, vi } from "vitest";

import {
  publishTerminalSidebarState,
  publishTerminalWorktreesChanged,
  requestTerminalGroupSelect,
  requestTerminalNewTab,
  TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT,
  TERMINAL_SIDEBAR_NEW_TAB_EVENT,
  TERMINAL_SIDEBAR_SYNC_EVENT,
  TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
  type TerminalSidebarState,
  type TerminalWorktreeGroup,
} from "@/utils/terminalSidebarBridge";

describe("terminalSidebarBridge", () => {
  it("publishes groups, members, per-tab group ids, and active group", () => {
    const onSync = vi.fn();
    window.addEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, onSync);

    const state: TerminalSidebarState = {
      groups: [
        {
          id: "__workspace__",
          label: "Workspace",
          isDefault: true,
          members: [],
        },
        {
          id: "group-1",
          label: "Feature",
          members: [
            {
              repoName: "api",
              baseBranch: "main",
              baseDetached: false,
              reusedBranch: true,
            },
          ],
        },
      ],
      tabs: [
        {
          id: "lead-codex-1",
          label: "lead-codex-1",
          backendName: "codex",
          connectionState: "connected",
          groupId: "group-1",
        },
      ],
      activeTabId: "lead-codex-1",
      activeGroupId: "group-1",
    };

    publishTerminalSidebarState(state);

    expect(onSync).toHaveBeenCalledWith(
      expect.objectContaining({ detail: state }),
    );

    window.removeEventListener(TERMINAL_SIDEBAR_SYNC_EVENT, onSync);
  });

  it("dispatches group select, new-tab, and worktrees-changed payloads", () => {
    const onGroupSelect = vi.fn();
    const onNewTab = vi.fn();
    const onWorktreesChanged = vi.fn();
    window.addEventListener(TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT, onGroupSelect);
    window.addEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNewTab);
    window.addEventListener(
      TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
      onWorktreesChanged,
    );

    const group: TerminalWorktreeGroup = {
      id: "group-1",
      label: "Feature",
      members: [{ repoName: "api", baseBranch: "main" }],
    };

    requestTerminalGroupSelect("group-1");
    requestTerminalNewTab("group-1");
    publishTerminalWorktreesChanged(group);

    expect(onGroupSelect).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "group-1" } }),
    );
    expect(onNewTab).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "group-1" } }),
    );
    expect(onWorktreesChanged).toHaveBeenCalledWith(
      expect.objectContaining({ detail: group }),
    );

    window.removeEventListener(
      TERMINAL_SIDEBAR_ACTIVE_GROUP_EVENT,
      onGroupSelect,
    );
    window.removeEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNewTab);
    window.removeEventListener(
      TERMINAL_SIDEBAR_WORKTREES_CHANGED_EVENT,
      onWorktreesChanged,
    );
  });
});
