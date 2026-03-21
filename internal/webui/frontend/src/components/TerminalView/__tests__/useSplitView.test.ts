/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useSplitView hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, beforeEach } from "vitest";

import { useSplitView } from "../useSplitView";
import type { TabState } from "../terminalTabUtils";

function makeTab(id: string, label?: string): TabState {
  return {
    id,
    label: label ?? id,
    sessionName: id,
    connectionState: "connected",
    backendName: "claude",
  };
}

function makeTabs(count: number): TabState[] {
  return Array.from({ length: count }, (_, i) =>
    makeTab(`tab-${i + 1}`, `Terminal ${i + 1}`),
  );
}

describe("useSplitView", () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  describe("initial state", () => {
    it("starts with split view disabled by default", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      expect(result.current.isSplitView).toBe(false);
      expect(result.current.splitRatio).toBe(0.5);
      expect(result.current.focusedPane).toBe("left");
    });

    it("restores split view state from sessionStorage", () => {
      sessionStorage.setItem("terminal-split-view", "true");
      sessionStorage.setItem("terminal-split-right-tab", "tab-2");

      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      expect(result.current.isSplitView).toBe(true);
      expect(result.current.rightPaneTabId).toBe("tab-2");
    });

    it("restores right pane tab ID from sessionStorage", () => {
      sessionStorage.setItem("terminal-split-right-tab", "tab-3");

      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      expect(result.current.rightPaneTabId).toBe("tab-3");
    });

    it("defaults rightPaneTabId to empty string when not in sessionStorage", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      expect(result.current.rightPaneTabId).toBe("");
    });
  });

  describe("canSplit", () => {
    it("returns true when 2 or more tabs exist", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(2), activeTabId: "tab-1" }),
      );

      expect(result.current.canSplit).toBe(true);
    });

    it("returns false when fewer than 2 tabs exist", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(1), activeTabId: "tab-1" }),
      );

      expect(result.current.canSplit).toBe(false);
    });

    it("returns false for empty tabs array", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: [], activeTabId: "" }),
      );

      expect(result.current.canSplit).toBe(false);
    });
  });

  describe("handleToggleSplit", () => {
    it("enables split view and selects first non-active tab for right pane", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });

      expect(result.current.isSplitView).toBe(true);
      expect(result.current.rightPaneTabId).toBe("tab-2");
    });

    it("disables split view on second toggle", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });
      expect(result.current.isSplitView).toBe(true);

      act(() => {
        result.current.handleToggleSplit();
      });
      expect(result.current.isSplitView).toBe(false);
    });

    it("selects correct right pane tab when active tab is not the first", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-2" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });

      expect(result.current.isSplitView).toBe(true);
      expect(result.current.rightPaneTabId).toBe("tab-1");
    });
  });

  describe("sessionStorage persistence", () => {
    it("persists isSplitView to sessionStorage when toggled on", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });

      expect(sessionStorage.getItem("terminal-split-view")).toBe("true");
    });

    it("persists isSplitView to sessionStorage when toggled off", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });
      act(() => {
        result.current.handleToggleSplit();
      });

      expect(sessionStorage.getItem("terminal-split-view")).toBe("false");
    });

    it("persists rightPaneTabId to sessionStorage when changed", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleToggleSplit();
      });
      act(() => {
        result.current.handleRightPaneTabChange("tab-3");
      });

      expect(sessionStorage.getItem("terminal-split-right-tab")).toBe("tab-3");
    });
  });

  describe("tab conflict resolution", () => {
    it("auto-switches right pane when activeTabId matches rightPaneTabId", () => {
      sessionStorage.setItem("terminal-split-view", "true");
      sessionStorage.setItem("terminal-split-right-tab", "tab-1");

      const tabs = makeTabs(3);
      const { result } = renderHook(() =>
        useSplitView({ tabs, activeTabId: "tab-1" }),
      );

      // The effect should auto-switch right pane to a different tab
      expect(result.current.rightPaneTabId).not.toBe("tab-1");
    });

    it("does not auto-switch when split view is disabled", () => {
      sessionStorage.setItem("terminal-split-view", "false");
      sessionStorage.setItem("terminal-split-right-tab", "tab-1");

      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      // Right pane tab ID preserved since split is off
      expect(result.current.rightPaneTabId).toBe("tab-1");
    });
  });

  describe("tab removal handling", () => {
    it("disables split view when tabs drop below 2", () => {
      sessionStorage.setItem("terminal-split-view", "true");
      sessionStorage.setItem("terminal-split-right-tab", "tab-2");

      const { result, rerender } = renderHook(
        ({ tabs, activeTabId }) => useSplitView({ tabs, activeTabId }),
        {
          initialProps: {
            tabs: makeTabs(2),
            activeTabId: "tab-1",
          },
        },
      );

      expect(result.current.isSplitView).toBe(true);

      // Remove a tab, leaving only 1
      rerender({
        tabs: [makeTab("tab-1", "Terminal 1")],
        activeTabId: "tab-1",
      });

      expect(result.current.isSplitView).toBe(false);
    });

    it("falls back to another tab when right pane tab is removed", () => {
      sessionStorage.setItem("terminal-split-view", "true");
      sessionStorage.setItem("terminal-split-right-tab", "tab-3");

      const { result, rerender } = renderHook(
        ({ tabs, activeTabId }) => useSplitView({ tabs, activeTabId }),
        {
          initialProps: {
            tabs: makeTabs(3),
            activeTabId: "tab-1",
          },
        },
      );

      expect(result.current.isSplitView).toBe(true);
      expect(result.current.rightPaneTabId).toBe("tab-3");

      // Remove tab-3
      rerender({
        tabs: [makeTab("tab-1", "Terminal 1"), makeTab("tab-2", "Terminal 2")],
        activeTabId: "tab-1",
      });

      expect(result.current.isSplitView).toBe(true);
      expect(result.current.rightPaneTabId).toBe("tab-2");
    });
  });

  describe("handleSplitRatioChange", () => {
    it("updates split ratio", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleSplitRatioChange(0.3);
      });

      expect(result.current.splitRatio).toBe(0.3);
    });
  });

  describe("handleRightPaneTabChange", () => {
    it("updates right pane tab ID", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.handleRightPaneTabChange("tab-3");
      });

      expect(result.current.rightPaneTabId).toBe("tab-3");
    });
  });

  describe("setFocusedPane", () => {
    it("updates focused pane to right", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.setFocusedPane("right");
      });

      expect(result.current.focusedPane).toBe("right");
    });

    it("updates focused pane back to left", () => {
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      act(() => {
        result.current.setFocusedPane("right");
      });
      act(() => {
        result.current.setFocusedPane("left");
      });

      expect(result.current.focusedPane).toBe("left");
    });
  });

  describe("matchMedia guard", () => {
    it("does not throw when window.matchMedia is not a function", () => {
      // In jsdom, matchMedia may not be available; the hook guards with typeof check.
      // This test verifies the hook initializes without error.
      const { result } = renderHook(() =>
        useSplitView({ tabs: makeTabs(3), activeTabId: "tab-1" }),
      );

      expect(result.current.isSplitView).toBeDefined();
    });
  });
});
