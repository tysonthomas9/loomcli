/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceRepos hook.
 *
 * Verifies connection state transitions, stale data preservation,
 * auto-retry with exponential backoff, retryNow behavior,
 * countdown decrement, max attempt exhaustion, and cleanup on unmount.
 */

import { renderHook, act } from "@testing-library/react";
import React from "react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchWorkspaceApi } from "@/api/workspace";
import type { WorkspaceData } from "@/api/workspace";
import { calculateBackoffDelay } from "@/utils/reconnectBackoff";

import { useWorkspaceRepos } from "../useWorkspaceRepos";

// Mock the workspace API
vi.mock("@/api/workspace", () => ({
  fetchWorkspaceApi: vi.fn(),
}));

// Mock calculateBackoffDelay so delays are deterministic
vi.mock("@/utils/reconnectBackoff", () => ({
  calculateBackoffDelay: vi.fn(),
}));

const mockFetchWorkspace = vi.mocked(fetchWorkspaceApi);
const mockCalculateBackoff = vi.mocked(calculateBackoffDelay);

/** Helper to create a mock WorkspaceData. */
function createWorkspaceData(
  overrides?: Partial<WorkspaceData>,
): WorkspaceData {
  return {
    name: "test-workspace",
    path: "/workspaces/test",
    repos: [
      {
        name: "alpha",
        path: "/repos/alpha",
        default_branch: "main",
        remote: "origin",
        groups: [],
      },
    ],
    groups: [],
    agents: [],
    workspaces: [],
    ...overrides,
  };
}

