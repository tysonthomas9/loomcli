/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceState hook.
 * Verifies per-workspace snapshot capture/restore, URL sync, scroll management,
 * panel close on switch, and popstate handling.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { ViewMode } from "@/components/ViewSwitcher";
import type { FilterState, FilterActions } from "../useFilterState";

import {
  useWorkspaceState,
  type UseWorkspaceStateParams,
} from "../useWorkspaceState";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Mock window.location for URL sync tests.
 */
function mockWindowLocation(search = ""): void {
  Object.defineProperty(window, "location", {
    value: {
      pathname: "/app",
      search,
      href: `http://localhost:3000/app${search}`,
    },
    writable: true,
    configurable: true,
  });
}

/**
 * Mock window.history for URL sync tests.
 */
function mockWindowHistory(): { replaceState: ReturnType<typeof vi.fn> } {
  const replaceState = vi.fn();
  Object.defineProperty(window, "history", {
    value: {
      replaceState,
      pushState: vi.fn(),
      state: null,
    },
    writable: true,
    configurable: true,
  });
  return { replaceState };
}

/**
 * Create a mock scroll container element attached to the DOM via
 * document.getElementById('main-content').
 */
function mockScrollContainer(scrollTop = 0): HTMLDivElement {
  const el = document.createElement("div");
  el.id = "main-content";
  Object.defineProperty(el, "scrollTop", {
    value: scrollTop,
    writable: true,
    configurable: true,
  });
  document.body.appendChild(el);
  return el;
}

/**
 * Build default params for useWorkspaceState with vi.fn mocks.
 */
