/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SplitPaneSelector component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi } from "vitest";

import "@testing-library/jest-dom";
import { SplitPaneSelector } from "../SplitPaneSelector";

const defaultTabs = [
  { id: "tab-1", label: "Terminal 1" },
  { id: "tab-2", label: "Terminal 2" },
  { id: "tab-3", label: "Terminal 3" },
];

describe("SplitPaneSelector", () => {
  describe("rendering", () => {
    it("renders with correct test id", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-2"
          onTabChange={vi.fn()}
        />,
      );

      expect(screen.getByTestId("split-pane-selector")).toBeInTheDocument();
    });

    it("renders a select element with correct aria-label", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-2"
          onTabChange={vi.fn()}
        />,
      );

      const select = screen.getByRole("combobox", { name: "Right pane tab" });
      expect(select).toBeInTheDocument();
    });

    it("shows the right pane tab as selected value", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-3"
          onTabChange={vi.fn()}
        />,
      );

      const select = screen.getByRole("combobox") as HTMLSelectElement;
      expect(select.value).toBe("tab-3");
    });
  });

  describe("filtering", () => {
    it("filters out the active left tab from options", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-2"
          onTabChange={vi.fn()}
        />,
      );

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(2);
      expect(options[0]).toHaveTextContent("Terminal 2");
      expect(options[1]).toHaveTextContent("Terminal 3");
    });

    it("does not show the left pane tab as an option", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-2"
          rightPaneTabId="tab-1"
          onTabChange={vi.fn()}
        />,
      );

      const options = screen.getAllByRole("option");
      const optionValues = options.map((o) => (o as HTMLOptionElement).value);
      expect(optionValues).not.toContain("tab-2");
      expect(optionValues).toContain("tab-1");
      expect(optionValues).toContain("tab-3");
    });

    it("shows all tabs except the left pane tab", () => {
      const tabs = [
        { id: "a", label: "Alpha" },
        { id: "b", label: "Beta" },
        { id: "c", label: "Charlie" },
        { id: "d", label: "Delta" },
      ];

      render(
        <SplitPaneSelector
          tabs={tabs}
          activeLeftTabId="b"
          rightPaneTabId="a"
          onTabChange={vi.fn()}
        />,
      );

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(3);
      expect(options[0]).toHaveTextContent("Alpha");
      expect(options[1]).toHaveTextContent("Charlie");
      expect(options[2]).toHaveTextContent("Delta");
    });
  });

  describe("interactions", () => {
    it("calls onTabChange when a different tab is selected", () => {
      const onTabChange = vi.fn();
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-2"
          onTabChange={onTabChange}
        />,
      );

      const select = screen.getByRole("combobox");
      fireEvent.change(select, { target: { value: "tab-3" } });

      expect(onTabChange).toHaveBeenCalledTimes(1);
      expect(onTabChange).toHaveBeenCalledWith("tab-3");
    });
  });

  describe("option values", () => {
    it("each option has the correct value attribute matching tab id", () => {
      render(
        <SplitPaneSelector
          tabs={defaultTabs}
          activeLeftTabId="tab-1"
          rightPaneTabId="tab-2"
          onTabChange={vi.fn()}
        />,
      );

      const options = screen.getAllByRole("option");
      expect((options[0] as HTMLOptionElement).value).toBe("tab-2");
      expect((options[1] as HTMLOptionElement).value).toBe("tab-3");
    });
  });

  describe("brand colors", () => {
    it("renders tab labels correctly even when brandColor is provided", () => {
      const tabsWithColors = [
        { id: "claude-1", label: "Claude", brandColor: "#D97706" },
        { id: "codex-1", label: "Codex", brandColor: "#22c55e" },
        { id: "shell-1", label: "Shell", brandColor: "#6b7280" },
      ];

      render(
        <SplitPaneSelector
          tabs={tabsWithColors}
          activeLeftTabId="claude-1"
          rightPaneTabId="codex-1"
          onTabChange={vi.fn()}
        />,
      );

      const options = screen.getAllByRole("option");
      expect(options).toHaveLength(2);
      expect(options[0]).toHaveTextContent("Codex");
      expect(options[1]).toHaveTextContent("Shell");
    });
  });
});
