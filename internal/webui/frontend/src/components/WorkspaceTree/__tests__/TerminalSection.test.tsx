/**
 * @vitest-environment jsdom
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import "@testing-library/jest-dom";

import { TerminalSection } from "../TerminalSection";
import {
  DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
  publishTerminalSidebarState,
  TERMINAL_SIDEBAR_NEW_TAB_EVENT,
  TERMINAL_SIDEBAR_SELECT_EVENT,
} from "@/utils/terminalSidebarBridge";

describe("TerminalSection", () => {
  it("renders synced terminal tabs and highlights the active one", () => {
    render(<TerminalSection />);

    act(() => {
      publishTerminalSidebarState({
        groups: [
          {
            id: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
            label: "Workspace",
            isDefault: true,
            members: [],
          },
        ],
        tabs: [
          {
            id: "lead-codex-1",
            label: "lead-codex-1",
            backendName: "codex",
            connectionState: "connected",
            groupId: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
          },
          {
            id: "lead-claude-1",
            label: "lead-claude-1",
            backendName: "claude",
            connectionState: "disconnected",
            groupId: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
          },
        ],
        activeTabId: "lead-claude-1",
        activeGroupId: DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
      });
    });

    const active = screen.getByTestId("sidebar-terminal-lead-claude-1");
    expect(active).toHaveAttribute("data-selected", "true");
    expect(active).toHaveAttribute("aria-current", "page");
    expect(
      screen.getByTestId("sidebar-terminal-lead-codex-1"),
    ).not.toHaveAttribute("data-selected");
  });

  it("dispatches select and new-tab events from sidebar actions", () => {
    const onSelect = vi.fn();
    const onNew = vi.fn();
    window.addEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
    window.addEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNew);

    render(<TerminalSection />);
    act(() => {
      publishTerminalSidebarState({
        groups: [
          {
            id: "group-1",
            label: "Feature",
            members: [],
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
      });
    });

    fireEvent.click(screen.getByTestId("sidebar-terminal-lead-codex-1"));
    fireEvent.click(screen.getByTestId("sidebar-new-terminal"));

    expect(onSelect).toHaveBeenCalled();
    expect(onNew).toHaveBeenCalledWith(
      expect.objectContaining({ detail: { groupId: "group-1" } }),
    );

    window.removeEventListener(TERMINAL_SIDEBAR_SELECT_EVENT, onSelect);
    window.removeEventListener(TERMINAL_SIDEBAR_NEW_TAB_EVENT, onNew);
  });

  it("shows an empty hint before any sync arrives", () => {
    render(<TerminalSection />);
    expect(screen.getByText("No terminal sessions")).toBeInTheDocument();
  });
});
