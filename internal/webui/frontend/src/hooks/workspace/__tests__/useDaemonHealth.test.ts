/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useDaemonHealth hook.
 *
 * Verifies initial health check, state transitions, exponential backoff,
 * retryNow behavior, visibility change re-polling, daemon-unavailable
 * custom events, and debounce logic.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { notifyDaemonUnavailable } from "@/api/client";
import { checkDaemonHealth } from "@/api/health";

import { useDaemonHealth } from "../useDaemonHealth";

// Mock the health API
vi.mock("@/api/health", () => ({
  checkDaemonHealth: vi.fn(),
}));

const mockGet = vi.mocked(checkDaemonHealth);

/** Helper to create a successful health response. */
function healthOk() {
  return { status: "ok" as const, daemon: { connected: true } };
}

/** Helper to create a failed/degraded health response. */
function healthDegraded(error?: string) {
  return {
    status: "degraded" as const,
    daemon: { connected: false, error },
  };
}

/** Helper to flush pending microtasks. */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useDaemonHealth", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockGet.mockReset();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  describe("initial health check", () => {
    it("performs a health check on mount", async () => {
      mockGet.mockResolvedValueOnce(healthOk());

      renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(mockGet).toHaveBeenCalledTimes(1);
      expect(mockGet).toHaveBeenCalledWith();
    });

    it("returns connected state on successful health check", async () => {
      mockGet.mockResolvedValueOnce(healthOk());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.wasEverConnected).toBe(true);
      expect(result.current.connectionMode).toBe("connected");
      expect(result.current.lastError).toBeNull();
      expect(result.current.retryCountdown).toBe(0);
    });

    it("returns never_connected on initial health check failure", async () => {
      mockGet.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(false);
      expect(result.current.wasEverConnected).toBe(false);
      expect(result.current.connectionMode).toBe("never_connected");
      expect(result.current.lastError).toBe("Network error");
    });

    it("sets isChecking during health check", async () => {
      let resolveHealth!: (val: unknown) => void;
      mockGet.mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveHealth = resolve;
          }),
      );

      const { result } = renderHook(() => useDaemonHealth());

      // Should be checking during the request
      expect(result.current.isChecking).toBe(true);

      await act(async () => {
        resolveHealth(healthOk());
      });

      expect(result.current.isChecking).toBe(false);
    });
  });

  describe("state transitions", () => {
    it("transitions from never_connected to connected", async () => {
      // First check fails
      mockGet.mockRejectedValueOnce(new Error("Connection refused"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.connectionMode).toBe("never_connected");
      expect(result.current.isDaemonAvailable).toBe(false);

      // Next check succeeds (triggered by retry)
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.connectionMode).toBe("connected");
      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.wasEverConnected).toBe(true);
    });

    it("transitions from connected to reconnecting on failure", async () => {
      // First check succeeds
      mockGet.mockResolvedValueOnce(healthOk());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.connectionMode).toBe("connected");

      // Second check fails (simulate via daemon-unavailable event)
      mockGet.mockRejectedValueOnce(new Error("Daemon stopped"));

      // Dispatch daemon-unavailable event to trigger a re-check
      await act(async () => {
        notifyDaemonUnavailable();
      });
      await flushPromises();

      // After debounce period, the unavailable state should be applied
      // Advance past the debounce (2s)
      // The first failure starts the unavailable timer. Need a second failure
      // after the debounce to actually show overlay.
      mockGet.mockRejectedValueOnce(new Error("Daemon stopped"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(false);
      expect(result.current.connectionMode).toBe("reconnecting");
    });

    it("transitions from reconnecting back to connected", async () => {
      // First check succeeds
      mockGet.mockResolvedValueOnce(healthOk());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Second check fails
      mockGet.mockRejectedValueOnce(new Error("Daemon stopped"));

      await act(async () => {
        notifyDaemonUnavailable();
      });
      await flushPromises();

      // Advance past debounce and let retry fire
      mockGet.mockRejectedValueOnce(new Error("Still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.connectionMode).toBe("reconnecting");

      // Now recover via retryNow
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.connectionMode).toBe("connected");
      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.lastError).toBeNull();
    });
  });

  describe("exponential backoff", () => {
    it("schedules retry with initial 5s delay after failure", async () => {
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // On initial failure (never_connected, no debounce), retry is scheduled
      expect(result.current.retryCountdown).toBe(5);
    });

    it("doubles retry delay on subsequent failures up to 60s cap", async () => {
      // Initial failure
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // First retry delay = 5s
      expect(result.current.retryCountdown).toBe(5);

      // Let the retry fire at 5s -> next delay should be 10s
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(10);

      // Let the retry fire at 10s -> next delay should be 20s
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(20);

      // Let the retry fire at 20s -> next delay should be 40s
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(20000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(40);

      // Let the retry fire at 40s -> next delay should be capped at 60s
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(40000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(60);
    });

    it("countdown decrements every second", async () => {
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

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

      // At 5s, the retry timeout fires and schedules a new check.
      // Mock the next health check so the retry can proceed.
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      // After retry fires, a new countdown starts at 10s (backoff doubled)
      expect(result.current.retryCountdown).toBe(10);
    });
  });

  describe("retryNow", () => {
    it("resets backoff and triggers immediate retry", async () => {
      // Initial failure
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Let first retry fire to escalate backoff to 10s
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(10);

      // Now retryNow should reset and immediately check
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.retryCountdown).toBe(0);
      expect(result.current.connectionMode).toBe("connected");
    });

    it("resets backoff delay to initial value after retryNow", async () => {
      // Initial failure
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Escalate backoff
      mockGet.mockRejectedValueOnce(new Error("down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(10);

      // retryNow with failure should reset to 5s backoff
      mockGet.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(5);
    });
  });

  describe("visibility change", () => {
    it("re-polls when tab becomes visible and daemon is unavailable", async () => {
      // Initial failure
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(false);

      const callCountBefore = mockGet.mock.calls.length;

      // Simulate tab becoming visible
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        Object.defineProperty(document, "visibilityState", {
          value: "visible",
          writable: true,
          configurable: true,
        });
        document.dispatchEvent(new Event("visibilitychange"));
      });
      await flushPromises();

      expect(mockGet.mock.calls.length).toBeGreaterThan(callCountBefore);
      expect(result.current.isDaemonAvailable).toBe(true);
    });

    it("does not re-poll when tab becomes visible and daemon is available", async () => {
      // Initial success
      mockGet.mockResolvedValueOnce(healthOk());

      renderHook(() => useDaemonHealth());

      await flushPromises();

      const callCountBefore = mockGet.mock.calls.length;

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

      // No additional calls since daemon is available
      expect(mockGet.mock.calls.length).toBe(callCountBefore);
    });
  });

  describe("daemon-unavailable events", () => {
    it("triggers health check on daemon-unavailable event when daemon is available", async () => {
      // Initially connected
      mockGet.mockResolvedValueOnce(healthOk());

      renderHook(() => useDaemonHealth());

      await flushPromises();

      const callCountBefore = mockGet.mock.calls.length;

      // Dispatch daemon-unavailable event
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        notifyDaemonUnavailable();
      });
      await flushPromises();

      expect(mockGet.mock.calls.length).toBeGreaterThan(callCountBefore);
    });

    it("does not trigger redundant health check when daemon already unavailable", async () => {
      // Initial failure -> daemon unavailable
      mockGet.mockRejectedValueOnce(new Error("down"));

      renderHook(() => useDaemonHealth());

      await flushPromises();

      const callCountBefore = mockGet.mock.calls.length;

      // Dispatch daemon-unavailable event while already unavailable
      await act(async () => {
        notifyDaemonUnavailable();
      });
      await flushPromises();

      // Should not trigger another check since already unavailable
      expect(mockGet.mock.calls.length).toBe(callCountBefore);
    });
  });

  describe("debounce", () => {
    it("shows overlay immediately for never-connected state (no debounce)", async () => {
      mockGet.mockRejectedValueOnce(new Error("Cannot connect"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Should immediately show as unavailable (first check = never-connected, no debounce)
      expect(result.current.isDaemonAvailable).toBe(false);
      expect(result.current.connectionMode).toBe("never_connected");
    });

    it("debounces before showing overlay for previously connected daemon", async () => {
      // Start connected
      mockGet.mockResolvedValueOnce(healthOk());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(true);

      // Now fail - should not immediately mark unavailable due to debounce
      mockGet.mockRejectedValueOnce(new Error("Blip"));

      await act(async () => {
        notifyDaemonUnavailable();
      });
      await flushPromises();

      // Still available due to debounce (failure < 2s ago)
      expect(result.current.isDaemonAvailable).toBe(true);
    });
  });

  describe("degraded response handling", () => {
    it("treats degraded daemon.connected=false as unavailable", async () => {
      mockGet.mockResolvedValueOnce(healthDegraded("Low memory"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(false);
      expect(result.current.lastError).toBe("Low memory");
    });

    it("treats response with status=ok and daemon.connected=true as available", async () => {
      mockGet.mockResolvedValueOnce(healthOk());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.connectionMode).toBe("connected");
    });

    it("uses default error message for degraded response without error field", async () => {
      mockGet.mockResolvedValueOnce(healthDegraded());

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.lastError).toBe("Daemon is degraded");
    });
  });

  describe("error messages", () => {
    it("extracts message from Error instances", async () => {
      mockGet.mockRejectedValueOnce(new Error("ECONNREFUSED"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.lastError).toBe("ECONNREFUSED");
    });

    it("uses fallback message for non-Error exceptions", async () => {
      mockGet.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      expect(result.current.lastError).toBe("Failed to reach daemon");
    });
  });

  describe("cleanup on unmount", () => {
    it("clears timers on unmount", async () => {
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { unmount } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Timers are now scheduled (retry and countdown)
      unmount();

      // Advancing timers after unmount should not throw
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        vi.advanceTimersByTime(60000);
      });

      // If we got here without errors, cleanup worked
    });
  });

  describe("successful recovery clears error and countdown", () => {
    it("recovery after retryNow clears all error state", async () => {
      // Reset mock to ensure clean state (guard against leakage from prior tests)
      mockGet.mockReset();

      // Start with failure to get into never_connected state
      mockGet.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useDaemonHealth());

      await flushPromises();

      // Verify we're in a failed state (never_connected shows immediately)
      expect(result.current.connectionMode).toBe("never_connected");

      // Now recovery via retryNow
      mockGet.mockResolvedValueOnce(healthOk());

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      // All error state should be cleared
      expect(result.current.lastError).toBeNull();
      expect(result.current.retryCountdown).toBe(0);
      expect(result.current.isDaemonAvailable).toBe(true);
      expect(result.current.connectionMode).toBe("connected");
      expect(result.current.wasEverConnected).toBe(true);
    });
  });
});
