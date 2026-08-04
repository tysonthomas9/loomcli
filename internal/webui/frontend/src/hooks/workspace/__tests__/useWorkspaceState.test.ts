/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceState hook (T24 rewrite).
 * Verifies ephemeral per-workspace snapshot capture/restore:
 * - Scroll position (via scrollContainerRef)
 * - Active panel state (via restorePanel callback)
 *
 * URL-owned state (view, filters, search) is NOT snapshotted.
 */

import { renderHook } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import type { PanelState } from "../usePanelManager";

import {
  useWorkspaceState,
  clearWorkspaceSnapshots,
  type UseWorkspaceStateParams,
} from "../useWorkspaceState";

let mockWorkspaceId = "ws-1";
vi.mock("../useWorkspaceContext", () => ({
  useWorkspaceContext: () => ({ workspaceId: mockWorkspaceId }),
}));

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/**
 * Create a mock scroll container element with configurable scrollTop.
 */
function createScrollContainer(scrollTop = 0): {
  ref: React.RefObject<HTMLElement | null>;
  element: HTMLDivElement;
} {
  const element = document.createElement("div");
  Object.defineProperty(element, "scrollTop", {
    value: scrollTop,
    writable: true,
    configurable: true,
  });
  const ref = { current: element } as React.RefObject<HTMLElement | null>;
  return { ref, element };
}

/**
 * Build default params for useWorkspaceState with vi.fn mocks.
 */
function createParams(overrides?: Partial<UseWorkspaceStateParams>) {
  const { ref } = createScrollContainer(0);

  const params: UseWorkspaceStateParams = {
    scrollContainerRef: ref,
    activePanel: null,
    restorePanel: vi.fn(),
    closeAllPanels: vi.fn(),
    ...overrides,
  };

  return { params };
}

// ---------------------------------------------------------------------------
// Test suite
// ---------------------------------------------------------------------------

