// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { TerminalTabBar } from "../TerminalTabBar";

class MockResizeObserver {
  observe = vi.fn();
  unobserve = vi.fn();
  disconnect = vi.fn();
  constructor(_callback: ResizeObserverCallback) {}
}
globalThis.ResizeObserver =
  MockResizeObserver as unknown as typeof ResizeObserver;

const defaultProps = {
  tabs: [
    { id: "tab-1", label: "Tab 1", connectionState: "connected" as const },
    { id: "tab-2", label: "Tab 2", connectionState: "connected" as const },
  ],
  activeTabId: "tab-1",
  onTabChange: vi.fn(),
  onTabClose: vi.fn(),
  onNewTab: vi.fn(),
};

describe("TerminalTabBar - split right", () => {
  it("renders split-right button when onSplitRight is provided", () => {
    render(
      <TerminalTabBar
        {...defaultProps}
        onSplitRight={vi.fn()}
        canSplitRight={true}
      />,
    );

    expect(screen.getByTestId("terminal-split-right")).toBeInTheDocument();
  });

  it("does not render split-right button when onSplitRight is omitted", () => {
    render(<TerminalTabBar {...defaultProps} />);
    expect(
      screen.queryByTestId("terminal-split-right"),
    ).not.toBeInTheDocument();
  });

  it("uses the agent editor split label", () => {
    render(
      <TerminalTabBar
        {...defaultProps}
        onSplitRight={vi.fn()}
        canSplitRight={true}
      />,
    );

    expect(
      screen.getByRole("button", { name: "Split editor right" }),
    ).toBeInTheDocument();
  });

  it("disables split-right when canSplitRight is false", () => {
    render(
      <TerminalTabBar
        {...defaultProps}
        onSplitRight={vi.fn()}
        canSplitRight={false}
      />,
    );

    expect(screen.getByTestId("terminal-split-right")).toBeDisabled();
  });

  it("shows close button for a lone group tab when totalTabCount > 1", () => {
    render(
      <TerminalTabBar
        {...defaultProps}
        tabs={[{ id: "tab-2", label: "Tab 2", connectionState: "connected" }]}
        activeTabId="tab-2"
        totalTabCount={2}
      />,
    );

    expect(screen.getByTestId("terminal-tab-close-tab-2")).toBeInTheDocument();
  });

  it("calls onSplitRight when clicked", () => {
    const onSplitRight = vi.fn();
    render(
      <TerminalTabBar
        {...defaultProps}
        onSplitRight={onSplitRight}
        canSplitRight={true}
      />,
    );

    fireEvent.click(screen.getByTestId("terminal-split-right"));
    expect(onSplitRight).toHaveBeenCalledTimes(1);
  });
});
