/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalTabBar component.
 */

import { render, screen, fireEvent, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import "@testing-library/jest-dom";
import { TerminalTabBar, type TerminalTab } from "../TerminalTabBar";

// Mock ResizeObserver (not available in jsdom)
// Stores callbacks so tests can trigger overflow recalculation
let resizeObserverCallbacks: Array<() => void> = [];

class MockResizeObserver {
  callback: ResizeObserverCallback;
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  constructor(callback: ResizeObserverCallback) {
    this.callback = callback;
    resizeObserverCallbacks.push(() =>
      callback([], this as unknown as ResizeObserver),
    );
  }
}
globalThis.ResizeObserver =
  MockResizeObserver as unknown as typeof ResizeObserver;

function makeTabs(count: number): TerminalTab[] {
  return Array.from({ length: count }, (_, i) => ({
    id: `tab-${i + 1}`,
    label: `Terminal ${i + 1}`,
    connectionState: "connected" as const,
  }));
}

const defaultProps = {
  tabs: makeTabs(3),
  activeTabId: "tab-1",
  onTabChange: vi.fn(),
  onTabClose: vi.fn(),
  onNewTab: vi.fn(),
};

describe("TerminalTabBar", () => {
  describe("rendering", () => {
    it("renders all tabs with correct labels", () => {
      render(<TerminalTabBar {...defaultProps} />);

      expect(screen.getByText("Terminal 1")).toBeInTheDocument();
      expect(screen.getByText("Terminal 2")).toBeInTheDocument();
      expect(screen.getByText("Terminal 3")).toBeInTheDocument();
    });

    it("renders connection state dots with correct data-status", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected" },
        { id: "b", label: "B", connectionState: "connecting" },
        { id: "c", label: "C", connectionState: "disconnected" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(screen.getByTestId("terminal-tab-status-a")).toHaveAttribute(
        "data-status",
        "connected",
      );
      expect(screen.getByTestId("terminal-tab-status-b")).toHaveAttribute(
        "data-status",
        "connecting",
      );
      expect(screen.getByTestId("terminal-tab-status-c")).toHaveAttribute(
        "data-status",
        "disconnected",
      );
    });

    it("marks active tab with aria-selected=true", () => {
      render(<TerminalTabBar {...defaultProps} activeTabId="tab-2" />);

      expect(screen.getByTestId("terminal-tab-tab-1")).toHaveAttribute(
        "aria-selected",
        "false",
      );
      expect(screen.getByTestId("terminal-tab-tab-2")).toHaveAttribute(
        "aria-selected",
        "true",
      );
      expect(screen.getByTestId("terminal-tab-tab-3")).toHaveAttribute(
        "aria-selected",
        "false",
      );
    });

    it("active tab has tabIndex=0, others -1", () => {
      render(<TerminalTabBar {...defaultProps} activeTabId="tab-2" />);

      expect(screen.getByTestId("terminal-tab-tab-1")).toHaveAttribute(
        "tabIndex",
        "-1",
      );
      expect(screen.getByTestId("terminal-tab-tab-2")).toHaveAttribute(
        "tabIndex",
        "0",
      );
      expect(screen.getByTestId("terminal-tab-tab-3")).toHaveAttribute(
        "tabIndex",
        "-1",
      );
    });

    it("hides close button when only 1 tab", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={makeTabs(1)}
          activeTabId="tab-1"
        />,
      );

      expect(
        screen.queryByTestId("terminal-tab-close-tab-1"),
      ).not.toBeInTheDocument();
    });

    it("shows close buttons when multiple tabs", () => {
      render(<TerminalTabBar {...defaultProps} />);

      expect(
        screen.getByTestId("terminal-tab-close-tab-1"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("terminal-tab-close-tab-2"),
      ).toBeInTheDocument();
      expect(
        screen.getByTestId("terminal-tab-close-tab-3"),
      ).toBeInTheDocument();
    });

    it("renders new tab button with correct aria-label", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const button = screen.getByTestId("terminal-new-tab-button");
      expect(button).toBeInTheDocument();
      expect(button).toHaveAttribute("aria-label", "New terminal tab");
    });
  });

  describe("interactions", () => {
    it("clicking a tab calls onTabChange with correct id", () => {
      const onTabChange = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabChange={onTabChange} />);

      fireEvent.click(screen.getByTestId("terminal-tab-tab-2"));

      expect(onTabChange).toHaveBeenCalledTimes(1);
      expect(onTabChange).toHaveBeenCalledWith("tab-2");
    });

    it("clicking close button calls onTabClose with correct id", () => {
      const onTabClose = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabClose={onTabClose} />);

      fireEvent.click(screen.getByTestId("terminal-tab-close-tab-2"));

      expect(onTabClose).toHaveBeenCalledTimes(1);
      expect(onTabClose).toHaveBeenCalledWith("tab-2");
    });

    it("close button click does NOT trigger onTabChange (stopPropagation)", () => {
      const onTabChange = vi.fn();
      const onTabClose = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onTabChange={onTabChange}
          onTabClose={onTabClose}
        />,
      );

      const closeBtn = screen.getByTestId("terminal-tab-close-tab-2");
      const clickEvent = new MouseEvent("click", { bubbles: true });
      const stopSpy = vi.spyOn(clickEvent, "stopPropagation");
      fireEvent(closeBtn, clickEvent);

      expect(onTabClose).toHaveBeenCalledWith("tab-2");
      expect(onTabChange).not.toHaveBeenCalled();
      expect(stopSpy).toHaveBeenCalled();
    });

    it("clicking new tab button calls onNewTab", () => {
      const onNewTab = vi.fn();
      render(<TerminalTabBar {...defaultProps} onNewTab={onNewTab} />);

      fireEvent.click(screen.getByTestId("terminal-new-tab-button"));

      expect(onNewTab).toHaveBeenCalledTimes(1);
    });

    it("opens backend menu when onBackendSelect is provided", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          availableBackends={["claude", "codex"]}
          onBackendSelect={vi.fn()}
        />,
      );

      expect(
        screen.queryByTestId("new-terminal-tab-menu"),
      ).not.toBeInTheDocument();

      fireEvent.click(screen.getByTestId("terminal-new-tab-button"));

      expect(screen.getByTestId("new-terminal-tab-menu")).toBeInTheDocument();
      expect(screen.getByTestId("new-tab-backend-shell")).toBeInTheDocument();
      expect(screen.getByTestId("new-tab-backend-claude")).toBeInTheDocument();
    });

    it("selecting a backend calls onBackendSelect", () => {
      const onBackendSelect = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          availableBackends={["claude", "codex"]}
          onBackendSelect={onBackendSelect}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-new-tab-button"));
      fireEvent.click(screen.getByTestId("new-tab-backend-codex"));

      expect(onBackendSelect).toHaveBeenCalledWith("codex");
      expect(
        screen.queryByTestId("new-terminal-tab-menu"),
      ).not.toBeInTheDocument();
    });
  });

  describe("keyboard navigation", () => {
    it("ArrowRight moves to next tab", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-1"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "ArrowRight" });

      expect(onTabChange).toHaveBeenCalledWith("tab-2");
    });

    it("ArrowLeft moves to previous tab", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-2"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "ArrowLeft" });

      expect(onTabChange).toHaveBeenCalledWith("tab-1");
    });

    it("ArrowRight wraps from last to first", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-3"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "ArrowRight" });

      expect(onTabChange).toHaveBeenCalledWith("tab-1");
    });

    it("ArrowLeft wraps from first to last", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-1"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "ArrowLeft" });

      expect(onTabChange).toHaveBeenCalledWith("tab-3");
    });

    it("Home moves to first tab", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-3"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "Home" });

      expect(onTabChange).toHaveBeenCalledWith("tab-1");
    });

    it("End moves to last tab", () => {
      const onTabChange = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-1"
          onTabChange={onTabChange}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "End" });

      expect(onTabChange).toHaveBeenCalledWith("tab-3");
    });
  });

  describe("accessibility", () => {
    it("has role=tablist with correct aria-label", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      expect(tablist).toBeInTheDocument();
      expect(tablist).toHaveAttribute("aria-label", "Terminal tabs");
    });

    it("each tab has role=tab", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tabs = screen.getAllByRole("tab");
      expect(tabs).toHaveLength(3);
    });

    it("close buttons have aria-label including tab name", () => {
      render(<TerminalTabBar {...defaultProps} />);

      expect(screen.getByTestId("terminal-tab-close-tab-1")).toHaveAttribute(
        "aria-label",
        "Close Terminal 1",
      );
      expect(screen.getByTestId("terminal-tab-close-tab-2")).toHaveAttribute(
        "aria-label",
        "Close Terminal 2",
      );
    });

    it("status dots have aria-label describing connection state", () => {
      const tabs: TerminalTab[] = [
        { id: "x", label: "X", connectionState: "connected" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="x" />);

      expect(screen.getByTestId("terminal-tab-status-x")).toHaveAttribute(
        "aria-label",
        "Connection: connected",
      );
    });
  });

  describe("tab rename", () => {
    it("double-click shows rename input with current label", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      expect(input).toBeInTheDocument();
      expect(input).toHaveValue("Terminal 1");
    });

    it("Enter confirms rename and calls onTabRename", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.change(input, { target: { value: "My Terminal" } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onTabRename).toHaveBeenCalledWith("tab-1", "My Terminal");
      expect(
        screen.queryByTestId("terminal-tab-rename-input-tab-1"),
      ).not.toBeInTheDocument();
    });

    it("Escape cancels rename without calling onTabRename", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.change(input, { target: { value: "Changed" } });
      fireEvent.keyDown(input, { key: "Escape" });

      expect(onTabRename).not.toHaveBeenCalled();
      expect(
        screen.queryByTestId("terminal-tab-rename-input-tab-1"),
      ).not.toBeInTheDocument();
    });

    it("blur confirms rename", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.change(input, { target: { value: "Blurred Name" } });
      fireEvent.blur(input);

      expect(onTabRename).toHaveBeenCalledWith("tab-1", "Blurred Name");
    });

    it("empty label reverts without calling onTabRename", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.change(input, { target: { value: "" } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onTabRename).not.toHaveBeenCalled();
    });

    it("unchanged label skips onTabRename callback", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.keyDown(input, { key: "Enter" });

      expect(onTabRename).not.toHaveBeenCalled();
    });

    it("clicking input does not switch tab", () => {
      const onTabChange = vi.fn();
      const onTabRename = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onTabChange={onTabChange}
          onTabRename={onTabRename}
        />,
      );

      const label = screen.getByTestId("terminal-tab-label-tab-2");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-2");
      onTabChange.mockClear();
      fireEvent.click(input);

      expect(onTabChange).not.toHaveBeenCalled();
    });

    it("without onTabRename prop, double-click does not enter edit mode", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      expect(
        screen.queryByTestId("terminal-tab-rename-input-tab-1"),
      ).not.toBeInTheDocument();
    });

    it("keyboard nav suppressed during edit", () => {
      const onTabChange = vi.fn();
      const onTabRename = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onTabChange={onTabChange}
          onTabRename={onTabRename}
        />,
      );

      const label = screen.getByTestId("terminal-tab-label-tab-1");
      fireEvent.doubleClick(label);

      const input = screen.getByTestId("terminal-tab-rename-input-tab-1");
      onTabChange.mockClear();
      fireEvent.keyDown(input, { key: "ArrowRight" });

      expect(onTabChange).not.toHaveBeenCalled();
    });

    it("only one tab editable at a time", () => {
      const onTabRename = vi.fn();
      render(<TerminalTabBar {...defaultProps} onTabRename={onTabRename} />);

      // Start editing tab-1
      fireEvent.doubleClick(screen.getByTestId("terminal-tab-label-tab-1"));
      expect(
        screen.getByTestId("terminal-tab-rename-input-tab-1"),
      ).toBeInTheDocument();

      // Double-click tab-2 — tab-1 should exit edit mode
      // Note: tab-1 confirmEdit fires on blur when focus moves to tab-2's double-click
      // but since we're using fireEvent, we need to simulate this manually
      const input1 = screen.getByTestId("terminal-tab-rename-input-tab-1");
      fireEvent.blur(input1);

      // Now tab-1's input should be gone (confirmEdit reverts since unchanged)
      expect(
        screen.queryByTestId("terminal-tab-rename-input-tab-1"),
      ).not.toBeInTheDocument();

      // Double-click tab-2
      fireEvent.doubleClick(screen.getByTestId("terminal-tab-label-tab-2"));
      expect(
        screen.getByTestId("terminal-tab-rename-input-tab-2"),
      ).toBeInTheDocument();
    });
  });

  describe("brand color status dots", () => {
    it("status dot sets --brand-color CSS variable when brandColor is provided", () => {
      const tabs: TerminalTab[] = [
        {
          id: "claude-1",
          label: "Claude 1",
          connectionState: "connected",
          brandColor: "#d4a574",
        },
      ];
      render(
        <TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="claude-1" />,
      );

      const dot = screen.getByTestId("terminal-tab-status-claude-1");
      expect(dot).toHaveStyle({ "--brand-color": "#d4a574" });
    });

    it("status dot does not set inline style when brandColor is undefined", () => {
      const tabs: TerminalTab[] = [
        { id: "plain", label: "Plain", connectionState: "connected" },
      ];
      render(
        <TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="plain" />,
      );

      const dot = screen.getByTestId("terminal-tab-status-plain");
      expect(dot.getAttribute("style")).toBeNull();
    });

    it("connected state shows brand color at full opacity (no opacity override)", () => {
      const tabs: TerminalTab[] = [
        {
          id: "codex-1",
          label: "Codex 1",
          connectionState: "connected",
          brandColor: "#10a37f",
        },
      ];
      render(
        <TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="codex-1" />,
      );

      const dot = screen.getByTestId("terminal-tab-status-codex-1");
      expect(dot).toHaveStyle({ "--brand-color": "#10a37f" });
      // CSS handles opacity via data-status; no inline opacity override
      expect(dot.style.opacity).toBe("");
    });

    it("all status dot states still use data-status attribute", () => {
      const tabs: TerminalTab[] = [
        {
          id: "a",
          label: "A",
          connectionState: "connected",
          brandColor: "#d4a574",
        },
        {
          id: "b",
          label: "B",
          connectionState: "connecting",
          brandColor: "#10a37f",
        },
        {
          id: "c",
          label: "C",
          connectionState: "disconnected",
          brandColor: "#6366f1",
        },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(screen.getByTestId("terminal-tab-status-a")).toHaveAttribute(
        "data-status",
        "connected",
      );
      expect(screen.getByTestId("terminal-tab-status-b")).toHaveAttribute(
        "data-status",
        "connecting",
      );
      expect(screen.getByTestId("terminal-tab-status-c")).toHaveAttribute(
        "data-status",
        "disconnected",
      );
    });
  });

  describe("unread indicator", () => {
    it("renders unread dot on inactive tab with hasUnread: true", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected", hasUnread: false },
        { id: "b", label: "B", connectionState: "connected", hasUnread: true },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(screen.getByTestId("terminal-tab-unread-b")).toBeInTheDocument();
      expect(screen.getByTestId("terminal-tab-unread-b")).toHaveAttribute(
        "aria-label",
        "has new output",
      );
    });

    it("does NOT render unread dot on active tab even with hasUnread: true", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected", hasUnread: true },
        { id: "b", label: "B", connectionState: "connected", hasUnread: false },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(
        screen.queryByTestId("terminal-tab-unread-a"),
      ).not.toBeInTheDocument();
    });

    it("does not render unread dot when hasUnread is false", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected", hasUnread: false },
        { id: "b", label: "B", connectionState: "connected", hasUnread: false },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(
        screen.queryByTestId("terminal-tab-unread-a"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("terminal-tab-unread-b"),
      ).not.toBeInTheDocument();
    });

    it("does not render unread dot when hasUnread is undefined", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected" },
        { id: "b", label: "B", connectionState: "connected" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(
        screen.queryByTestId("terminal-tab-unread-a"),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByTestId("terminal-tab-unread-b"),
      ).not.toBeInTheDocument();
    });

    it("renders unread dot on multiple inactive tabs simultaneously", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected", hasUnread: true },
        { id: "b", label: "B", connectionState: "connected", hasUnread: true },
        { id: "c", label: "C", connectionState: "connected", hasUnread: true },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      // Active tab should NOT show unread
      expect(
        screen.queryByTestId("terminal-tab-unread-a"),
      ).not.toBeInTheDocument();
      // Inactive tabs should show unread
      expect(screen.getByTestId("terminal-tab-unread-b")).toBeInTheDocument();
      expect(screen.getByTestId("terminal-tab-unread-c")).toBeInTheDocument();
    });
  });

  describe("edge cases", () => {
    it("renders just the new-tab button when tabs array is empty", () => {
      render(<TerminalTabBar {...defaultProps} tabs={[]} activeTabId="" />);

      expect(screen.queryAllByRole("tab")).toHaveLength(0);
      expect(screen.getByTestId("terminal-new-tab-button")).toBeInTheDocument();
    });

    it("applies active class to current tab", () => {
      render(<TerminalTabBar {...defaultProps} activeTabId="tab-2" />);

      const tab2 = screen.getByTestId("terminal-tab-tab-2");
      const tab1 = screen.getByTestId("terminal-tab-tab-1");

      expect(tab2.className).toMatch(/active/);
      expect(tab1.className).not.toMatch(/active/);
    });
  });

  describe("context menu", () => {
    const contextMenuProps = {
      ...defaultProps,
      onTabRename: vi.fn(),
      onDuplicateTab: vi.fn(),
      maxTabsReached: false,
    };

    it("right-click on a tab opens context menu with Duplicate, Rename, Close options", () => {
      render(<TerminalTabBar {...contextMenuProps} />);

      const tab = screen.getByTestId("terminal-tab-tab-2");
      fireEvent.contextMenu(tab);

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-duplicate")).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-rename")).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-close")).toBeInTheDocument();
    });

    it('context menu "Duplicate" calls onDuplicateTab with correct tabId', () => {
      const onDuplicateTab = vi.fn();
      render(
        <TerminalTabBar
          {...contextMenuProps}
          onDuplicateTab={onDuplicateTab}
        />,
      );

      const tab = screen.getByTestId("terminal-tab-tab-2");
      fireEvent.contextMenu(tab);

      fireEvent.click(screen.getByTestId("context-menu-duplicate"));

      expect(onDuplicateTab).toHaveBeenCalledTimes(1);
      expect(onDuplicateTab).toHaveBeenCalledWith("tab-2");
    });

    it('context menu "Rename" enters edit mode for the tab', () => {
      const onTabRename = vi.fn();
      render(
        <TerminalTabBar {...contextMenuProps} onTabRename={onTabRename} />,
      );

      const tab = screen.getByTestId("terminal-tab-tab-2");
      fireEvent.contextMenu(tab);

      fireEvent.click(screen.getByTestId("context-menu-rename"));

      // Context menu should close
      expect(
        screen.queryByTestId("terminal-tab-context-menu"),
      ).not.toBeInTheDocument();

      // Rename input should appear for tab-2
      expect(
        screen.getByTestId("terminal-tab-rename-input-tab-2"),
      ).toBeInTheDocument();
    });

    it('context menu "Close" calls onTabClose with correct tabId', () => {
      const onTabClose = vi.fn();
      render(<TerminalTabBar {...contextMenuProps} onTabClose={onTabClose} />);

      const tab = screen.getByTestId("terminal-tab-tab-2");
      fireEvent.contextMenu(tab);

      fireEvent.click(screen.getByTestId("context-menu-close"));

      expect(onTabClose).toHaveBeenCalledTimes(1);
      expect(onTabClose).toHaveBeenCalledWith("tab-2");
    });

    it('context menu "Duplicate" is disabled when maxTabsReached=true', () => {
      render(<TerminalTabBar {...contextMenuProps} maxTabsReached={true} />);

      const tab = screen.getByTestId("terminal-tab-tab-1");
      fireEvent.contextMenu(tab);

      const duplicateBtn = screen.getByTestId("context-menu-duplicate");
      expect(duplicateBtn).toBeDisabled();
    });

    it("context menu closes when clicking outside", () => {
      render(<TerminalTabBar {...contextMenuProps} />);

      const tab = screen.getByTestId("terminal-tab-tab-1");
      fireEvent.contextMenu(tab);

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();

      // Click outside the context menu
      fireEvent.mouseDown(document.body);

      expect(
        screen.queryByTestId("terminal-tab-context-menu"),
      ).not.toBeInTheDocument();
    });

    it("context menu closes on Escape key", () => {
      render(<TerminalTabBar {...contextMenuProps} />);

      const tab = screen.getByTestId("terminal-tab-tab-1");
      fireEvent.contextMenu(tab);

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();

      fireEvent.keyDown(document, { key: "Escape" });

      expect(
        screen.queryByTestId("terminal-tab-context-menu"),
      ).not.toBeInTheDocument();
    });

    it('context menu "Close" is not shown when only 1 tab', () => {
      render(
        <TerminalTabBar
          {...contextMenuProps}
          tabs={makeTabs(1)}
          activeTabId="tab-1"
        />,
      );

      const tab = screen.getByTestId("terminal-tab-tab-1");
      fireEvent.contextMenu(tab);

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();
      expect(
        screen.queryByTestId("context-menu-close"),
      ).not.toBeInTheDocument();
    });

    it("Shift+F10 keyboard shortcut opens context menu", () => {
      render(<TerminalTabBar {...contextMenuProps} />);

      const tab = screen.getByTestId("terminal-tab-tab-2");
      fireEvent.keyDown(tab, { key: "F10", shiftKey: true });

      expect(
        screen.getByTestId("terminal-tab-context-menu"),
      ).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-duplicate")).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-rename")).toBeInTheDocument();
      expect(screen.getByTestId("context-menu-close")).toBeInTheDocument();
    });
  });

  describe("overflow scroll buttons", () => {
    /**
     * Helper: simulates overflow by patching scrollWidth / clientWidth / scrollLeft
     * on the tablist element, then triggering the ResizeObserver callback.
     */
    function simulateOverflow(
      tablist: HTMLElement,
      overrides: {
        scrollWidth?: number;
        clientWidth?: number;
        scrollLeft?: number;
      },
    ) {
      Object.defineProperty(tablist, "scrollWidth", {
        value: overrides.scrollWidth ?? 1000,
        configurable: true,
      });
      Object.defineProperty(tablist, "clientWidth", {
        value: overrides.clientWidth ?? 400,
        configurable: true,
      });
      Object.defineProperty(tablist, "scrollLeft", {
        value: overrides.scrollLeft ?? 0,
        configurable: true,
      });
    }

    beforeEach(() => {
      resizeObserverCallbacks = [];
    });

    it("does not show scroll buttons when tabs fit in the container", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 400,
        clientWidth: 400,
        scrollLeft: 0,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.queryByTestId("scroll-tabs-left")).not.toBeInTheDocument();
      expect(screen.queryByTestId("scroll-tabs-right")).not.toBeInTheDocument();
    });

    it("shows right scroll button when content overflows to the right", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 800,
        clientWidth: 400,
        scrollLeft: 0,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.queryByTestId("scroll-tabs-left")).not.toBeInTheDocument();
      expect(screen.getByTestId("scroll-tabs-right")).toBeInTheDocument();
    });

    it("shows left scroll button when scrolled past the start", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      // scrollLeft=400 + clientWidth=400 = 800 = scrollWidth => no right overflow
      simulateOverflow(tablist, {
        scrollWidth: 800,
        clientWidth: 400,
        scrollLeft: 400,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.getByTestId("scroll-tabs-left")).toBeInTheDocument();
      expect(screen.queryByTestId("scroll-tabs-right")).not.toBeInTheDocument();
    });

    it("shows both scroll buttons when scrolled in the middle", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.getByTestId("scroll-tabs-left")).toBeInTheDocument();
      expect(screen.getByTestId("scroll-tabs-right")).toBeInTheDocument();
    });

    it("left scroll button has correct aria-label", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.getByTestId("scroll-tabs-left")).toHaveAttribute(
        "aria-label",
        "Scroll tabs left",
      );
    });

    it("right scroll button has correct aria-label", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.getByTestId("scroll-tabs-right")).toHaveAttribute(
        "aria-label",
        "Scroll tabs right",
      );
    });

    it("clicking left scroll button calls scrollBy with negative offset", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });
      const scrollBySpy = vi.fn();
      tablist.scrollBy = scrollBySpy;

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      fireEvent.click(screen.getByTestId("scroll-tabs-left"));

      expect(scrollBySpy).toHaveBeenCalledWith({
        left: -150,
        behavior: "smooth",
      });
    });

    it("clicking right scroll button calls scrollBy with positive offset", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });
      const scrollBySpy = vi.fn();
      tablist.scrollBy = scrollBySpy;

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      fireEvent.click(screen.getByTestId("scroll-tabs-right"));

      expect(scrollBySpy).toHaveBeenCalledWith({
        left: 150,
        behavior: "smooth",
      });
    });

    it("scroll buttons have tabIndex=-1 (not focusable in tab order)", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      simulateOverflow(tablist, {
        scrollWidth: 1200,
        clientWidth: 400,
        scrollLeft: 200,
      });

      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });

      expect(screen.getByTestId("scroll-tabs-left")).toHaveAttribute(
        "tabIndex",
        "-1",
      );
      expect(screen.getByTestId("scroll-tabs-right")).toHaveAttribute(
        "tabIndex",
        "-1",
      );
    });

    it("overflow updates on scroll events", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");

      // Initially no overflow
      simulateOverflow(tablist, {
        scrollWidth: 400,
        clientWidth: 400,
        scrollLeft: 0,
      });
      act(() => {
        resizeObserverCallbacks.forEach((cb) => cb());
      });
      expect(screen.queryByTestId("scroll-tabs-right")).not.toBeInTheDocument();

      // Simulate scrollWidth change and fire scroll event
      simulateOverflow(tablist, {
        scrollWidth: 800,
        clientWidth: 400,
        scrollLeft: 0,
      });
      act(() => {
        fireEvent.scroll(tablist);
      });

      expect(screen.getByTestId("scroll-tabs-right")).toBeInTheDocument();
    });
  });

  describe("auto-scroll new tab", () => {
    let rafCallback: FrameRequestCallback | null = null;

    beforeEach(() => {
      rafCallback = null;
      resizeObserverCallbacks = [];
      vi.spyOn(window, "requestAnimationFrame").mockImplementation((cb) => {
        rafCallback = cb;
        return 1;
      });
    });

    afterEach(() => {
      vi.restoreAllMocks();
    });

    it("scrolls to end when a new tab is added", () => {
      const initialTabs = makeTabs(3);
      const { rerender } = render(
        <TerminalTabBar {...defaultProps} tabs={initialTabs} />,
      );

      const tablist = screen.getByRole("tablist");
      const scrollToSpy = vi.fn();
      tablist.scrollTo = scrollToSpy;
      Object.defineProperty(tablist, "scrollWidth", {
        value: 1000,
        configurable: true,
      });

      // Add a 4th tab
      const newTabs = makeTabs(4);
      rerender(<TerminalTabBar {...defaultProps} tabs={newTabs} />);

      // The effect schedules a rAF — flush it
      expect(rafCallback).not.toBeNull();
      act(() => {
        rafCallback!(0);
      });

      expect(scrollToSpy).toHaveBeenCalledWith({
        left: 1000,
        behavior: "smooth",
      });
    });

    it("does NOT auto-scroll when a tab is removed (count decreases)", () => {
      const initialTabs = makeTabs(4);
      const { rerender } = render(
        <TerminalTabBar {...defaultProps} tabs={initialTabs} />,
      );

      const tablist = screen.getByRole("tablist");
      const scrollToSpy = vi.fn();
      tablist.scrollTo = scrollToSpy;

      // Remove a tab (4 -> 3)
      const fewerTabs = makeTabs(3);
      rerender(<TerminalTabBar {...defaultProps} tabs={fewerTabs} />);

      // rAF should not have been called for scroll
      if (rafCallback) {
        act(() => {
          rafCallback!(0);
        });
      }

      expect(scrollToSpy).not.toHaveBeenCalled();
    });
  });

  describe("crashed status dot", () => {
    it("renders status dot with data-status='crashed' for crashed tab", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected" },
        { id: "b", label: "B", connectionState: "crashed" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(screen.getByTestId("terminal-tab-status-b")).toHaveAttribute(
        "data-status",
        "crashed",
      );
    });

    it("crashed tab still renders alongside other connection states", () => {
      const tabs: TerminalTab[] = [
        { id: "a", label: "A", connectionState: "connected" },
        { id: "b", label: "B", connectionState: "crashed" },
        { id: "c", label: "C", connectionState: "disconnected" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="a" />);

      expect(screen.getByTestId("terminal-tab-status-a")).toHaveAttribute(
        "data-status",
        "connected",
      );
      expect(screen.getByTestId("terminal-tab-status-b")).toHaveAttribute(
        "data-status",
        "crashed",
      );
      expect(screen.getByTestId("terminal-tab-status-c")).toHaveAttribute(
        "data-status",
        "disconnected",
      );
    });

    it("crashed status dot has correct aria-label", () => {
      const tabs: TerminalTab[] = [
        { id: "x", label: "X", connectionState: "crashed" },
      ];
      render(<TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="x" />);

      expect(screen.getByTestId("terminal-tab-status-x")).toHaveAttribute(
        "aria-label",
        "Connection: crashed",
      );
    });
  });

  describe("keyboard: Delete/Backspace closes tab", () => {
    it("Delete key on tablist calls onTabClose with active tab id", () => {
      const onTabClose = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-2"
          onTabClose={onTabClose}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "Delete" });

      expect(onTabClose).toHaveBeenCalledTimes(1);
      expect(onTabClose).toHaveBeenCalledWith("tab-2");
    });

    it("Backspace key on tablist calls onTabClose with active tab id", () => {
      const onTabClose = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          activeTabId="tab-3"
          onTabClose={onTabClose}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "Backspace" });

      expect(onTabClose).toHaveBeenCalledTimes(1);
      expect(onTabClose).toHaveBeenCalledWith("tab-3");
    });

    it("Delete does NOT close when only one tab remains", () => {
      const onTabClose = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={makeTabs(1)}
          activeTabId="tab-1"
          onTabClose={onTabClose}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "Delete" });

      expect(onTabClose).not.toHaveBeenCalled();
    });

    it("Backspace does NOT close when only one tab remains", () => {
      const onTabClose = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={makeTabs(1)}
          activeTabId="tab-1"
          onTabClose={onTabClose}
        />,
      );

      const tablist = screen.getByRole("tablist");
      fireEvent.keyDown(tablist, { key: "Backspace" });

      expect(onTabClose).not.toHaveBeenCalled();
    });
  });

  describe("accessibility: aria-keyshortcuts and id attributes", () => {
    it("tablist has aria-keyshortcuts attribute listing keyboard shortcuts", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tablist = screen.getByRole("tablist");
      const shortcuts = tablist.getAttribute("aria-keyshortcuts");
      expect(shortcuts).toBeTruthy();
      expect(shortcuts).toContain("Meta+1");
      expect(shortcuts).toContain("Meta+9");
      expect(shortcuts).toContain("Meta+T");
      expect(shortcuts).toContain("Meta+W");
    });

    it("each tab element has an id attribute of terminal-tab-{id}", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const tabs = screen.getAllByRole("tab");
      expect(tabs[0]).toHaveAttribute("id", "terminal-tab-tab-1");
      expect(tabs[1]).toHaveAttribute("id", "terminal-tab-tab-2");
      expect(tabs[2]).toHaveAttribute("id", "terminal-tab-tab-3");
    });
  });

  describe("restart marker", () => {
    it("renders a marker for a tab with replacedAt and none without", () => {
      const tabs = makeTabs(2);
      tabs[0] = { ...tabs[0]!, replacedAt: "2026-08-15T10:00:00Z" };
      render(<TerminalTabBar {...defaultProps} tabs={tabs} />);

      expect(
        screen.getByTestId("tab-marker-restart-tab-1"),
      ).toBeInTheDocument();
      expect(screen.queryByTestId("tab-marker-restart-tab-2")).toBeNull();
    });

    it("renders the marker separately from the connection dot", () => {
      const tabs = makeTabs(1);
      tabs[0] = { ...tabs[0]!, replacedAt: "2026-08-15T10:00:00Z" };
      render(<TerminalTabBar {...defaultProps} tabs={tabs} />);

      // A replaced tab can still be connected — the two are independent.
      expect(screen.getByTestId("terminal-tab-status-tab-1")).toHaveAttribute(
        "data-status",
        "connected",
      );
      expect(
        screen.getByTestId("tab-marker-restart-tab-1"),
      ).toBeInTheDocument();
    });

    it("clicking the marker dismisses the notice for that tab", () => {
      const onDismissRestartNotice = vi.fn();
      const onTabChange = vi.fn();
      const tabs = makeTabs(2);
      tabs[1] = { ...tabs[1]!, replacedAt: "2026-08-15T10:00:00Z" };
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={tabs}
          onTabChange={onTabChange}
          onDismissRestartNotice={onDismissRestartNotice}
        />,
      );

      fireEvent.click(screen.getByTestId("tab-marker-restart-tab-2"));

      expect(onDismissRestartNotice).toHaveBeenCalledWith("tab-2");
      // The click must not also select the tab underneath.
      expect(onTabChange).not.toHaveBeenCalled();
    });

    it("offers the context-menu entry only for a tab with a marker", () => {
      const onDismissRestartNotice = vi.fn();
      const tabs = makeTabs(2);
      tabs[0] = { ...tabs[0]!, replacedAt: "2026-08-15T10:00:00Z" };
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={tabs}
          onDismissRestartNotice={onDismissRestartNotice}
        />,
      );

      fireEvent.contextMenu(screen.getByTestId("terminal-tab-tab-2"));
      expect(
        screen.queryByTestId("context-menu-dismiss-restart-notice"),
      ).toBeNull();
      fireEvent.keyDown(document, { key: "Escape" });

      fireEvent.contextMenu(screen.getByTestId("terminal-tab-tab-1"));
      fireEvent.click(
        screen.getByTestId("context-menu-dismiss-restart-notice"),
      );
      expect(onDismissRestartNotice).toHaveBeenCalledWith("tab-1");
    });
  });

  describe("forwardRef support", () => {
    it("forwards ref to the outer container div", () => {
      const ref = { current: null as HTMLDivElement | null };
      render(<TerminalTabBar ref={ref} {...defaultProps} />);

      expect(ref.current).toBeInstanceOf(HTMLDivElement);
      expect(ref.current).toBe(screen.getByTestId("terminal-tab-bar"));
    });

    it("callback ref receives the outer container element", () => {
      let refValue: HTMLDivElement | null = null;
      const callbackRef = (el: HTMLDivElement | null) => {
        refValue = el;
      };
      render(<TerminalTabBar ref={callbackRef} {...defaultProps} />);

      expect(refValue).toBeInstanceOf(HTMLDivElement);
      expect(refValue).toBe(screen.getByTestId("terminal-tab-bar"));
    });
  });
});