function createParams(overrides?: Partial<UseWorkspaceStateParams>) {
  const defaultState: {
    view: ViewMode;
    filters: FilterState;
    searchValue: string;
    selectedIssueId: string | null;
  } = {
    view: "kanban",
    filters: {},
    searchValue: "",
    selectedIssueId: null,
  };

  const stateRef = { current: defaultState };

  const filterActions: FilterActions = {
    setPriority: vi.fn(),
    setType: vi.fn(),
    setLabels: vi.fn(),
    setSearch: vi.fn(),
    setShowBlocked: vi.fn(),
    setGroupBy: vi.fn(),
    clearFilter: vi.fn(),
    clearAll: vi.fn(),
  };

  const params: UseWorkspaceStateParams = {
    stateRef: stateRef as React.RefObject<typeof defaultState>,
    setView: vi.fn(),
    filterActions,
    setSearchValue: vi.fn(),
    closeAllPanels: vi.fn(),
    ...overrides,
  };

  return { params, stateRef };
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("useWorkspaceState", () => {
  let historyMock: { replaceState: ReturnType<typeof vi.fn> };
  let rafCallbacks: Map<number, FrameRequestCallback>;
  let nextRafId: number;

  beforeEach(() => {
    mockWindowLocation();
    historyMock = mockWindowHistory();

    // Mock requestAnimationFrame / cancelAnimationFrame
    rafCallbacks = new Map();
    nextRafId = 1;

    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((cb: FrameRequestCallback) => {
        const id = nextRafId++;
        rafCallbacks.set(id, cb);
        return id;
      }),
    );

    vi.stubGlobal(
      "cancelAnimationFrame",
      vi.fn((id: number) => {
        rafCallbacks.delete(id);
      }),
    );
  });

  afterEach(() => {
    // Clean up any elements added to the DOM
    const el = document.getElementById("main-content");
    if (el) el.remove();

    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  /**
   * Flush all pending rAF callbacks (simulates next animation frame).
   */
  function flushRaf(): void {
    for (const [id, cb] of rafCallbacks) {
      cb(performance.now());
      rafCallbacks.delete(id);
    }
  }

  // -----------------------------------------------------------------------
  // 1. Initial state
  // -----------------------------------------------------------------------

  describe("initial state", () => {
    it("currentWorkspaceId is null when no URL param and no snapshots", () => {
      mockWindowLocation("");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(result.current.currentWorkspaceId).toBeNull();
    });

    it("switchWorkspace function is returned", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(typeof result.current.switchWorkspace).toBe("function");
    });
  });

  // -----------------------------------------------------------------------
  // 2. First switch
  // -----------------------------------------------------------------------

  describe("first switch", () => {
    it("sets currentWorkspaceId to the new value", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("A");
      });

      expect(result.current.currentWorkspaceId).toBe("A");
    });

    it("calls closeAllPanels on switch", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("A");
      });

      expect(params.closeAllPanels).toHaveBeenCalled();
    });

    it("restores defaults on first visit (kanban view, empty filters/search)", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("A");
      });

      // First visit with no snapshot: defaults applied
      expect(params.setView).toHaveBeenCalledWith("kanban");
      expect(params.filterActions.clearAll).toHaveBeenCalled();
      expect(params.setSearchValue).toHaveBeenCalledWith("");
    });
  });

  // -----------------------------------------------------------------------
  // 3. Snapshot capture
  // -----------------------------------------------------------------------

  describe("snapshot capture", () => {
    it("captures current state when switching away from a workspace", () => {
      const { params, stateRef } = createParams();
      const scrollEl = mockScrollContainer(150);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to workspace A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Simulate user changing state while on workspace A
      stateRef.current.view = "table";
      stateRef.current.filters = { search: "auth", showBlocked: true };
      stateRef.current.searchValue = "auth";
      stateRef.current.selectedIssueId = "issue-42";
      scrollEl.scrollTop = 300;

      // Now switch to workspace B - should capture A's state
      act(() => {
        result.current.switchWorkspace("B");
      });

      // Switch back to A - should restore A's captured state
      act(() => {
        result.current.switchWorkspace("A");
      });

      expect(params.setView).toHaveBeenCalledWith("table");
      expect(params.filterActions.setSearch).toHaveBeenCalledWith("auth");
      expect(params.filterActions.setShowBlocked).toHaveBeenCalledWith(true);
      expect(params.setSearchValue).toHaveBeenCalledWith("auth");
    });

    it("captures scrollTop from scroll container element", () => {
      const { params, stateRef } = createParams();
      const scrollEl = mockScrollContainer(0);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Set scroll position and state
      scrollEl.scrollTop = 500;
      stateRef.current.view = "kanban";

      // Switch to B - captures A's scroll at 500
      act(() => {
        result.current.switchWorkspace("B");
      });

      // Switch back to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Flush rAF to restore scroll
      flushRaf();

      expect(scrollEl.scrollTop).toBe(500);
    });
  });

  // -----------------------------------------------------------------------
  // 4. Snapshot restore
  // -----------------------------------------------------------------------

  describe("snapshot restore", () => {
    it("restores all saved state values when switching back", () => {
      const { params, stateRef } = createParams();
      mockScrollContainer(0);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Set state for workspace A
      stateRef.current = {
        view: "graph",
        filters: {
          search: "network",
          showBlocked: false,
          groupBy: "epic",
          labels: ["frontend"],
        },
        searchValue: "network",
        selectedIssueId: "issue-99",
      };

      // Switch to B
      act(() => {
        result.current.switchWorkspace("B");
      });

      // Change state for workspace B
      stateRef.current = {
        view: "monitor",
        filters: {},
        searchValue: "other",
        selectedIssueId: null,
      };

      // Clear the mock call history so we can verify the restore calls
      vi.mocked(params.setView).mockClear();
      vi.mocked(params.filterActions.clearAll).mockClear();
      vi.mocked(params.filterActions.setSearch).mockClear();
      vi.mocked(params.filterActions.setShowBlocked).mockClear();
      vi.mocked(params.filterActions.setGroupBy).mockClear();
      vi.mocked(params.filterActions.setLabels).mockClear();
      vi.mocked(params.setSearchValue).mockClear();

      // Switch back to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      expect(params.setView).toHaveBeenCalledWith("graph");
      expect(params.filterActions.clearAll).toHaveBeenCalled();
      expect(params.filterActions.setSearch).toHaveBeenCalledWith("network");
      expect(params.filterActions.setShowBlocked).toHaveBeenCalledWith(false);
      expect(params.filterActions.setGroupBy).toHaveBeenCalledWith("epic");
      expect(params.filterActions.setLabels).toHaveBeenCalledWith(["frontend"]);
      expect(params.setSearchValue).toHaveBeenCalledWith("network");
    });
  });

  // -----------------------------------------------------------------------
  // 5. No snapshot (first visit)
  // -----------------------------------------------------------------------

  describe("no snapshot (first visit)", () => {
    it("applies defaults when visiting a workspace with no prior snapshot", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A first
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Clear mock history
      vi.mocked(params.setView).mockClear();
      vi.mocked(params.filterActions.clearAll).mockClear();
      vi.mocked(params.setSearchValue).mockClear();

      // Switch to B (never visited before)
      act(() => {
        result.current.switchWorkspace("B");
      });

      // Defaults: kanban view, clearAll filters, empty search
      expect(params.setView).toHaveBeenCalledWith("kanban");
      expect(params.filterActions.clearAll).toHaveBeenCalled();
      expect(params.setSearchValue).toHaveBeenCalledWith("");
    });
  });

  // -----------------------------------------------------------------------
  // 6. Multiple workspaces
  // -----------------------------------------------------------------------

  describe("multiple workspaces", () => {
    it("preserves each workspace state independently (A -> B -> C -> A)", () => {
      const { params, stateRef } = createParams();
      mockScrollContainer(0);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });
      stateRef.current = {
        view: "table",
        filters: {},
        searchValue: "alpha",
        selectedIssueId: "a1",
      };

      // Switch to B
      act(() => {
        result.current.switchWorkspace("B");
      });
      stateRef.current = {
        view: "graph",
        filters: {},
        searchValue: "beta",
        selectedIssueId: "b1",
      };

      // Switch to C
      act(() => {
        result.current.switchWorkspace("C");
      });
      stateRef.current = {
        view: "monitor",
        filters: {},
        searchValue: "gamma",
        selectedIssueId: "c1",
      };

      // Clear mocks before switching back to A
      vi.mocked(params.setView).mockClear();
      vi.mocked(params.setSearchValue).mockClear();

      // Switch back to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // A's state should be restored
      expect(params.setView).toHaveBeenCalledWith("table");
      expect(params.setSearchValue).toHaveBeenCalledWith("alpha");
      expect(result.current.currentWorkspaceId).toBe("A");
    });
  });

  // -----------------------------------------------------------------------
  // 7. Panel close
  // -----------------------------------------------------------------------

  describe("panel close", () => {
    it("calls closeAllPanels on every switch", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("A");
      });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(1);

      act(() => {
        result.current.switchWorkspace("B");
      });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(2);

      act(() => {
        result.current.switchWorkspace("C");
      });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(3);
    });
  });

  // -----------------------------------------------------------------------
  // 8. Scroll position
  // -----------------------------------------------------------------------

  describe("scroll position", () => {
    it("captures scrollTop from scroll container and restores via rAF", () => {
      const { params, stateRef } = createParams();
      const scrollEl = mockScrollContainer(0);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Simulate scrolling
      scrollEl.scrollTop = 250;
      stateRef.current.view = "kanban";

      // Switch to B (captures A at 250)
      act(() => {
        result.current.switchWorkspace("B");
      });
      flushRaf();

      // B's default scroll is 0
      expect(scrollEl.scrollTop).toBe(0);

      // Switch back to A
      act(() => {
        result.current.switchWorkspace("A");
      });
      flushRaf();

      // A's scroll should be restored to 250
      expect(scrollEl.scrollTop).toBe(250);
    });

    it("defaults scrollTop to 0 when no main-content element exists", () => {
      const { params, stateRef } = createParams();
      // No scroll container in DOM

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });

      stateRef.current.view = "kanban";

      // Switch to B - should not throw even without main-content
      act(() => {
        result.current.switchWorkspace("B");
      });

      // Should not throw when flushing rAF without element
      flushRaf();

      expect(result.current.currentWorkspaceId).toBe("B");
    });
  });

  // -----------------------------------------------------------------------
  // 9. Rapid switch cancels rAF
  // -----------------------------------------------------------------------

  describe("rapid switch cancels rAF", () => {
    it("cancels pending rAF when switching rapidly, only final scroll restore executes", () => {
      const { params, stateRef } = createParams();
      const scrollEl = mockScrollContainer(0);

      const { result } = renderHook(() => useWorkspaceState(params));

      // Switch to A
      act(() => {
        result.current.switchWorkspace("A");
      });
      scrollEl.scrollTop = 100;
      stateRef.current.view = "kanban";

      // Switch to B
      act(() => {
        result.current.switchWorkspace("B");
      });
      scrollEl.scrollTop = 200;
      stateRef.current.view = "table";

      // Switch to C rapidly without flushing rAF
      act(() => {
        result.current.switchWorkspace("C");
      });

      // cancelAnimationFrame should have been called
      expect(cancelAnimationFrame).toHaveBeenCalled();

      // Only the latest rAF callback should remain
      expect(rafCallbacks.size).toBe(1);

      // Flush - should restore C's scroll (default 0 since first visit)
      flushRaf();
      expect(scrollEl.scrollTop).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // 10. Same workspace / null state
  // -----------------------------------------------------------------------

  describe("same workspace / null state", () => {
    it("switchWorkspace(null) from initial null state works gracefully", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      // Should not throw
      act(() => {
        result.current.switchWorkspace(null);
      });

      expect(result.current.currentWorkspaceId).toBeNull();
    });

    it("does not capture snapshot when currentWorkspaceId is null", () => {
      const { params, stateRef } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      // State ref has some values
      stateRef.current.view = "table";
      stateRef.current.searchValue = "test";

      // Switch from null to A - should not try to save null workspace snapshot
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Defaults applied (not the ref values, since there's no snapshot for A)
      expect(params.setView).toHaveBeenCalledWith("kanban");
      expect(params.setSearchValue).toHaveBeenCalledWith("");
    });
  });

  // -----------------------------------------------------------------------
  // 11. URL workspace param
  // -----------------------------------------------------------------------

  describe("URL workspace param", () => {
    it("updates workspace query param on switch via replaceState", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("my-workspace");
      });

      expect(historyMock.replaceState).toHaveBeenCalledWith(
        null,
        "",
        "/app?workspace=my-workspace",
      );
    });

    it("removes workspace param when switching to null", () => {
      mockWindowLocation("?workspace=old");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace(null);
      });

      const lastCall = historyMock.replaceState.mock.calls.at(-1);
      expect(lastCall?.[2]).toBe("/app");
    });

    it("preserves other URL params when updating workspace", () => {
      mockWindowLocation("?view=kanban&priority=2");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      act(() => {
        result.current.switchWorkspace("ws-1");
      });

      const lastUrl = historyMock.replaceState.mock.calls.at(-1)?.[2] as string;
      expect(lastUrl).toContain("workspace=ws-1");
      expect(lastUrl).toContain("view=kanban");
      expect(lastUrl).toContain("priority=2");
    });
  });

  // -----------------------------------------------------------------------
  // 12. URL initialization
  // -----------------------------------------------------------------------

  describe("URL initialization", () => {
    it("initializes currentWorkspaceId from ?workspace=foo", () => {
      mockWindowLocation("?workspace=foo");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(result.current.currentWorkspaceId).toBe("foo");
    });

    it("initializes to null when workspace param is absent", () => {
      mockWindowLocation("?view=kanban");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(result.current.currentWorkspaceId).toBeNull();
    });

    it("initializes to null when URL has no params", () => {
      mockWindowLocation("");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(result.current.currentWorkspaceId).toBeNull();
    });
  });

  // -----------------------------------------------------------------------
  // 13. Popstate handling
  // -----------------------------------------------------------------------

  describe("popstate handling", () => {
    it("switches workspace when popstate fires with changed workspace param", () => {
      mockWindowLocation("?workspace=A");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));
      expect(result.current.currentWorkspaceId).toBe("A");

      // Simulate browser navigation changing workspace
      act(() => {
        mockWindowLocation("?workspace=B");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current.currentWorkspaceId).toBe("B");
    });

    it("switches to null when popstate removes workspace param", () => {
      mockWindowLocation("?workspace=A");
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));
      expect(result.current.currentWorkspaceId).toBe("A");

      act(() => {
        mockWindowLocation("");
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      expect(result.current.currentWorkspaceId).toBeNull();
    });

    it("does not switch when popstate fires with same workspace param", () => {
      mockWindowLocation("?workspace=A");
      const { params } = createParams();

      renderHook(() => useWorkspaceState(params));

      // Clear initial calls
      vi.mocked(params.closeAllPanels).mockClear();

      act(() => {
        // URL still has workspace=A
        window.dispatchEvent(new PopStateEvent("popstate"));
      });

      // closeAllPanels should not be called since workspace did not change
      expect(params.closeAllPanels).not.toHaveBeenCalled();
    });

    it("cleans up popstate listener on unmount", () => {
      const removeEventListenerSpy = vi.spyOn(window, "removeEventListener");
      const { params } = createParams();

      const { unmount } = renderHook(() => useWorkspaceState(params));

      unmount();

      expect(removeEventListenerSpy).toHaveBeenCalledWith(
        "popstate",
        expect.any(Function),
      );
    });
  });

  // -----------------------------------------------------------------------
  // Cleanup
  // -----------------------------------------------------------------------

  describe("cleanup", () => {
    it("cancels pending rAF on unmount", () => {
      const { params } = createParams();

      const { result, unmount } = renderHook(() => useWorkspaceState(params));

      // Trigger a switch to create a pending rAF
      act(() => {
        result.current.switchWorkspace("A");
      });

      // Should have a pending rAF
      expect(rafCallbacks.size).toBe(1);

      unmount();

      expect(cancelAnimationFrame).toHaveBeenCalled();
    });
  });
});