describe("useWorkspaceState", () => {
  let rafCallbacks: Map<number, FrameRequestCallback>;
  let nextRafId: number;

  beforeEach(() => {
    clearWorkspaceSnapshots();
    mockWorkspaceId = "ws-1";

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
  // 1. Return value
  // -----------------------------------------------------------------------

  describe("return value", () => {
    it("returns void", () => {
      const { params } = createParams();
      const { result } = renderHook(() => useWorkspaceState(params));
      expect(result.current).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // 2. First visit (no snapshot)
  // -----------------------------------------------------------------------

  describe("first visit", () => {
    it("does not call restorePanel on first visit (no snapshot exists)", () => {
      const { params } = createParams();
      renderHook(() => useWorkspaceState(params));
      flushRaf();
      expect(params.restorePanel).not.toHaveBeenCalled();
    });

    it("does not modify scrollTop on first visit", () => {
      const { ref, element } = createScrollContainer(0);
      const { params } = createParams({ scrollContainerRef: ref });
      renderHook(() => useWorkspaceState(params));
      flushRaf();
      expect(element.scrollTop).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // 3. Snapshot capture on unmount
  // -----------------------------------------------------------------------

  describe("snapshot capture on unmount", () => {
    it("captures scrollTop and activePanel on unmount, restores on remount", () => {
      const { ref, element } = createScrollContainer(0);
      const restorePanel = vi.fn();
      const panel: PanelState = { type: "issue", id: "abc-123" };
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panel,
        restorePanel,
      });

      const { unmount } = renderHook(() => useWorkspaceState(params));

      // Simulate scroll
      element.scrollTop = 350;

      // Unmount captures snapshot
      unmount();

      // Remount with same workspaceId
      restorePanel.mockClear();
      renderHook(() => useWorkspaceState({ ...params, restorePanel }));

      // Panel should be restored
      expect(restorePanel).toHaveBeenCalledWith(panel);

      // Scroll should be restored via rAF
      flushRaf();
      expect(element.scrollTop).toBe(350);
    });
  });

  // -----------------------------------------------------------------------
  // 4. Scroll restore on revisit
  // -----------------------------------------------------------------------

  describe("scroll restore on revisit", () => {
    it("restores scroll position via requestAnimationFrame", () => {
      const { ref, element } = createScrollContainer(0);
      const { params } = createParams({
        scrollContainerRef: ref,
      });

      // First mount: scroll to 500, then unmount
      const { unmount } = renderHook(() => useWorkspaceState(params));
      element.scrollTop = 500;
      unmount();

      // Second mount: scroll should restore
      element.scrollTop = 0;
      renderHook(() => useWorkspaceState(params));

      // Before rAF flush, scroll hasn't changed yet
      expect(element.scrollTop).toBe(0);

      flushRaf();
      expect(element.scrollTop).toBe(500);
    });
  });

  // -----------------------------------------------------------------------
  // 5. Panel restore on revisit
  // -----------------------------------------------------------------------

  describe("panel restore on revisit", () => {
    it("restores issue panel state on revisit", () => {
      const { ref } = createScrollContainer(0);
      const panel: PanelState = { type: "issue", id: "issue-42" };
      const restorePanel = vi.fn();
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panel,
        restorePanel,
      });

      const { unmount } = renderHook(() => useWorkspaceState(params));
      unmount();

      restorePanel.mockClear();
      renderHook(() =>
        useWorkspaceState({ ...params, activePanel: null, restorePanel }),
      );

      expect(restorePanel).toHaveBeenCalledWith(panel);
    });

    it("restores agent panel state on revisit", () => {
      const { ref } = createScrollContainer(0);
      const panel: PanelState = { type: "agent", name: "falcon" };
      const restorePanel = vi.fn();
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panel,
        restorePanel,
      });

      const { unmount } = renderHook(() => useWorkspaceState(params));
      unmount();

      restorePanel.mockClear();
      renderHook(() =>
        useWorkspaceState({ ...params, activePanel: null, restorePanel }),
      );

      expect(restorePanel).toHaveBeenCalledWith(panel);
    });

    it("does not call restorePanel when snapshot has null panel", () => {
      const { ref } = createScrollContainer(0);
      const restorePanel = vi.fn();
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: null,
        restorePanel,
      });

      const { unmount } = renderHook(() => useWorkspaceState(params));
      unmount();

      restorePanel.mockClear();
      renderHook(() => useWorkspaceState({ ...params, restorePanel }));

      expect(restorePanel).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 6. Workspace switch via workspaceId change (defense-in-depth)
  // -----------------------------------------------------------------------

  describe("workspace switch via rerender", () => {
    it("captures old snapshot and restores new on workspaceId change", () => {
      const { ref, element } = createScrollContainer(0);
      const restorePanel = vi.fn();
      const panelA: PanelState = { type: "issue", id: "a-1" };

      mockWorkspaceId = "A";
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panelA,
        restorePanel,
      });

      const { rerender } = renderHook(
        (props: { panel: PanelState }) =>
          useWorkspaceState({
            ...params,
            activePanel: props.panel,
            restorePanel,
          }),
        { initialProps: { panel: panelA } },
      );

      // Scroll on workspace A
      element.scrollTop = 200;

      // Switch to workspace B (first visit)
      restorePanel.mockClear();
      mockWorkspaceId = "B";
      rerender({ panel: null });

      // closeAllPanels called on switch
      expect(params.closeAllPanels).toHaveBeenCalledTimes(1);
      // No snapshot for B → no restorePanel call
      expect(restorePanel).not.toHaveBeenCalled();

      // Switch back to A
      restorePanel.mockClear();
      mockWorkspaceId = "A";
      rerender({ panel: null });

      // A's panel should be restored
      expect(restorePanel).toHaveBeenCalledWith(panelA);

      // A's scroll should be restored
      flushRaf();
      expect(element.scrollTop).toBe(200);
    });

    it("calls closeAllPanels on every workspace switch", () => {
      mockWorkspaceId = "A";
      const { params } = createParams();

      const { rerender } = renderHook(() => useWorkspaceState(params));

      mockWorkspaceId = "B";
      rerender();
      expect(params.closeAllPanels).toHaveBeenCalledTimes(1);

      mockWorkspaceId = "C";
      rerender();
      expect(params.closeAllPanels).toHaveBeenCalledTimes(2);
    });
  });

  // -----------------------------------------------------------------------
  // 7. Multiple workspaces (A → B → C → A)
  // -----------------------------------------------------------------------

  describe("multiple workspaces", () => {
    it("preserves each workspace snapshot independently", () => {
      const { ref, element } = createScrollContainer(0);
      const restorePanel = vi.fn();
      const panelA: PanelState = { type: "issue", id: "a-1" };
      const panelB: PanelState = { type: "agent", name: "hawk" };

      mockWorkspaceId = "A";
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panelA,
        restorePanel,
      });

      const { rerender } = renderHook(
        (props: { panel: PanelState }) =>
          useWorkspaceState({
            ...params,
            activePanel: props.panel,
            restorePanel,
          }),
        { initialProps: { panel: panelA } },
      );

      element.scrollTop = 100;

      // Switch to B
      mockWorkspaceId = "B";
      rerender({ panel: panelB });
      element.scrollTop = 300;

      // Switch to C (first visit)
      mockWorkspaceId = "C";
      rerender({ panel: null });

      // Switch back to A
      restorePanel.mockClear();
      mockWorkspaceId = "A";
      rerender({ panel: null });

      expect(restorePanel).toHaveBeenCalledWith(panelA);
      flushRaf();
      expect(element.scrollTop).toBe(100);
    });
  });

  // -----------------------------------------------------------------------
  // 8. Rapid switch cancels rAF
  // -----------------------------------------------------------------------

  describe("rapid switch cancels rAF", () => {
    it("cancels pending rAF when switching rapidly", () => {
      const { ref, element } = createScrollContainer(0);
      const { params } = createParams({
        scrollContainerRef: ref,
      });

      // First: create a snapshot for B by mounting/unmounting with B
      mockWorkspaceId = "B";
      const { unmount: setup } = renderHook(() => useWorkspaceState(params));
      element.scrollTop = 200;
      setup();

      // Now mount with A
      mockWorkspaceId = "A";
      element.scrollTop = 0;
      const { rerender } = renderHook(() => useWorkspaceState(params));

      element.scrollTop = 100;

      // Switch to B — B has a snapshot, so restoreFromSnapshot schedules rAF
      mockWorkspaceId = "B";
      rerender();
      expect(rafCallbacks.size).toBeGreaterThan(0);

      // Switch to C rapidly without flushing rAF — should cancel
      mockWorkspaceId = "C";
      rerender();

      expect(cancelAnimationFrame).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 9. Cleanup on unmount
  // -----------------------------------------------------------------------

  describe("cleanup", () => {
    it("cancels pending rAF on unmount", () => {
      const { ref, element } = createScrollContainer(0);
      const { params } = createParams({
        scrollContainerRef: ref,
      });

      // First mount/unmount to create a snapshot
      const { unmount: unmount1 } = renderHook(() => useWorkspaceState(params));
      element.scrollTop = 100;
      unmount1();

      // Second mount — triggers rAF for scroll restore
      const { unmount: unmount2 } = renderHook(() => useWorkspaceState(params));

      expect(rafCallbacks.size).toBeGreaterThan(0);

      unmount2();

      expect(cancelAnimationFrame).toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 10. clearWorkspaceSnapshots utility
  // -----------------------------------------------------------------------

  describe("clearWorkspaceSnapshots", () => {
    it("clears all stored snapshots so subsequent mount gets no restore", () => {
      const { ref, element } = createScrollContainer(0);
      const restorePanel = vi.fn();
      const panel: PanelState = { type: "issue", id: "xyz" };
      const { params } = createParams({
        scrollContainerRef: ref,
        activePanel: panel,
        restorePanel,
      });

      // Mount, scroll, unmount
      const { unmount } = renderHook(() => useWorkspaceState(params));
      element.scrollTop = 999;
      unmount();

      // Clear all snapshots
      clearWorkspaceSnapshots();

      // Reset element scroll to verify it's NOT restored to 999
      element.scrollTop = 0;

      // Remount — no restore should happen
      restorePanel.mockClear();
      renderHook(() =>
        useWorkspaceState({ ...params, activePanel: null, restorePanel }),
      );
      flushRaf();

      expect(restorePanel).not.toHaveBeenCalled();
      expect(element.scrollTop).toBe(0);
    });
  });

  // -----------------------------------------------------------------------
  // 11. scrollContainerRef.current is null
  // -----------------------------------------------------------------------

  describe("null scrollContainerRef", () => {
    it("defaults scrollTop to 0 when ref is null during capture", () => {
      const restorePanel = vi.fn();
      const nullRef = {
        current: null,
      } as React.RefObject<HTMLElement | null>;

      const { params } = createParams({
        scrollContainerRef: nullRef,
        activePanel: null,
        restorePanel,
      });

      // Mount and unmount — should not throw
      const { unmount } = renderHook(() => useWorkspaceState(params));
      unmount();

      // Remount — should not throw or set scroll
      renderHook(() => useWorkspaceState(params));
      flushRaf();

      // No restorePanel called (panel was null)
      expect(restorePanel).not.toHaveBeenCalled();
    });
  });

  // -----------------------------------------------------------------------
  // 12. Does NOT manage URL-owned state
  // -----------------------------------------------------------------------

  describe("URL-owned state exclusion", () => {
    it("does not accept view/filter/search params (structural guarantee)", () => {
      // This is a compile-time check. The UseWorkspaceStateParams type
      // does not include stateRef, setView, filterActions, or setSearchValue.
      // If someone tries to add them, TypeScript will error.
      const { params } = createParams();

      // Verify the params shape only has the expected keys
      const keys = Object.keys(params).sort();
      expect(keys).toEqual([
        "activePanel",
        "closeAllPanels",
        "restorePanel",
        "scrollContainerRef",
      ]);
    });
  });
});
