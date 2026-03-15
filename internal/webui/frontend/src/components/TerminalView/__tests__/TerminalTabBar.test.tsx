/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalTabBar component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { TerminalTabBar, type TerminalTab } from "../TerminalTabBar";

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
  onToggleFullHeight: vi.fn(),
  isFullHeight: false,
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

    it("renders full-height toggle button", () => {
      render(<TerminalTabBar {...defaultProps} />);

      const toggle = screen.getByTestId("terminal-fullheight-toggle");
      expect(toggle).toBeInTheDocument();
      expect(toggle).toHaveAttribute("aria-label", "Toggle full height");
    });

    it("full-height toggle shows aria-pressed based on isFullHeight", () => {
      const { rerender } = render(
        <TerminalTabBar {...defaultProps} isFullHeight={false} />,
      );

      expect(screen.getByTestId("terminal-fullheight-toggle")).toHaveAttribute(
        "aria-pressed",
        "false",
      );

      rerender(<TerminalTabBar {...defaultProps} isFullHeight={true} />);

      expect(screen.getByTestId("terminal-fullheight-toggle")).toHaveAttribute(
        "aria-pressed",
        "true",
      );
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

    it("clicking full-height toggle calls onToggleFullHeight", () => {
      const onToggleFullHeight = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleFullHeight={onToggleFullHeight}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-fullheight-toggle"));

      expect(onToggleFullHeight).toHaveBeenCalledTimes(1);
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

    it("aria-pressed on full-height toggle matches isFullHeight", () => {
      render(<TerminalTabBar {...defaultProps} isFullHeight={true} />);

      expect(screen.getByTestId("terminal-fullheight-toggle")).toHaveAttribute(
        "aria-pressed",
        "true",
      );
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
          brandColor: "#D97706",
        },
      ];
      render(
        <TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="claude-1" />,
      );

      const dot = screen.getByTestId("terminal-tab-status-claude-1");
      expect(dot).toHaveStyle({ "--brand-color": "#D97706" });
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
          brandColor: "#22c55e",
        },
      ];
      render(
        <TerminalTabBar {...defaultProps} tabs={tabs} activeTabId="codex-1" />,
      );

      const dot = screen.getByTestId("terminal-tab-status-codex-1");
      expect(dot).toHaveStyle({ "--brand-color": "#22c55e" });
      // CSS handles opacity via data-status; no inline opacity override
      expect(dot.style.opacity).toBe("");
    });

    it("all status dot states still use data-status attribute", () => {
      const tabs: TerminalTab[] = [
        {
          id: "a",
          label: "A",
          connectionState: "connected",
          brandColor: "#D97706",
        },
        {
          id: "b",
          label: "B",
          connectionState: "connecting",
          brandColor: "#22c55e",
        },
        {
          id: "c",
          label: "C",
          connectionState: "disconnected",
          brandColor: "#3B82F6",
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
});
