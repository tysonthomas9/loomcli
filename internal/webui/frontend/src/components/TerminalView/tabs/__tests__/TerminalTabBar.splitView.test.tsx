/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for split view toggle button in TerminalTabBar.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { TerminalTabBar, type TerminalTab } from "../TerminalTabBar";

// Mock ResizeObserver (not available in jsdom)
class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  constructor(_callback: ResizeObserverCallback) {}
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
  onToggleFullHeight: vi.fn(),
  isFullHeight: false,
};

describe("TerminalTabBar - split view toggle", () => {
  describe("rendering", () => {
    it("renders split toggle button when onToggleSplit is provided", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={false}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).toBeInTheDocument();
    });

    it("does not render split toggle button when onToggleSplit is not provided", () => {
      render(<TerminalTabBar {...defaultProps} />);

      expect(
        screen.queryByTestId("terminal-split-toggle"),
      ).not.toBeInTheDocument();
    });

    it("has correct aria-label", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={false}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).toHaveAttribute("aria-label", "Toggle split view");
    });
  });

  describe("disabled state", () => {
    it("is disabled when canSplit is false", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          tabs={makeTabs(1)}
          activeTabId="tab-1"
          onToggleSplit={vi.fn()}
          canSplit={false}
          isSplitView={false}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).toBeDisabled();
    });

    it("is enabled when canSplit is true", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={false}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).not.toBeDisabled();
    });
  });

  describe("aria-pressed state", () => {
    it("has aria-pressed=false when split view is disabled", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={false}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).toHaveAttribute("aria-pressed", "false");
    });

    it("has aria-pressed=true when split view is enabled", () => {
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={true}
        />,
      );

      const toggle = screen.getByTestId("terminal-split-toggle");
      expect(toggle).toHaveAttribute("aria-pressed", "true");
    });

    it("updates aria-pressed when isSplitView prop changes", () => {
      const { rerender } = render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={false}
        />,
      );

      expect(screen.getByTestId("terminal-split-toggle")).toHaveAttribute(
        "aria-pressed",
        "false",
      );

      rerender(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={vi.fn()}
          canSplit={true}
          isSplitView={true}
        />,
      );

      expect(screen.getByTestId("terminal-split-toggle")).toHaveAttribute(
        "aria-pressed",
        "true",
      );
    });
  });

  describe("interactions", () => {
    it("calls onToggleSplit when clicked", () => {
      const onToggleSplit = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={onToggleSplit}
          canSplit={true}
          isSplitView={false}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-split-toggle"));

      expect(onToggleSplit).toHaveBeenCalledTimes(1);
    });

    it("does not call onToggleSplit when disabled and clicked", () => {
      const onToggleSplit = vi.fn();
      render(
        <TerminalTabBar
          {...defaultProps}
          onToggleSplit={onToggleSplit}
          canSplit={false}
          isSplitView={false}
        />,
      );

      fireEvent.click(screen.getByTestId("terminal-split-toggle"));

      expect(onToggleSplit).not.toHaveBeenCalled();
    });
  });
});
