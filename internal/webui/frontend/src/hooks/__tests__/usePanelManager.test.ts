/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for usePanelManager hook.
 *
 * Covers: immediate open, same-panel no-op, same-type swap, cross-type
 * close-then-open transition, closePanel, rapid clicking, unmount cleanup,
 * and isOpen checks.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { usePanelManager } from "../usePanelManager";

/** Transition duration used internally by usePanelManager. */
const TRANSITION_MS = 300;

describe("usePanelManager", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("initial state", () => {
    it("starts with no active panel", () => {
      const { result } = renderHook(() => usePanelManager());

      expect(result.current.activePanel).toBeNull();
      expect(result.current.pendingPanel).toBeNull();
    });
  });

  describe("openPanel with no active panel", () => {
    it("immediately sets activePanel for an issue panel", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-1",
      });
      expect(result.current.pendingPanel).toBeNull();
    });

    it("immediately sets activePanel for an agent panel", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });
      expect(result.current.pendingPanel).toBeNull();
    });
  });

  describe("openPanel with same panel already active (no-op)", () => {
    it("does not change state when opening the same issue panel", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      const panelBefore = result.current.activePanel;

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      // activePanel reference stays the same (no re-render triggered).
      expect(result.current.activePanel).toBe(panelBefore);
      expect(result.current.pendingPanel).toBeNull();
    });

    it("does not change state when opening the same agent panel", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      const panelBefore = result.current.activePanel;

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      expect(result.current.activePanel).toBe(panelBefore);
      expect(result.current.pendingPanel).toBeNull();
    });
  });

  describe("openPanel with same type but different id (content swap)", () => {
    it("swaps issue content without close animation", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-2" });
      });

      // Should swap immediately, no pending panel, no null intermediate.
      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-2",
      });
      expect(result.current.pendingPanel).toBeNull();
    });

    it("swaps agent content without close animation", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-beta" });
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-beta",
      });
      expect(result.current.pendingPanel).toBeNull();
    });
  });

  describe("openPanel with different type active (close-then-open transition)", () => {
    it("closes current panel and queues new panel as pending", () => {
      const { result } = renderHook(() => usePanelManager());

      // Open an issue panel first.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      // Open an agent panel (different type).
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Current panel should be null (closing), pending should be queued.
      expect(result.current.activePanel).toBeNull();
      expect(result.current.pendingPanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });
    });

    it("opens the pending panel after TRANSITION_MS", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Advance time by the transition duration.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });
      expect(result.current.pendingPanel).toBeNull();
    });

    it("does not open pending panel before TRANSITION_MS completes", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Advance time just short of the transition.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS - 1);
      });

      expect(result.current.activePanel).toBeNull();
      expect(result.current.pendingPanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });
    });

    it("works when switching from agent to issue", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.activePanel).toBeNull();

      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-1",
      });
    });
  });

  describe("closePanel", () => {
    it("sets activePanel to null", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      act(() => {
        result.current.closePanel();
      });

      expect(result.current.activePanel).toBeNull();
    });

    it("is a no-op when no panel is active", () => {
      const { result } = renderHook(() => usePanelManager());

      // Should not throw.
      act(() => {
        result.current.closePanel();
      });

      expect(result.current.activePanel).toBeNull();
    });

    it("cancels any pending transition timeout", () => {
      const { result } = renderHook(() => usePanelManager());

      // Start a cross-type transition.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Close during the transition.
      act(() => {
        result.current.closePanel();
      });

      expect(result.current.activePanel).toBeNull();

      // After the transition time, nothing should have opened (timeout was cancelled).
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toBeNull();
    });
  });

  describe("rapid openPanel calls (debouncing)", () => {
    it("only the final panel is active after timeout", () => {
      const { result } = renderHook(() => usePanelManager());

      // Open an issue panel first.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      // Rapid cross-type clicks: issue -> agent-alpha -> issue-2 -> agent-beta
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // At this point activePanel is null and pendingPanel is agent-alpha.
      // Now switch again before the timeout fires.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-2" });
      });

      // Since activePanel is null (closing), and we request an issue panel,
      // the timeout should be cancelled, and issue-2 opens immediately.
      // Actually, activePanel is null so the "no panel active" branch runs.
      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-2",
      });

      // Now switch to agent-beta while issue-2 is active.
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-beta" });
      });

      // Should be in transition.
      expect(result.current.activePanel).toBeNull();
      expect(result.current.pendingPanel).toEqual({
        type: "agent",
        name: "agent-beta",
      });

      // After transition, only agent-beta should be active.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-beta",
      });
      expect(result.current.pendingPanel).toBeNull();
    });

    it("cancels intermediate timeouts during rapid cross-type switches", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      // First cross-type switch: issue -> agent-alpha
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Partially advance time (not enough to trigger the timeout).
      act(() => {
        vi.advanceTimersByTime(100);
      });

      expect(result.current.activePanel).toBeNull();

      // The pending timeout fires and sets agent-alpha as active.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS - 100);
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });

      // Now switch back to issue while agent-alpha is active.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-3" });
      });

      // In transition, activePanel null again.
      expect(result.current.activePanel).toBeNull();

      // Switch to a different agent before timeout fires (cancels issue-3).
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-gamma" });
      });

      // Since activePanel is null, agent-gamma opens immediately.
      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-gamma",
      });

      // Ensure no stale timeouts fire.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toEqual({
        type: "agent",
        name: "agent-gamma",
      });
    });
  });

  describe("openPanel during close animation", () => {
    it("cancels close timeout and opens new panel immediately when no panel active", () => {
      const { result } = renderHook(() => usePanelManager());

      // Open issue, then switch to agent (starts close animation).
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // activePanel is null, pending is agent-alpha.
      expect(result.current.activePanel).toBeNull();

      // Open a different panel during the close animation.
      // Since activePanel is null, it opens immediately.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-2" });
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-2",
      });
      expect(result.current.pendingPanel).toBeNull();

      // Make sure the original timeout does not interfere.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-2",
      });
    });
  });

  describe("unmount during timeout", () => {
    it("does not throw or cause state updates after unmount", () => {
      const { result, unmount } = renderHook(() => usePanelManager());

      // Start a cross-type transition.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Unmount before the timeout fires.
      unmount();

      // Advance timers past the transition -- should not throw.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });
    });

    it("clears the pending timeout on unmount", () => {
      const clearTimeoutSpy = vi.spyOn(global, "clearTimeout");

      const { result, unmount } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      clearTimeoutSpy.mockClear();

      unmount();

      expect(clearTimeoutSpy).toHaveBeenCalled();

      clearTimeoutSpy.mockRestore();
    });
  });

  describe("isOpen", () => {
    it("returns false when no panel is active", () => {
      const { result } = renderHook(() => usePanelManager());

      expect(result.current.isOpen("issue")).toBe(false);
      expect(result.current.isOpen("agent")).toBe(false);
    });

    it("returns true for matching type when no id specified", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.isOpen("issue")).toBe(true);
      expect(result.current.isOpen("agent")).toBe(false);
    });

    it("returns true for matching type and id (issue)", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.isOpen("issue", "issue-1")).toBe(true);
      expect(result.current.isOpen("issue", "issue-2")).toBe(false);
    });

    it("returns true for matching type and name (agent)", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      expect(result.current.isOpen("agent", "agent-alpha")).toBe(true);
      expect(result.current.isOpen("agent", "agent-beta")).toBe(false);
    });

    it("returns false for wrong type even with matching id", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "shared-id" });
      });

      expect(result.current.isOpen("agent", "shared-id")).toBe(false);
    });

    it("updates after panel transitions", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.isOpen("issue", "issue-1")).toBe(true);

      // Start cross-type transition.
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // During transition, nothing is "open".
      expect(result.current.isOpen("issue")).toBe(false);
      expect(result.current.isOpen("agent")).toBe(false);

      // After transition completes.
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.isOpen("agent", "agent-alpha")).toBe(true);
      expect(result.current.isOpen("issue")).toBe(false);
    });
  });

  describe("pendingPanel", () => {
    it("is null when no transition is in progress", () => {
      const { result } = renderHook(() => usePanelManager());

      expect(result.current.pendingPanel).toBeNull();

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.pendingPanel).toBeNull();
    });

    it("reflects the queued panel during a cross-type transition", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      expect(result.current.pendingPanel).toEqual({
        type: "agent",
        name: "agent-alpha",
      });
    });

    it("is cleared after transition completes", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.pendingPanel).toBeNull();
    });
  });

  describe("edge cases", () => {
    it("handles open -> close -> open of same panel", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.closePanel();
      });

      expect(result.current.activePanel).toBeNull();

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-1",
      });
    });

    it("handles multiple same-type swaps in sequence", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-2" });
      });
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-3" });
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-3",
      });
      expect(result.current.pendingPanel).toBeNull();
    });

    it("handles close during transition followed by new open", () => {
      const { result } = renderHook(() => usePanelManager());

      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-1" });
      });
      act(() => {
        result.current.openPanel({ type: "agent", name: "agent-alpha" });
      });

      // Close cancels the pending transition.
      act(() => {
        result.current.closePanel();
      });

      expect(result.current.activePanel).toBeNull();

      // After timeout, nothing should open (transition was cancelled).
      act(() => {
        vi.advanceTimersByTime(TRANSITION_MS);
      });

      expect(result.current.activePanel).toBeNull();

      // New open should work normally.
      act(() => {
        result.current.openPanel({ type: "issue", id: "issue-2" });
      });

      expect(result.current.activePanel).toEqual({
        type: "issue",
        id: "issue-2",
      });
    });
  });
});
