/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import type React from "react";

import { useTabOrdering } from "../useTabOrdering";
import type { TabState } from "../terminalTabUtils";

function makeTab(id: string, opts: { pinned?: boolean } = {}): TabState {
  return {
    id,
    label: id,
    sessionName: `session-${id}`,
    connectionState: "connected" as const,
    backendName: "claude",
    pinned: opts.pinned,
  };
}

function createArgs(
  overrides: Partial<Parameters<typeof useTabOrdering>[0]> = {},
) {
  return {
    tabs: [] as TabState[],
    setTabs: vi.fn() as React.Dispatch<React.SetStateAction<TabState[]>>,
    setActiveTabId: vi.fn() as React.Dispatch<React.SetStateAction<string>>,
    handleTabClose: vi.fn(),
    updatePinned: vi.fn().mockResolvedValue(undefined),
    reorderTabMeta: vi.fn().mockResolvedValue(undefined),
    deleteTab: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
}

describe("useTabOrdering", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("handleCloseOthers", () => {
    it("removes unpinned tabs but preserves pinned tabs", () => {
      const tab1 = makeTab("tab-1");
      const tab2 = makeTab("tab-2", { pinned: true });
      const tab3 = makeTab("tab-3");
      const deleteTab = vi.fn().mockResolvedValue(undefined);
      const setTabs = vi.fn() as React.Dispatch<
        React.SetStateAction<TabState[]>
      >;
      const setActiveTabId = vi.fn() as React.Dispatch<
        React.SetStateAction<string>
      >;

      const args = createArgs({
        tabs: [tab1, tab2, tab3],
        deleteTab,
        setTabs,
        setActiveTabId,
      });

      const { result } = renderHook(() => useTabOrdering(args));

      act(() => {
        result.current.handleCloseOthers("tab-1");
      });

      // deleteTab should be called only for tab-3 (unpinned, not the target)
      expect(deleteTab).toHaveBeenCalledTimes(1);
      expect(deleteTab).toHaveBeenCalledWith("session-tab-3");
      // deleteTab should NOT be called for the pinned tab
      expect(deleteTab).not.toHaveBeenCalledWith("session-tab-2");

      // setTabs should keep target and pinned tabs
      expect(setTabs).toHaveBeenCalledTimes(1);
      const updater = (setTabs as unknown as ReturnType<typeof vi.fn>).mock
        .calls[0][0] as (prev: TabState[]) => TabState[];
      const result2 = updater([tab1, tab2, tab3]);
      expect(result2.map((t) => t.id)).toEqual(["tab-1", "tab-2"]);

      expect(setActiveTabId).toHaveBeenCalledWith("tab-1");
    });

    it("on a pinned tab preserves other pinned tabs", () => {
      const tab1 = makeTab("tab-1", { pinned: true });
      const tab2 = makeTab("tab-2", { pinned: true });
      const tab3 = makeTab("tab-3");
      const deleteTab = vi.fn().mockResolvedValue(undefined);
      const setTabs = vi.fn() as React.Dispatch<
        React.SetStateAction<TabState[]>
      >;

      const args = createArgs({
        tabs: [tab1, tab2, tab3],
        deleteTab,
        setTabs,
      });

      const { result } = renderHook(() => useTabOrdering(args));

      act(() => {
        result.current.handleCloseOthers("tab-1");
      });

      // Only tab-3 (unpinned) should be deleted
      expect(deleteTab).toHaveBeenCalledTimes(1);
      expect(deleteTab).toHaveBeenCalledWith("session-tab-3");

      // setTabs should keep both pinned tabs
      const updater = (setTabs as unknown as ReturnType<typeof vi.fn>).mock
        .calls[0][0] as (prev: TabState[]) => TabState[];
      const result2 = updater([tab1, tab2, tab3]);
      expect(result2.map((t) => t.id)).toEqual(["tab-1", "tab-2"]);
    });

    it("with no pinned tabs closes all others", () => {
      const tab1 = makeTab("tab-1");
      const tab2 = makeTab("tab-2");
      const tab3 = makeTab("tab-3");
      const deleteTab = vi.fn().mockResolvedValue(undefined);
      const setTabs = vi.fn() as React.Dispatch<
        React.SetStateAction<TabState[]>
      >;

      const args = createArgs({
        tabs: [tab1, tab2, tab3],
        deleteTab,
        setTabs,
      });

      const { result } = renderHook(() => useTabOrdering(args));

      act(() => {
        result.current.handleCloseOthers("tab-1");
      });

      expect(deleteTab).toHaveBeenCalledTimes(2);
      expect(deleteTab).toHaveBeenCalledWith("session-tab-2");
      expect(deleteTab).toHaveBeenCalledWith("session-tab-3");

      const updater = (setTabs as unknown as ReturnType<typeof vi.fn>).mock
        .calls[0][0] as (prev: TabState[]) => TabState[];
      const result2 = updater([tab1, tab2, tab3]);
      expect(result2.map((t) => t.id)).toEqual(["tab-1"]);
    });

    it("when all other tabs are pinned closes nothing", () => {
      const tab1 = makeTab("tab-1");
      const tab2 = makeTab("tab-2", { pinned: true });
      const tab3 = makeTab("tab-3", { pinned: true });
      const deleteTab = vi.fn().mockResolvedValue(undefined);
      const setTabs = vi.fn() as React.Dispatch<
        React.SetStateAction<TabState[]>
      >;
      const setActiveTabId = vi.fn() as React.Dispatch<
        React.SetStateAction<string>
      >;

      const args = createArgs({
        tabs: [tab1, tab2, tab3],
        deleteTab,
        setTabs,
        setActiveTabId,
      });

      const { result } = renderHook(() => useTabOrdering(args));

      act(() => {
        result.current.handleCloseOthers("tab-1");
      });

      // No tabs should be deleted — early return
      expect(deleteTab).not.toHaveBeenCalled();
      expect(setTabs).not.toHaveBeenCalled();
      expect(setActiveTabId).not.toHaveBeenCalled();
    });

    it("sets activeTabId to the target tab", () => {
      const tab1 = makeTab("tab-1");
      const tab2 = makeTab("tab-2");
      const setActiveTabId = vi.fn() as React.Dispatch<
        React.SetStateAction<string>
      >;

      const args = createArgs({
        tabs: [tab1, tab2],
        setActiveTabId,
      });

      const { result } = renderHook(() => useTabOrdering(args));

      act(() => {
        result.current.handleCloseOthers("tab-1");
      });

      expect(setActiveTabId).toHaveBeenCalledWith("tab-1");
    });
  });
});
