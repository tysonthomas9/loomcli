/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useDebouncedCallback } from "./useDebouncedCallback";

describe("useDebouncedCallback", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("delays invocation by the specified delay", () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useDebouncedCallback(fn, 300));

    act(() => {
      result.current("a");
    });

    expect(fn).not.toHaveBeenCalled();

    act(() => {
      vi.advanceTimersByTime(300);
    });

    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("a");
  });

  it("resets timer on rapid successive calls — only the last fires", () => {
    const fn = vi.fn();
    const { result } = renderHook(() => useDebouncedCallback(fn, 300));

    act(() => {
      result.current("a");
    });

    act(() => {
      vi.advanceTimersByTime(100);
    });

    act(() => {
      result.current("b");
    });

    act(() => {
      vi.advanceTimersByTime(100);
    });

    act(() => {
      result.current("c");
    });

    // No calls yet
    expect(fn).not.toHaveBeenCalled();

    // Advance full delay from last call
    act(() => {
      vi.advanceTimersByTime(300);
    });

    expect(fn).toHaveBeenCalledTimes(1);
    expect(fn).toHaveBeenCalledWith("c");
  });

  it("returns a stable function reference across re-renders", () => {
    const fn = vi.fn();
    const { result, rerender } = renderHook(
      ({ delay }) => useDebouncedCallback(fn, delay),
      { initialProps: { delay: 300 } },
    );

    const ref1 = result.current;
    rerender({ delay: 300 });
    const ref2 = result.current;

    expect(ref1).toBe(ref2);
  });

  it("cleans up timeout on unmount — no call after unmount", () => {
    const fn = vi.fn();
    const { result, unmount } = renderHook(() => useDebouncedCallback(fn, 300));

    act(() => {
      result.current("a");
    });

    unmount();

    act(() => {
      vi.advanceTimersByTime(300);
    });

    expect(fn).not.toHaveBeenCalled();
  });

  it("uses latest callback (not stale closure)", () => {
    const fn1 = vi.fn();
    const fn2 = vi.fn();

    const { result, rerender } = renderHook(
      ({ cb }) => useDebouncedCallback(cb, 300),
      { initialProps: { cb: fn1 } },
    );

    act(() => {
      result.current("x");
    });

    // Update callback before debounce fires
    rerender({ cb: fn2 });

    act(() => {
      vi.advanceTimersByTime(300);
    });

    // Should call fn2, not fn1
    expect(fn1).not.toHaveBeenCalled();
    expect(fn2).toHaveBeenCalledWith("x");
  });
});
