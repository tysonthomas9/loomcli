/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TerminalPaneArea component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";
import React, { createRef } from "react";

import "@testing-library/jest-dom";
import { TerminalPaneArea } from "../TerminalPaneArea";
import type { TabState } from "../terminalTabUtils";

// ── Mock child components ──────────────────────────────────────────────────

vi.mock("../SplitDivider", () => ({
  SplitDivider: vi.fn(() => (
    <div data-testid="split-divider">SplitDivider</div>
  )),
}));

vi.mock("../SplitPaneSelector", () => ({
  SplitPaneSelector: vi.fn((props: { rightPaneTabId: string }) => (
    <div data-testid="split-pane-selector">
      SplitPaneSelector: {props.rightPaneTabId}
    </div>
  )),
}));

// ── Mock CSS modules ───────────────────────────────────────────────────────

vi.mock("../TerminalView.module.css", () => ({
  default: {
    terminalsContainer: "terminalsContainer",
    terminalPane: "terminalPane",
    terminalPaneSplit: "terminalPaneSplit",
    splitContainer: "splitContainer",
    splitPaneLeft: "splitPaneLeft",
    splitPaneRight: "splitPaneRight",
  },
}));

// ── Helpers ────────────────────────────────────────────────────────────────

function makeTab(id: string, label?: string): TabState {
  return {
    id,
    label: label ?? id,
    sessionName: id,
    connectionState: "connected",
    backendName: "claude",
  };
}

