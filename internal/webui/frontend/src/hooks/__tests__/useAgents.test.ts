/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useAgents } from '../useAgents';
import type { FetchStatusResult } from '@/api/agents';
import type { LoomTaskLists } from '@/types';

// Mock the agent API functions
vi.mock('@/api', () => ({
  fetchStatus: vi.fn(),
  fetchTasks: vi.fn(),
}));

// Import the mocked functions for test manipulation
import { fetchStatus, fetchTasks } from '@/api';
const mockFetchStatus = vi.mocked(fetchStatus);
const mockFetchTasks = vi.mocked(fetchTasks);

/**
 * Helper to create a mock FetchStatusResult.
 */
function createMockStatusResult(overrides?: Partial<FetchStatusResult>): FetchStatusResult {
  return {
    agents: [],
    tasks: {
      needs_planning: 0,
      ready_to_implement: 0,
      in_progress: 0,
      need_review: 0,
      blocked: 0,
    },
    agentTasks: {},
    sync: {
      db_synced: true,
      db_last_sync: '',
      git_needs_push: 0,
      git_needs_pull: 0,
    },
    stats: {
      open: 0,
      closed: 0,
      total: 0,
      completion: 0,
    },
    timestamp: new Date().toISOString(),
    ...overrides,
  };
}

/**
 * Helper to create a mock LoomTaskLists.
 */
function createMockTaskLists(): LoomTaskLists {
  return {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    blocked: [],
  };
}

/**
 * Helper to flush pending promises when using fake timers.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

/**
 * Helper to setup mocks for a successful fetch followed by failures.
 * Returns a function to control when the initial fetch resolves.
 */
function setupSuccessThenFail() {
  const statusResult = createMockStatusResult();
  const taskLists = createMockTaskLists();

  // First call succeeds, subsequent calls fail
  mockFetchStatus.mockResolvedValueOnce(statusResult);
  mockFetchTasks.mockResolvedValueOnce(taskLists);

  return { statusResult, taskLists };
}

/**
 * Helper to make subsequent fetches fail.
 */
function makeNextFetchFail() {
  mockFetchStatus.mockRejectedValueOnce(new Error('Connection refused'));
  mockFetchTasks.mockRejectedValueOnce(new Error('Connection refused'));
}

