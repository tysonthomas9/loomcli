/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for usePollingWithBackoff hook.
 *
 * Verifies countdown timer, exponential backoff progression, retryNow,
 * reportSuccess, stale banner, connectionLost, forceRetry, visibility
 * change re-polling, unmount cleanup, and enabled flag.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { usePollingWithBackoff } from "../usePollingWithBackoff";

/** Helper to flush pending microtasks. */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("usePollingWithBackoff", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("countdown", () => {
    it("starts retryCountdown at initialDelay after reportFailure", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      // Set wasEverConnected so reportFailure schedules retry
      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });

      expect(result.current.retryCountdown).toBe(5);
    });

    it("decrements retryCountdown every 1s", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });

      expect(result.current.retryCountdown).toBe(5);

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      expect(result.current.retryCountdown).toBe(4);

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      expect(result.current.retryCountdown).toBe(3);

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      expect(result.current.retryCountdown).toBe(2);

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      expect(result.current.retryCountdown).toBe(1);
    });

    it("calls onRetry when countdown expires", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      act(() => {
        result.current.reportSuccess();
      });
      onRetry.mockClear();

      act(() => {
        result.current.reportFailure();
      });

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(onRetry).toHaveBeenCalledTimes(1);
    });
  });

  describe("backoff progression", () => {
    it("doubles delay on consecutive failures up to maxDelay", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      act(() => {
        result.current.reportSuccess();
      });

      // Sequence: 5 -> 10 -> 20 -> 40 -> 60 (capped)
      const expectedDelays = [5, 10, 20, 40, 60];

      for (const delay of expectedDelays) {
        act(() => {
          result.current.reportFailure();
        });

        expect(result.current.retryCountdown).toBe(delay);

        // Advance to let the retry fire
        await act(async () => {
          vi.advanceTimersByTime(delay * 1000);
        });
        await flushPromises();
      }

      // One more failure should still be capped at 60
      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(60);
    });
  });

  describe("retryNow", () => {
    it("clears countdown, resets backoff, and triggers onRetry", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      act(() => {
        result.current.reportSuccess();
      });
      onRetry.mockClear();

      // Cause failure so backoff is scheduled
      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(5);

      // Let first retry fire to escalate backoff to 10
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(10);

      onRetry.mockClear();

      // retryNow should reset and immediately trigger onRetry
      act(() => {
        result.current.retryNow();
      });

      expect(result.current.retryCountdown).toBe(0);
      expect(onRetry).toHaveBeenCalledTimes(1);

      // Next failure should use initial delay again (backoff was reset)
      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(5);
    });

    it("clears connectionLost state", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          maxFailuresAtCeiling: 2,
          maxDelay: 5,
          initialDelay: 5,
        }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      // Two failures at max delay → connectionLost
      act(() => {
        result.current.reportFailure();
      });

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      act(() => {
        result.current.reportFailure();
      });

      expect(result.current.connectionLost).toBe(true);

      act(() => {
        result.current.retryNow();
      });

      expect(result.current.connectionLost).toBe(false);
    });
  });

  describe("reportSuccess", () => {
    it("clears all error state", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          staleBannerDelayMs: 100,
          maxFailuresAtCeiling: 1,
          maxDelay: 5,
          initialDelay: 5,
        }),
      );

      // Establish connection then fail
      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(5);
      expect(result.current.disconnectedSince).not.toBeNull();

      // Let stale banner timer fire
      await act(async () => {
        vi.advanceTimersByTime(100);
      });
      expect(result.current.showStaleBanner).toBe(true);

      // Let retry fire at ceiling to trigger connectionLost
      await act(async () => {
        vi.advanceTimersByTime(4900);
      });
      await flushPromises();

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.connectionLost).toBe(true);

      // Now report success - everything should reset
      act(() => {
        result.current.reportSuccess();
      });

      expect(result.current.retryCountdown).toBe(0);
      expect(result.current.showStaleBanner).toBe(false);
      expect(result.current.connectionLost).toBe(false);
      expect(result.current.disconnectedSince).toBeNull();
      expect(result.current.wasEverConnected).toBe(true);
    });
  });

  describe("stale banner", () => {
    it("appears after staleBannerDelayMs of continuous failure", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          staleBannerDelayMs: 3000,
        }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });

      // Before the stale delay
      expect(result.current.showStaleBanner).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(2999);
      });
      expect(result.current.showStaleBanner).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(1);
      });
      expect(result.current.showStaleBanner).toBe(true);
    });

    it("does not appear when wasEverConnected is false", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          staleBannerDelayMs: 100,
        }),
      );

      // reportFailure without ever connecting
      act(() => {
        result.current.reportFailure({ forceRetry: true });
      });

      await act(async () => {
        vi.advanceTimersByTime(200);
      });

      expect(result.current.showStaleBanner).toBe(false);
    });

    it("is cleared by reportSuccess", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          staleBannerDelayMs: 100,
        }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });

      await act(async () => {
        vi.advanceTimersByTime(100);
      });
      expect(result.current.showStaleBanner).toBe(true);

      act(() => {
        result.current.reportSuccess();
      });
      expect(result.current.showStaleBanner).toBe(false);
    });
  });

  describe("connectionLost", () => {
    it("is set after maxFailuresAtCeiling consecutive failures at max delay", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          maxFailuresAtCeiling: 3,
          maxDelay: 5,
          initialDelay: 5,
        }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      // All failures are at ceiling (initialDelay === maxDelay = 5)
      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.connectionLost).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.connectionLost).toBe(false);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.connectionLost).toBe(true);
    });

    it("is not set when maxFailuresAtCeiling is 0 (disabled)", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({
          onRetry,
          maxFailuresAtCeiling: 0,
          maxDelay: 5,
          initialDelay: 5,
        }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      for (let i = 0; i < 10; i++) {
        act(() => {
          result.current.reportFailure();
        });
        await act(async () => {
          vi.advanceTimersByTime(5000);
        });
        await flushPromises();
      }

      expect(result.current.connectionLost).toBe(false);
    });
  });

  describe("forceRetry", () => {
    it("schedules retry even when wasEverConnected is false", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      expect(result.current.wasEverConnected).toBe(false);

      act(() => {
        result.current.reportFailure({ forceRetry: true });
      });

      expect(result.current.retryCountdown).toBe(5);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(onRetry).toHaveBeenCalled();
    });
  });

  describe("no retry without wasEverConnected", () => {
    it("does not schedule retry when wasEverConnected is false and forceRetry is not set", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() => usePollingWithBackoff({ onRetry }));

      expect(result.current.wasEverConnected).toBe(false);

      act(() => {
        result.current.reportFailure();
      });

      expect(result.current.retryCountdown).toBe(0);

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(onRetry).not.toHaveBeenCalled();
    });
  });

  describe("unmount cleanup", () => {
    it("clears timers on unmount and does not setState after", async () => {
      const clearTimeoutSpy = vi.spyOn(globalThis, "clearTimeout");
      const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");

      const onRetry = vi.fn();
      const { result, unmount } = renderHook(() =>
        usePollingWithBackoff({ onRetry }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });
      expect(result.current.retryCountdown).toBe(5);

      clearTimeoutSpy.mockClear();
      clearIntervalSpy.mockClear();

      unmount();

      expect(clearTimeoutSpy).toHaveBeenCalled();
      expect(clearIntervalSpy).toHaveBeenCalled();

      // Advancing timers after unmount should not throw
      await act(async () => {
        vi.advanceTimersByTime(60000);
      });
      await flushPromises();

      clearTimeoutSpy.mockRestore();
      clearIntervalSpy.mockRestore();
    });

    it("reportSuccess and reportFailure are no-ops after unmount", async () => {
      const onRetry = vi.fn();
      const { result, unmount } = renderHook(() =>
        usePollingWithBackoff({ onRetry }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      unmount();

      // These should not throw — mountedRef guards them
      result.current.reportSuccess();
      result.current.reportFailure();

      // onRetry should not be called
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
    });
  });

  describe("visibility change", () => {
    it("calls retryNow when tab becomes visible and disconnected", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({ onRetry, repollOnVisibilityChange: true }),
      );

      // Establish connection then disconnect
      act(() => {
        result.current.reportSuccess();
      });
      onRetry.mockClear();

      act(() => {
        result.current.reportFailure();
      });

      expect(result.current.disconnectedSince).not.toBeNull();

      onRetry.mockClear();

      // Simulate tab becoming visible
      await act(async () => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      // retryNow was called, which calls onRetry
      expect(onRetry).toHaveBeenCalledTimes(1);
      expect(result.current.retryCountdown).toBe(0);
    });

    it("does not re-poll when not disconnected", async () => {
      const onRetry = vi.fn();
      renderHook(() =>
        usePollingWithBackoff({ onRetry, repollOnVisibilityChange: true }),
      );

      // reportSuccess sets wasEverConnected but disconnectedSince stays null
      onRetry.mockClear();

      await act(async () => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      expect(onRetry).not.toHaveBeenCalled();
    });

    it("does not re-poll when repollOnVisibilityChange is false", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({ onRetry, repollOnVisibilityChange: false }),
      );

      act(() => {
        result.current.reportSuccess();
      });
      act(() => {
        result.current.reportFailure();
      });
      onRetry.mockClear();

      await act(async () => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      expect(onRetry).not.toHaveBeenCalled();
    });
  });

  describe("disabled", () => {
    it("does not schedule retries when enabled is false", async () => {
      const onRetry = vi.fn();
      const { result } = renderHook(() =>
        usePollingWithBackoff({ onRetry, enabled: false }),
      );

      act(() => {
        result.current.reportSuccess();
      });

      act(() => {
        result.current.reportFailure();
      });

      // scheduleRetry returns early when enabled=false
      expect(result.current.retryCountdown).toBe(0);

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(onRetry).not.toHaveBeenCalled();
    });
  });
});
