/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";

import { useDebouncedCallback } from "../useDebouncedCallback";

describe("useDebouncedCallback", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("delays invocation by the specified delay", () => {
    const callback = vi.fn();
    const { result } = renderHook(() => useDebouncedCallback(callback, 300));

    result.current("arg1");

    expect(callback).not.toHaveBeenCalled();

    vi.advanceTimersByTime(300);

    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith("arg1");
  });

  it("resets timer on rapid successive calls — only the last call fires", () => {
    const callback = vi.fn();
    const { result } = renderHook(() => useDebouncedCallback(callback, 300));

    result.current("first");
    vi.advanceTimersByTime(100);
    result.current("second");
    vi.advanceTimersByTime(100);
    result.current("third");

    // No calls yet
    expect(callback).not.toHaveBeenCalled();

    vi.advanceTimersByTime(300);

    // Only the last call fires
    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith("third");
  });

  it("cleans up timeout on unmount (no call after unmount)", () => {
    const callback = vi.fn();
    const { result, unmount } = renderHook(() =>
      useDebouncedCallback(callback, 300),
    );

    result.current("arg");

    unmount();

    vi.advanceTimersByTime(300);

    expect(callback).not.toHaveBeenCalled();
  });

  it("returns a stable function reference across re-renders", () => {
    const callback = vi.fn();
    const { result, rerender } = renderHook(() =>
      useDebouncedCallback(callback, 300),
    );

    const firstRef = result.current;
    rerender();
    const secondRef = result.current;

    expect(firstRef).toBe(secondRef);
  });

  it("uses latest callback (not stale closure)", () => {
    let counter = 0;
    const callback1 = vi.fn(() => counter++);
    const callback2 = vi.fn(() => (counter += 10));

    const { result, rerender } = renderHook(
      ({ cb }) => useDebouncedCallback(cb, 300),
      { initialProps: { cb: callback1 as (...args: unknown[]) => void } },
    );

    result.current();

    // Switch to callback2 before timer fires
    rerender({ cb: callback2 as (...args: unknown[]) => void });

    vi.advanceTimersByTime(300);

    // Should use the latest callback (callback2), not the stale one
    expect(callback1).not.toHaveBeenCalled();
    expect(callback2).toHaveBeenCalledTimes(1);
    expect(counter).toBe(10);
  });

  it("fires multiple separate calls when delay elapses between them", () => {
    const callback = vi.fn();
    const { result } = renderHook(() => useDebouncedCallback(callback, 300));

    result.current("first");
    vi.advanceTimersByTime(300);
    expect(callback).toHaveBeenCalledTimes(1);
    expect(callback).toHaveBeenCalledWith("first");

    result.current("second");
    vi.advanceTimersByTime(300);
    expect(callback).toHaveBeenCalledTimes(2);
    expect(callback).toHaveBeenCalledWith("second");
  });
});