function defaultProps(
  overrides: Partial<React.ComponentProps<typeof TerminalPaneArea>> = {},
) {
  return {
    tabs: [makeTab("tab-1", "Terminal 1"), makeTab("tab-2", "Terminal 2")],
    activeTabId: "tab-1",
    isSplitView: false,
    rightPaneTabId: "",
    splitRatio: 0.5,
    splitContainerRef: createRef<HTMLDivElement>(),
    onSplitRatioChange: vi.fn(),
    onRightPaneTabChange: vi.fn(),
    renderPane: vi.fn((tab: TabState, pane: "left" | "right" | null) => (
      <div data-testid={`pane-${tab.id}-${pane}`}>
        Pane: {tab.id} ({String(pane)})
      </div>
    )),
    ...overrides,
  };
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("TerminalPaneArea", () => {
  // 1. Non-split: renders all tabs, shows only active
  it("non-split: renders all tabs but only shows the active one", () => {
    const props = defaultProps();
    const { container } = render(<TerminalPaneArea {...props} />);

    // Both tab panels should be rendered
    const panels = container.querySelectorAll("[role='tabpanel']");
    expect(panels).toHaveLength(2);

    // Active tab panel should be visible (display: flex)
    const activePanel = container.querySelector("#terminal-panel-tab-1");
    expect(activePanel).toHaveStyle({ display: "flex" });

    // Inactive tab panel should be hidden (display: none)
    const inactivePanel = container.querySelector("#terminal-panel-tab-2");
    expect(inactivePanel).toHaveStyle({ display: "none" });
  });

  // 2. Non-split: calls renderPane with (tab, null)
  it("non-split: calls renderPane with (tab, null)", () => {
    const renderPane = vi.fn((tab: TabState, pane: "left" | "right" | null) => (
      <div data-testid={`pane-${tab.id}-${pane}`}>content</div>
    ));

    render(<TerminalPaneArea {...defaultProps({ renderPane })} />);

    expect(renderPane).toHaveBeenCalledTimes(2);
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-1" }),
      null,
    );
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-2" }),
      null,
    );
  });

  // 3. Split: renders left and right panes with SplitDivider
  it("split: renders left and right panes with SplitDivider", () => {
    render(
      <TerminalPaneArea
        {...defaultProps({
          isSplitView: true,
          rightPaneTabId: "tab-2",
        })}
      />,
    );

    expect(screen.getByTestId("split-container")).toBeInTheDocument();
    expect(screen.getByTestId("split-divider")).toBeInTheDocument();
    expect(screen.getByTestId("split-pane-selector")).toBeInTheDocument();
  });

  // 4. Split: left pane shows active tab, right pane shows rightPaneTabId
  it("split: left pane shows active tab, right pane shows rightPaneTabId", () => {
    const { container } = render(
      <TerminalPaneArea
        {...defaultProps({
          isSplitView: true,
          activeTabId: "tab-1",
          rightPaneTabId: "tab-2",
        })}
      />,
    );

    // Left pane: active tab (tab-1) visible, tab-2 hidden
    const leftActivePanel = container.querySelector("#terminal-panel-tab-1");
    expect(leftActivePanel).toHaveStyle({ display: "flex" });

    const leftInactivePanel = container.querySelector("#terminal-panel-tab-2");
    expect(leftInactivePanel).toHaveStyle({ display: "none" });

    // Right pane: tab-2 visible, tab-1 hidden
    const rightActivePanel = container.querySelector(
      "#terminal-panel-right-tab-2",
    );
    expect(rightActivePanel).toHaveStyle({ display: "flex" });

    const rightInactivePanel = container.querySelector(
      "#terminal-panel-right-tab-1",
    );
    expect(rightInactivePanel).toHaveStyle({ display: "none" });
  });

  it("split: calls renderPane with left and right pane arguments", () => {
    const renderPane = vi.fn((tab: TabState, pane: "left" | "right" | null) => (
      <div data-testid={`pane-${tab.id}-${pane}`}>content</div>
    ));

    render(
      <TerminalPaneArea
        {...defaultProps({
          isSplitView: true,
          rightPaneTabId: "tab-2",
          renderPane,
        })}
      />,
    );

    // In split mode, each tab is rendered in both left and right panes
    // Left pane: 2 tabs, Right pane: 2 tabs = 4 calls
    expect(renderPane).toHaveBeenCalledTimes(4);

    // Check left pane calls
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-1" }),
      "left",
    );
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-2" }),
      "left",
    );

    // Check right pane calls
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-1" }),
      "right",
    );
    expect(renderPane).toHaveBeenCalledWith(
      expect.objectContaining({ id: "tab-2" }),
      "right",
    );
  });

  // 5. Accessibility: tabpanel roles and ids present
  it("non-split: tabpanel roles and aria-labelledby present", () => {
    const { container } = render(<TerminalPaneArea {...defaultProps()} />);

    const panels = container.querySelectorAll("[role='tabpanel']");
    expect(panels).toHaveLength(2);

    const panel1 = container.querySelector("#terminal-panel-tab-1");
    expect(panel1).toHaveAttribute("role", "tabpanel");
    expect(panel1).toHaveAttribute("aria-labelledby", "terminal-tab-tab-1");

    const panel2 = container.querySelector("#terminal-panel-tab-2");
    expect(panel2).toHaveAttribute("role", "tabpanel");
    expect(panel2).toHaveAttribute("aria-labelledby", "terminal-tab-tab-2");
  });

  it("split: tabpanel roles and ids present on both panes", () => {
    const { container } = render(
      <TerminalPaneArea
        {...defaultProps({
          isSplitView: true,
          rightPaneTabId: "tab-2",
        })}
      />,
    );

    // Left pane panels
    const leftPanel = container.querySelector("#terminal-panel-tab-1");
    expect(leftPanel).toHaveAttribute("role", "tabpanel");
    expect(leftPanel).toHaveAttribute("aria-labelledby", "terminal-tab-tab-1");

    // Right pane panels
    const rightPanel = container.querySelector("#terminal-panel-right-tab-2");
    expect(rightPanel).toHaveAttribute("role", "tabpanel");
  });

  it("non-split: does not render SplitDivider or SplitPaneSelector", () => {
    render(<TerminalPaneArea {...defaultProps()} />);

    expect(screen.queryByTestId("split-divider")).not.toBeInTheDocument();
    expect(screen.queryByTestId("split-pane-selector")).not.toBeInTheDocument();
  });
});
