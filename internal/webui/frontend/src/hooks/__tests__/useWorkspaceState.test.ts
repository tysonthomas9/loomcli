/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceState hook.
 * Verifies per-workspace snapshot capture/restore, scroll management,
 * panel close on switch.
 *
 * After T12 migration: hook accepts workspaceId as a prop, returns void,
 * and reacts to workspaceId changes via useEffect.
 */

import { renderHook } from "@testing-library/react";
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
    workspaceId: "initial-ws",
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
  let rafCallbacks: Map<number, FrameRequestCallback>;
  let nextRafId: number;

  beforeEach(() => {
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
  // 1. The hook returns void
  // -----------------------------------------------------------------------

  describe("return value", () => {
    it("returns void (no currentWorkspaceId or switchWorkspace)", () => {
      const { params } = createParams();

      const { result } = renderHook(() => useWorkspaceState(params));

      expect(result.current).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // 2. Snapshot capture on workspace change
  // -----------------------------------------------------------------------

  describe("snapshot capture", () => {
    it("captures current state when switching workspaces via rerender", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      mockScrollContainer(150);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      // Simulate user changing state while on workspace A
      stateRef.current.view = "table";
      stateRef.current.filters = { search: "auth", showBlocked: true };
      stateRef.current.searchValue = "auth";
      stateRef.current.selectedIssueId = "issue-42";

      // Now switch to workspace B
      rerender({ wsId: "B" });

      // B gets defaults on first visit
      expect(params.setView).toHaveBeenCalledWith("kanban");
      expect(params.filterActions.clearAll).toHaveBeenCalled();
      expect(params.setSearchValue).toHaveBeenCalledWith("");

      // Clear mocks
      vi.mocked(params.setView).mockClear();
      vi.mocked(params.filterActions.clearAll).mockClear();
      vi.mocked(params.setSearchValue).mockClear();
      vi.mocked(params.filterActions.setSearch).mockClear();
      vi.mocked(params.filterActions.setShowBlocked).mockClear();

      // Switch back to A - should restore A's captured state
      rerender({ wsId: "A" });

      expect(params.setView).toHaveBeenCalledWith("table");
      expect(params.filterActions.setSearch).toHaveBeenCalledWith("auth");
      expect(params.filterActions.setShowBlocked).toHaveBeenCalledWith(true);
      expect(params.setSearchValue).toHaveBeenCalledWith("auth");
    });

    it("captures scrollTop from scroll container element", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      const scrollEl = mockScrollContainer(0);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      // Set scroll position and state
      scrollEl.scrollTop = 500;
      stateRef.current.view = "kanban";

      // Switch to B - captures A's scroll at 500
      rerender({ wsId: "B" });

      // Switch back to A
      rerender({ wsId: "A" });

      // Flush rAF to restore scroll
      flushRaf();

      expect(scrollEl.scrollTop).toBe(500);
    });
  });

  // -----------------------------------------------------------------------
  // 3. Snapshot restore
  // -----------------------------------------------------------------------

  describe("snapshot restore", () => {
    it("restores all saved state values when switching back", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      mockScrollContainer(0);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

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
      rerender({ wsId: "B" });

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
      rerender({ wsId: "A" });

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
  // 4. No snapshot (first visit)
  // -----------------------------------------------------------------------

  describe("no snapshot (first visit)", () => {
    it("applies defaults when visiting a workspace with no prior snapshot", () => {
      const { params } = createParams({ workspaceId: "A" });

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      // Clear mock history
      vi.mocked(params.setView).mockClear();
      vi.mocked(params.filterActions.clearAll).mockClear();
      vi.mocked(params.setSearchValue).mockClear();

      // Switch to B (never visited before)
      rerender({ wsId: "B" });

      // Defaults: kanban view, clearAll filters, empty search
      expect(params.setView).toHaveBeenCalledWith("kanban");
      expect(params.filterActions.clearAll).toHaveBeenCalled();
      expect(params.setSearchValue).toHaveBeenCalledWith("");
    });
  });

  // -----------------------------------------------------------------------
  // 5. Multiple workspaces
  // -----------------------------------------------------------------------

  describe("multiple workspaces", () => {
    it("preserves each workspace state independently (A -> B -> C -> A)", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      mockScrollContainer(0);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      stateRef.current = {
        view: "table",
        filters: {},
        searchValue: "alpha",
        selectedIssueId: "a1",
      };

      // Switch to B
      rerender({ wsId: "B" });
      stateRef.current = {
        view: "graph",
        filters: {},
        searchValue: "beta",
        selectedIssueId: "b1",
      };

      // Switch to C
      rerender({ wsId: "C" });
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
      rerender({ wsId: "A" });

      // A's state should be restored
      expect(params.setView).toHaveBeenCalledWith("table");
      expect(params.setSearchValue).toHaveBeenCalledWith("alpha");
    });
  });

  // -----------------------------------------------------------------------
  // 6. Panel close
  // -----------------------------------------------------------------------

  describe("panel close", () => {
    it("calls closeAllPanels on every switch", () => {
      const { params } = createParams({ workspaceId: "A" });

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      rerender({ wsId: "B" });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(1);

      rerender({ wsId: "C" });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(2);

      rerender({ wsId: "D" });
      expect(params.closeAllPanels).toHaveBeenCalledTimes(3);
    });
  });

  // -----------------------------------------------------------------------
  // 7. Scroll position
  // -----------------------------------------------------------------------

  describe("scroll position", () => {
    it("captures scrollTop from scroll container and restores via rAF", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      const scrollEl = mockScrollContainer(0);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      // Simulate scrolling
      scrollEl.scrollTop = 250;
      stateRef.current.view = "kanban";

      // Switch to B (captures A at 250)
      rerender({ wsId: "B" });
      flushRaf();

      // B's default scroll is 0
      expect(scrollEl.scrollTop).toBe(0);

      // Switch back to A
      rerender({ wsId: "A" });
      flushRaf();

      // A's scroll should be restored to 250
      expect(scrollEl.scrollTop).toBe(250);
    });

    it("defaults scrollTop to 0 when no main-content element exists", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      // No scroll container in DOM

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      stateRef.current.view = "kanban";

      // Switch to B - should not throw even without main-content
      rerender({ wsId: "B" });

      // Should not throw when flushing rAF without element
      flushRaf();
    });
  });

  // -----------------------------------------------------------------------
  // 8. Rapid switch cancels rAF
  // -----------------------------------------------------------------------

  describe("rapid switch cancels rAF", () => {
    it("cancels pending rAF when switching rapidly, only final scroll restore executes", () => {
      const { params, stateRef } = createParams({ workspaceId: "A" });
      const scrollEl = mockScrollContainer(0);

      const { rerender } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      scrollEl.scrollTop = 100;
      stateRef.current.view = "kanban";

      // Switch to B
      rerender({ wsId: "B" });
      scrollEl.scrollTop = 200;
      stateRef.current.view = "table";

      // Switch to C rapidly without flushing rAF
      rerender({ wsId: "C" });

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
  // 9. Cleanup
  // -----------------------------------------------------------------------

  describe("cleanup", () => {
    it("cancels pending rAF on unmount", () => {
      const { params } = createParams({ workspaceId: "A" });

      const { rerender, unmount } = renderHook(
        (props: { wsId: string }) =>
          useWorkspaceState({ ...params, workspaceId: props.wsId }),
        { initialProps: { wsId: "A" } },
      );

      // Trigger a switch to create a pending rAF
      rerender({ wsId: "B" });

      // Should have a pending rAF
      expect(rafCallbacks.size).toBe(1);

      unmount();

      expect(cancelAnimationFrame).toHaveBeenCalled();
    });
  });
});
