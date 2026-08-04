/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useElapsedTime hook.
 * Verifies formatted elapsed time output for various durations,
 * null handling, interval cleanup, and clock skew.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useElapsedTime } from "../useElapsedTime";

describe("useElapsedTime", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('returns "" when startTimestamp is null', () => {
    const { result } = renderHook(() => useElapsedTime(null));

    expect(result.current).toBe("");
  });

  it('returns "0s" when startTimestamp is Date.now()', () => {
    const now = Date.now();
    const { result } = renderHook(() => useElapsedTime(now));

    expect(result.current).toBe("0s");
  });

  it('returns formatted seconds (e.g., "30s") after 30 seconds', () => {
    const now = Date.now();
    const { result } = renderHook(() => useElapsedTime(now));

    act(() => {
      vi.advanceTimersByTime(30_000);
    });

    expect(result.current).toBe("30s");
  });

  it('returns formatted minutes (e.g., "2m") after 120 seconds', () => {
    const now = Date.now();
    const { result } = renderHook(() => useElapsedTime(now));

    act(() => {
      vi.advanceTimersByTime(120_000);
    });

    expect(result.current).toBe("2m");
  });

  it('returns formatted hours+minutes (e.g., "1h 5m") after 3900 seconds', () => {
    const now = Date.now();
    const { result } = renderHook(() => useElapsedTime(now));

    act(() => {
      vi.advanceTimersByTime(3_900_000);
    });

    expect(result.current).toBe("1h 5m");
  });

  it("cleans up interval on unmount", () => {
    const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");

    const now = Date.now();
    const { unmount } = renderHook(() => useElapsedTime(now));

    unmount();

    expect(clearIntervalSpy).toHaveBeenCalled();

    clearIntervalSpy.mockRestore();
  });

  it('returns "0s" when startTimestamp is in the future (clock skew)', () => {
    const futureTimestamp = Date.now() + 60_000;
    const { result } = renderHook(() => useElapsedTime(futureTimestamp));

    expect(result.current).toBe("0s");
  });
});