describe('useAgents', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockFetchStatus.mockReset();
    mockFetchTasks.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe('retry scheduling race condition prevention', () => {
    it('creates only one set of retry timers even with rapid error state changes', async () => {
      // Track all setTimeout and setInterval calls
      const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout');
      const setIntervalSpy = vi.spyOn(globalThis, 'setInterval');

      // First fetch succeeds (establishes wasEverConnected = true)
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      // Wait for initial successful fetch
      await flushPromises();

      expect(result.current.isConnected).toBe(true);
      expect(result.current.wasEverConnected).toBe(true);

      // Clear spy call counts after initial setup
      setTimeoutSpy.mockClear();
      setIntervalSpy.mockClear();

      // Now make multiple fetches fail rapidly to trigger error state
      makeNextFetchFail();

      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      expect(result.current.isConnected).toBe(false);
      expect(result.current.error).toBeInstanceOf(Error);

      // Count retry-related timer calls (setTimeout with delay >= 1000ms is retry,
      // setInterval with 1000ms delay is countdown)
      const retryTimeouts = setTimeoutSpy.mock.calls.filter((call) => (call[1] as number) >= 1000);
      const retryIntervals = setIntervalSpy.mock.calls.filter(
        (call) => (call[1] as number) === 1000
      );

      // Should have exactly one retry timeout (5s delay) and one countdown interval (1s)
      expect(retryTimeouts.length).toBe(1);
      expect(retryIntervals.length).toBe(1);

      // The retry timeout should be 5000ms (INITIAL_RETRY_DELAY * 1000)
      expect(retryTimeouts[0][1]).toBe(5000);

      setTimeoutSpy.mockRestore();
      setIntervalSpy.mockRestore();
    });

    it('does not create duplicate timers when error changes while retry is already scheduled', async () => {
      const setTimeoutSpy = vi.spyOn(globalThis, 'setTimeout');
      const setIntervalSpy = vi.spyOn(globalThis, 'setInterval');

      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();
      expect(result.current.isConnected).toBe(true);

      // Clear spy counts
      setTimeoutSpy.mockClear();
      setIntervalSpy.mockClear();

      // First failure - triggers retry scheduling
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      const firstTimeoutCount = setTimeoutSpy.mock.calls.filter(
        (call) => (call[1] as number) >= 1000
      ).length;
      const firstIntervalCount = setIntervalSpy.mock.calls.filter(
        (call) => (call[1] as number) === 1000
      ).length;

      expect(firstTimeoutCount).toBe(1);
      expect(firstIntervalCount).toBe(1);

      // Second failure while retry is already scheduled - should NOT create more timers
      // The effect guard (retryTimeoutRef.current || retryIntervalRef.current) prevents this
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      const secondTimeoutCount = setTimeoutSpy.mock.calls.filter(
        (call) => (call[1] as number) >= 1000
      ).length;
      const secondIntervalCount = setIntervalSpy.mock.calls.filter(
        (call) => (call[1] as number) === 1000
      ).length;

      // After the second failure and refetch, the retry effect will run again.
      // The refetch clears retry timers, so the effect reschedules. That's expected.
      // But we should never see MORE than the expected count (one set per error transition
      // that doesn't have existing timers).
      // Each refetch that fails clears timers then the effect reschedules, so we get 2 total.
      expect(secondTimeoutCount).toBe(2);
      expect(secondIntervalCount).toBe(2);

      setTimeoutSpy.mockRestore();
      setIntervalSpy.mockRestore();
    });

    it('refs are set synchronously after timer creation to prevent race', async () => {
      // This test validates the core fix: refs are set in the same synchronous block
      // as timer creation. We verify this by checking that after the retry effect runs,
      // the retry countdown is properly set (which means the interval is running).

      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();
      expect(result.current.isConnected).toBe(true);

      // Trigger error
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      // retryCountdown should be set to INITIAL_RETRY_DELAY (5 seconds)
      expect(result.current.retryCountdown).toBe(5);

      // Advance 1 second - countdown should decrement
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(4);
    });
  });

  describe('retry countdown', () => {
    it('counts down from the initial delay after disconnect', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();
      expect(result.current.isConnected).toBe(true);
      expect(result.current.retryCountdown).toBe(0);

      // Trigger error
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      // Should start countdown at 5
      expect(result.current.retryCountdown).toBe(5);

      // Count down each second (stop before the last tick triggers the retry)
      for (let expected = 4; expected >= 2; expected--) {
        await act(async () => {
          vi.advanceTimersByTime(1000);
        });
        await flushPromises();
        expect(result.current.retryCountdown).toBe(expected);
      }

      // At countdown=2, advance 1 more second to reach 1
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();
      expect(result.current.retryCountdown).toBe(1);
    });

    it('triggers a retry fetch when countdown expires', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();
      expect(result.current.isConnected).toBe(true);

      // Trigger error
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      // Reset mock to track the retry call
      const callCountBeforeRetry = mockFetchStatus.mock.calls.length;

      // Make the retry also fail (so we can observe the retry happened)
      makeNextFetchFail();

      // Advance past the full retry delay (5 seconds)
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // fetchStatus should have been called again (the retry)
      expect(mockFetchStatus.mock.calls.length).toBeGreaterThan(callCountBeforeRetry);
    });

    it('uses exponential backoff for successive retries', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();
      expect(result.current.isConnected).toBe(true);

      // First failure - should schedule retry at 5s
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(5);

      // Let the retry fire (also fails) - next retry should be at 10s
      makeNextFetchFail();
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // After the retry fires and fails, a new countdown should start at 10
      // (5 * 2 = 10 due to BACKOFF_MULTIPLIER)
      expect(result.current.retryCountdown).toBe(10);

      // Let this retry fire (also fails) - next should be 20s
      makeNextFetchFail();
      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(20);
    });

    it('caps retry delay at MAX_RETRY_DELAY (60s)', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      // Trigger first failure
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      // Simulate multiple retries to reach the cap:
      // 5s -> 10s -> 20s -> 40s -> 60s (capped)
      const expectedDelays = [5, 10, 20, 40, 60];

      for (let i = 0; i < expectedDelays.length; i++) {
        expect(result.current.retryCountdown).toBe(expectedDelays[i]);

        makeNextFetchFail();
        await act(async () => {
          vi.advanceTimersByTime(expectedDelays[i] * 1000);
        });
        await flushPromises();
      }

      // Should stay capped at 60
      expect(result.current.retryCountdown).toBe(60);
    });
  });

  describe('retryNow', () => {
    it('cancels countdown and retries immediately', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      // Trigger error
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(5);

      // Setup success for manual retry
      mockFetchStatus.mockResolvedValueOnce(createMockStatusResult());
      mockFetchTasks.mockResolvedValueOnce(createMockTaskLists());

      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(0);
      expect(result.current.isConnected).toBe(true);
    });

    it('resets backoff delay on manual retry', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      // Trigger failure and let backoff increase
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      // Let first retry fire (backoff goes to 10s)
      makeNextFetchFail();
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(10);

      // Manual retry - should reset delay
      makeNextFetchFail();
      await act(async () => {
        result.current.retryNow();
      });
      await flushPromises();

      // After manual retry fails, the new countdown should be back to 5 (reset)
      expect(result.current.retryCountdown).toBe(5);
    });
  });

  describe('retry only when previously connected', () => {
    it('does not schedule retry if never connected', async () => {
      // All fetches fail - never establishes connection
      mockFetchStatus.mockRejectedValue(new Error('Connection refused'));
      mockFetchTasks.mockRejectedValue(new Error('Connection refused'));

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      expect(result.current.isConnected).toBe(false);
      expect(result.current.wasEverConnected).toBe(false);
      expect(result.current.retryCountdown).toBe(0);
    });
  });

  describe('connection state', () => {
    it('returns connected when fetch succeeds', async () => {
      mockFetchStatus.mockResolvedValue(createMockStatusResult());
      mockFetchTasks.mockResolvedValue(createMockTaskLists());

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      expect(result.current.connectionState).toBe('connected');
    });

    it('returns reconnecting when countdown is active', async () => {
      // First fetch succeeds
      setupSuccessThenFail();

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      // Trigger error
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      expect(result.current.connectionState).toBe('reconnecting');
    });

    it('returns never_connected when first fetch has not completed', async () => {
      // Never resolves
      mockFetchStatus.mockImplementation(() => new Promise(() => {}));
      mockFetchTasks.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      expect(result.current.connectionState).toBe('never_connected');
    });
  });

  describe('cleanup', () => {
    it('clears retry timers on unmount', async () => {
      const clearTimeoutSpy = vi.spyOn(globalThis, 'clearTimeout');
      const clearIntervalSpy = vi.spyOn(globalThis, 'clearInterval');

      // First fetch succeeds
      setupSuccessThenFail();

      const { result, unmount } = renderHook(() => useAgents({ pollInterval: 0, enabled: true }));

      await flushPromises();

      // Trigger error to start retry timers
      makeNextFetchFail();
      await act(async () => {
        await result.current.refetch();
      });
      await flushPromises();

      expect(result.current.retryCountdown).toBe(5);

      clearTimeoutSpy.mockClear();
      clearIntervalSpy.mockClear();

      unmount();

      // Should have called clearTimeout and clearInterval during cleanup
      expect(clearTimeoutSpy).toHaveBeenCalled();
      expect(clearIntervalSpy).toHaveBeenCalled();

      clearTimeoutSpy.mockRestore();
      clearIntervalSpy.mockRestore();
    });
  });
});
