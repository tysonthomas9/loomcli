/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useUnreadTracking hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useUnreadTracking } from "../useUnreadTracking";

// ── Helpers ────────────────────────────────────────────────────────────────

function createOptions(
  overrides: Partial<Parameters<typeof useUnreadTracking>[0]> = {},
) {
  return {
    activeTabIdRef: { current: "tab-1" } as React.MutableRefObject<string>,
    isActive: true,
    onUnreadChange: undefined as ((hasAnyUnread: boolean) => void) | undefined,
    ...overrides,
  };
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("useUnreadTracking", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  // 1. handleOutput marks inactive tab as unread
  it("handleOutput marks inactive tab as unread", () => {
    const opts = createOptions({ activeTabIdRef: { current: "tab-1" } });
    const { result } = renderHook(() => useUnreadTracking(opts));

    act(() => {
      result.current.handleOutput("tab-2");
    });

    expect(result.current.tabUnread.get("tab-2")).toBe(true);
  });

  // 2. handleOutput skips active tab
  it("handleOutput skips active tab", () => {
    const opts = createOptions({ activeTabIdRef: { current: "tab-1" } });
    const { result } = renderHook(() => useUnreadTracking(opts));

    act(() => {
      result.current.handleOutput("tab-1");
    });

    expect(result.current.tabUnread.has("tab-1")).toBe(false);
  });

  // 3. handleOutput skips already-unread tab
  it("handleOutput skips already-unread tab (no extra state update)", () => {
    const opts = createOptions({ activeTabIdRef: { current: "tab-1" } });
    const { result } = renderHook(() => useUnreadTracking(opts));

    act(() => {
      result.current.handleOutput("tab-2");
    });
    const firstMap = result.current.tabUnread;

    act(() => {
      result.current.handleOutput("tab-2");
    });
    // Map reference should be identical — no state update
    expect(result.current.tabUnread).toBe(firstMap);
  });

  // 4. clearTabUnread removes unread flag
  it("clearTabUnread removes unread flag", () => {
    const opts = createOptions({ activeTabIdRef: { current: "tab-1" } });
    const { result } = renderHook(() => useUnreadTracking(opts));

    act(() => {
      result.current.handleOutput("tab-2");
    });
    expect(result.current.tabUnread.get("tab-2")).toBe(true);

    act(() => {
      result.current.clearTabUnread("tab-2");
    });
    expect(result.current.tabUnread.has("tab-2")).toBe(false);
  });

  // 5. onUnreadChange notified when unread changes
  it("onUnreadChange notified when unread changes", () => {
    const onUnreadChange = vi.fn();
    const opts = createOptions({
      activeTabIdRef: { current: "tab-1" },
      onUnreadChange,
    });
    const { result } = renderHook(() => useUnreadTracking(opts));

    // Initially called with false (no unread tabs)
    expect(onUnreadChange).toHaveBeenCalledWith(false);
    onUnreadChange.mockClear();

    act(() => {
      result.current.handleOutput("tab-2");
    });

    expect(onUnreadChange).toHaveBeenCalledWith(true);
  });

  it("onUnreadChange notified with false when all cleared", () => {
    const onUnreadChange = vi.fn();
    const opts = createOptions({
      activeTabIdRef: { current: "tab-1" },
      onUnreadChange,
    });
    const { result } = renderHook(() => useUnreadTracking(opts));

    act(() => {
      result.current.handleOutput("tab-2");
    });
    onUnreadChange.mockClear();

    act(() => {
      result.current.clearTabUnread("tab-2");
    });

    expect(onUnreadChange).toHaveBeenCalledWith(false);
  });

  // 6. Clear on activate (isActive false→true)
  it("clears unread on active tab when isActive transitions false→true", () => {
    const activeTabIdRef = { current: "tab-1" };
    const opts = createOptions({ activeTabIdRef, isActive: false });

    const { result, rerender } = renderHook(
      (props) => useUnreadTracking(props),
      { initialProps: opts },
    );

    // Mark tab-1 as unread (it's currently not active since isActive=false for the view)
    // First, make tab-1 not the active tab so handleOutput can mark it
    activeTabIdRef.current = "tab-2";
    act(() => {
      result.current.handleOutput("tab-1");
    });
    expect(result.current.tabUnread.get("tab-1")).toBe(true);

    // Now switch active tab back to tab-1 and activate view
    activeTabIdRef.current = "tab-1";
    rerender({ ...opts, isActive: true });

    // The unread flag on the active tab should be cleared
    expect(result.current.tabUnread.has("tab-1")).toBe(false);
  });

  it("does NOT clear unread when isActive stays true", () => {
    const activeTabIdRef = { current: "tab-2" };
    const opts = createOptions({ activeTabIdRef, isActive: true });

    const { result, rerender } = renderHook(
      (props) => useUnreadTracking(props),
      { initialProps: opts },
    );

    // Mark tab-1 as unread
    act(() => {
      result.current.handleOutput("tab-1");
    });
    expect(result.current.tabUnread.get("tab-1")).toBe(true);

    // Re-render with isActive still true
    rerender({ ...opts, isActive: true });

    // tab-1 should still be unread since we didn't transition false→true
    expect(result.current.tabUnread.get("tab-1")).toBe(true);
  });

  it("clearTabUnread is no-op for non-unread tab", () => {
    const opts = createOptions();
    const { result } = renderHook(() => useUnreadTracking(opts));

    const firstMap = result.current.tabUnread;

    act(() => {
      result.current.clearTabUnread("non-existent");
    });

    // Map should be same reference (no state update)
    expect(result.current.tabUnread).toBe(firstMap);
  });
});