/** Helper to flush pending microtasks. */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useWorkspaceRepos", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockFetchWorkspace.mockReset();
    mockCalculateBackoff.mockReset();
    // Default: return 5000ms delay for all attempts
    mockCalculateBackoff.mockReturnValue(5000);
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.useRealTimers();
  });

  describe("state transitions", () => {
    it("transitions from loading to connected on successful fetch", async () => {
      const data = createWorkspaceData();
      mockFetchWorkspace.mockResolvedValueOnce(data);

      const { result } = renderHook(() => useWorkspaceRepos());

      // Initially loading
      expect(result.current.connectionState).toBe("loading");
      expect(result.current.isLoading).toBe(true);

      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.isLoading).toBe(false);
      expect(result.current.workspace).toEqual(data);
      expect(result.current.repos).toEqual(data.repos);
      expect(result.current.error).toBeNull();
      expect(result.current.hasEverConnected).toBe(true);
    });

    it("transitions from loading to error_never_connected on failure", async () => {
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("error_never_connected");
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe("Network error");
      expect(result.current.workspace).toBeNull();
      expect(result.current.repos).toEqual([]);
      expect(result.current.hasEverConnected).toBe(false);
    });

    it("transitions from connected to error_lost_connection when refetch fails after prior success", async () => {
      const data = createWorkspaceData();

      // First fetch succeeds
      mockFetchWorkspace.mockResolvedValueOnce(data);

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.hasEverConnected).toBe(true);

      // Trigger refetch that fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Connection lost"));

      await act(async () => {
        result.current.refetch();
      });
      await flushPromises();

      expect(result.current.connectionState).toBe("error_lost_connection");
      expect(result.current.error).toBe("Connection lost");
    });
  });

  describe("stale data preservation", () => {
    it("preserves workspace data when transitioning to error_lost_connection", async () => {
      const data = createWorkspaceData({
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
          {
            name: "beta",
            path: "/repos/beta",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      });

      // First fetch succeeds
      mockFetchWorkspace.mockResolvedValueOnce(data);

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.workspace).toEqual(data);
      expect(result.current.repos).toHaveLength(2);

      // Refetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("Server down"));

      await act(async () => {
        result.current.refetch();
      });
      await flushPromises();

      // Workspace data should still be preserved
      expect(result.current.connectionState).toBe("error_lost_connection");
      expect(result.current.workspace).toEqual(data);
      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("alpha");
      expect(result.current.repos[1].name).toBe("beta");
    });
  });

  describe("auto-retry", () => {
    it("calls setTimeout with increasing delays after each failure", async () => {
      // Configure increasing delays
      mockCalculateBackoff
        .mockReturnValueOnce(5000)
        .mockReturnValueOnce(10000)
        .mockReturnValueOnce(20000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("error_never_connected");
      // attemptRef increments before scheduleRetry, so first call is attempt 1
      expect(mockCalculateBackoff).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ baseDelay: 5000, maxDelay: 60000 }),
      );

      // Advance to trigger retry at 5000ms
      mockFetchWorkspace.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // After second failure, attempt is 2
      expect(mockCalculateBackoff).toHaveBeenCalledWith(
        2,
        expect.objectContaining({ baseDelay: 5000, maxDelay: 60000 }),
      );

      // Advance to trigger retry at 10000ms
      mockFetchWorkspace.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      // After third failure, attempt is 3
      expect(mockCalculateBackoff).toHaveBeenCalledWith(
        3,
        expect.objectContaining({ baseDelay: 5000, maxDelay: 60000 }),
      );
    });

    it("auto-retry succeeds and transitions back to connected", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("error_never_connected");

      // Auto-retry succeeds
      const data = createWorkspaceData();
      mockFetchWorkspace.mockResolvedValueOnce(data);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.workspace).toEqual(data);
      expect(result.current.error).toBeNull();
    });
  });

  describe("retryNow", () => {
    it("clears timers, resets attempt count to 0, and calls fetchData", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("error_never_connected");

      // Let first auto-retry fire to escalate backoff
      mockFetchWorkspace.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // Now call retryNow with a successful response
      const data = createWorkspaceData();
      mockFetchWorkspace.mockResolvedValueOnce(data);

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.workspace).toEqual(data);
      expect(result.current.retryCountdown).toBeNull();
    });

    it("resets backoff delay to initial value when retryNow fails", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      // Let first auto-retry fire
      mockFetchWorkspace.mockRejectedValueOnce(new Error("still down"));

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // retryNow with failure should reset attempt to 0
      mockCalculateBackoff.mockClear();
      mockFetchWorkspace.mockRejectedValueOnce(new Error("still failing"));

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      // retryNow resets attemptRef to 0, fetchData fails, attemptRef
      // increments to 1, then scheduleRetry is called with attempt 1
      expect(mockCalculateBackoff).toHaveBeenCalledWith(
        1,
        expect.objectContaining({ baseDelay: 5000, maxDelay: 60000 }),
      );
    });
  });

  describe("countdown", () => {
    it("retryCountdown decrements each second", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      // Countdown should be set (approximately 5 seconds)
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
  });

  describe("max attempts", () => {
    it("stops auto-retry after 10 failed attempts", async () => {
      mockCalculateBackoff.mockReturnValue(1000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValue(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      // Exhaust all 10 attempts (initial + 9 retries)
      // The hook increments attempt after each failure and stops at maxAttempts=10
      for (let i = 0; i < 9; i++) {
        await act(async () => {
          vi.advanceTimersByTime(1000);
        });
        await flushPromises();
      }

      // After 10 failures (attempt 0 through 9), attemptRef is 10
      // which equals maxAttempts, so scheduleRetry should stop
      expect(result.current.retryCountdown).toBeNull();

      // Verify fetchWorkspace was called 10 times total (initial + 9 retries)
      expect(mockFetchWorkspace).toHaveBeenCalledTimes(10);

      // No more calls even after waiting
      await act(async () => {
        vi.advanceTimersByTime(100000);
      });
      expect(mockFetchWorkspace).toHaveBeenCalledTimes(10);
    });
  });

  describe("cleanup on unmount", () => {
    it("does not update state after unmount", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { unmount } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      // Timers are now scheduled (retry and countdown)
      unmount();

      // Advancing timers after unmount should not throw
      mockFetchWorkspace.mockResolvedValueOnce(createWorkspaceData());

      await act(async () => {
        vi.advanceTimersByTime(60000);
      });

      // If we got here without errors, cleanup worked
    });

    it("does not call fetchWorkspace after unmount when retry fires", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { unmount } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      const callCountBeforeUnmount = mockFetchWorkspace.mock.calls.length;

      // Unmount before retry timer fires
      unmount();

      // Advance past the retry timer
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });

      // fetchWorkspace should not have been called again
      expect(mockFetchWorkspace.mock.calls.length).toBe(callCountBeforeUnmount);
    });
  });

  describe("remount behavior (StrictMode compatibility)", () => {
    // StrictMode double-invokes effects: mount → cleanup → remount.
    // Without the fix, mountedRef.current stays false after cleanup,
    // causing all state updates in fetchData to be silently dropped.
    const strictWrapper = ({ children }: { children: React.ReactNode }) =>
      React.createElement(React.StrictMode, null, children);

    it("state updates work after StrictMode double-invoke cycle", async () => {
      const data = createWorkspaceData();
      // StrictMode causes two mount cycles — mock two successful responses
      mockFetchWorkspace
        .mockResolvedValueOnce(data)
        .mockResolvedValueOnce(data);

      const { result } = renderHook(() => useWorkspaceRepos(), {
        wrapper: strictWrapper,
      });

      await flushPromises();

      // After the double-invoke cycle, the hook should be connected
      expect(result.current.connectionState).toBe("connected");
      expect(result.current.workspace).toEqual(data);
      expect(result.current.isLoading).toBe(false);
    });

    it("auto-retry works after StrictMode double-invoke cycle", async () => {
      mockCalculateBackoff.mockReturnValue(3000);

      // StrictMode double-invoke causes two fetchData calls — both fail.
      // Each failure schedules a retry timer, so multiple timers may fire.
      // Use mockRejectedValue (not Once) to handle all initial calls,
      // then switch to resolved for the retry.
      mockFetchWorkspace.mockRejectedValue(new Error("fail"));

      const { result } = renderHook(() => useWorkspaceRepos(), {
        wrapper: strictWrapper,
      });

      await flushPromises();

      // After double-invoke, should be in error state with retry scheduled
      expect(result.current.connectionState).toBe("error_never_connected");
      expect(result.current.retryCountdown).not.toBeNull();

      // Switch mock to succeed for retries
      const data = createWorkspaceData();
      mockFetchWorkspace.mockResolvedValue(data);

      await act(async () => {
        vi.advanceTimersByTime(3000);
      });
      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.workspace).toEqual(data);
    });
  });

  describe("error message handling", () => {
    it("extracts message from Error instances", async () => {
      mockFetchWorkspace.mockRejectedValueOnce(new Error("ECONNREFUSED"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.error).toBe("ECONNREFUSED");
    });

    it("uses fallback message for non-Error exceptions", async () => {
      mockFetchWorkspace.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.error).toBe("Failed to load workspace data");
    });
  });

  describe("successful recovery clears error state", () => {
    it("clears error, countdown, and resets attempt on success after failure", async () => {
      mockCalculateBackoff.mockReturnValue(5000);

      // Initial fetch fails
      mockFetchWorkspace.mockRejectedValueOnce(new Error("down"));

      const { result } = renderHook(() => useWorkspaceRepos());

      await flushPromises();

      expect(result.current.connectionState).toBe("error_never_connected");
      expect(result.current.error).toBe("down");
      expect(result.current.retryCountdown).toBe(5);

      // Retry succeeds
      const data = createWorkspaceData();
      mockFetchWorkspace.mockResolvedValueOnce(data);

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.connectionState).toBe("connected");
      expect(result.current.error).toBeNull();
      expect(result.current.retryCountdown).toBeNull();
      expect(result.current.workspace).toEqual(data);
    });
  });
});
