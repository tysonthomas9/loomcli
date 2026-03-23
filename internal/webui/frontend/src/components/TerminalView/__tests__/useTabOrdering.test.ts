/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useState } from "react";

import { useTabOrdering } from "../useTabOrdering";
import type { TabState } from "../useTabOrdering";

function createTab(
  id: string,
  opts: { pinned?: boolean } = {},
): TabState {
  return {
    id,
    label: `Tab ${id}`,
    sessionName: `session-${id}`,
    pinned: opts.pinned,
  };
}

function setupHook(
  initialTabs: TabState[],
  deleteTab?: (sessionName: string) => void,
) {
  return renderHook(() => {
    const [tabs, setTabs] = useState(initialTabs);
    const [activeTabId, setActiveTabId] = useState(initialTabs[0]?.id ?? "");
    const result = useTabOrdering({
      tabs,
      setTabs,
      activeTabId,
      setActiveTabId,
      deleteTab,
    });
    return { ...result, tabs, activeTabId };
  });
}

describe("useTabOrdering", () => {
  describe("handleCloseOthers", () => {
    it("removes unpinned tabs but preserves pinned tabs", () => {
      const deleteTab = vi.fn();
      const tabs = [
        createTab("1"),
        createTab("2", { pinned: true }),
        createTab("3"),
      ];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseOthers("1");
      });

      // deleteTab should only be called for tab-3 (unpinned, not target)
      expect(deleteTab).toHaveBeenCalledTimes(1);
      expect(deleteTab).toHaveBeenCalledWith("session-3");
      // deleteTab should NOT be called for the pinned tab
      expect(deleteTab).not.toHaveBeenCalledWith("session-2");
    });

    it("on a pinned tab preserves other pinned tabs", () => {
      const deleteTab = vi.fn();
      const tabs = [
        createTab("1", { pinned: true }),
        createTab("2", { pinned: true }),
        createTab("3"),
      ];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseOthers("1");
      });

      // Only unpinned tab-3 should be deleted
      expect(deleteTab).toHaveBeenCalledTimes(1);
      expect(deleteTab).toHaveBeenCalledWith("session-3");
    });

    it("with no pinned tabs closes all others", () => {
      const deleteTab = vi.fn();
      const tabs = [createTab("1"), createTab("2"), createTab("3")];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseOthers("1");
      });

      expect(deleteTab).toHaveBeenCalledTimes(2);
      expect(deleteTab).toHaveBeenCalledWith("session-2");
      expect(deleteTab).toHaveBeenCalledWith("session-3");
    });

    it("when all other tabs are pinned closes nothing", () => {
      const deleteTab = vi.fn();
      const tabs = [
        createTab("1"),
        createTab("2", { pinned: true }),
        createTab("3", { pinned: true }),
      ];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseOthers("1");
      });

      expect(deleteTab).not.toHaveBeenCalled();
    });

    it("sets activeTabId to the target tab", () => {
      const deleteTab = vi.fn();
      const tabs = [createTab("1"), createTab("2"), createTab("3")];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseOthers("2");
      });

      expect(result.current.activeTabId).toBe("2");
    });
  });

  describe("handleCloseAll", () => {
    it("closes all unpinned tabs", () => {
      const deleteTab = vi.fn();
      const tabs = [
        createTab("1"),
        createTab("2", { pinned: true }),
        createTab("3"),
      ];
      const { result } = setupHook(tabs, deleteTab);

      act(() => {
        result.current.handleCloseAll();
      });

      expect(deleteTab).toHaveBeenCalledTimes(2);
      expect(deleteTab).toHaveBeenCalledWith("session-1");
      expect(deleteTab).toHaveBeenCalledWith("session-3");
      expect(deleteTab).not.toHaveBeenCalledWith("session-2");
    });
  });
});
