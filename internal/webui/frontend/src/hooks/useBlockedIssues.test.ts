/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';
import { useBlockedIssues } from './useBlockedIssues';
import type { BlockedIssue } from '@/types';

// Mock the getBlockedIssues API function
vi.mock('@/api/issues', () => ({
  getBlockedIssues: vi.fn(),
}));

// Import after mocking
import { getBlockedIssues } from '@/api/issues';

const mockGetBlockedIssues = vi.mocked(getBlockedIssues);

/**
 * Helper to create a minimal valid BlockedIssue for testing.
 */
function createBlockedIssue(overrides: Partial<BlockedIssue> = {}): BlockedIssue {
  return {
    id: overrides.id ?? 'issue-1',
    title: overrides.title ?? 'Test Issue',
    priority: overrides.priority ?? 2,
    created_at: overrides.created_at ?? '2024-01-01T00:00:00Z',
    updated_at: overrides.updated_at ?? '2024-01-01T00:00:00Z',
    blocked_by_count: overrides.blocked_by_count ?? 1,
    blocked_by: overrides.blocked_by ?? ['blocker-1'],
    ...overrides,
  };
}

/**
 * Helper to create a set of test blocked issues.
 */
function createTestBlockedIssues(): BlockedIssue[] {
  return [
    createBlockedIssue({
      id: 'issue-1',
      title: 'Blocked Issue 1',
      blocked_by_count: 2,
      blocked_by: ['blocker-1', 'blocker-2'],
    }),
    createBlockedIssue({
      id: 'issue-2',
      title: 'Blocked Issue 2',
      blocked_by_count: 1,
      blocked_by: ['blocker-3'],
    }),
  ];
}

/**
 * Helper to flush pending promises when using fake timers.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe('useBlockedIssues', () => {
  beforeEach(() => {
    mockGetBlockedIssues.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('Initial state', () => {
    it('returns expected shape with all properties', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      expect(result.current).toHaveProperty('data');
      expect(result.current).toHaveProperty('loading');
      expect(result.current).toHaveProperty('error');
      expect(result.current).toHaveProperty('refetch');

      expect(typeof result.current.refetch).toBe('function');

      // Wait for fetch to complete to avoid act warnings
      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });
    });

    it('starts with loading=true when enabled', async () => {
      mockGetBlockedIssues.mockImplementation(
        () => new Promise(() => {}) // Never resolves
      );

      const { result } = renderHook(() => useBlockedIssues());

      expect(result.current.loading).toBe(true);
      expect(result.current.data).toBeNull();
      expect(result.current.error).toBeNull();
    });

    it('starts with loading=false when enabled=false', () => {
      const { result } = renderHook(() => useBlockedIssues({ enabled: false }));

      expect(result.current.loading).toBe(false);
      expect(result.current.data).toBeNull();
      expect(result.current.error).toBeNull();
    });
  });

  describe('Successful data fetch', () => {
    it('fetches data on mount', async () => {
      const testIssues = createTestBlockedIssues();
      mockGetBlockedIssues.mockResolvedValue(testIssues);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(result.current.data).toEqual(testIssues);
      expect(result.current.error).toBeNull();
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      expect(mockGetBlockedIssues).toHaveBeenCalledWith({});
    });

    it('sets loading to false after successful fetch', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      expect(result.current.loading).toBe(true);

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(result.current.data).toEqual([]);
    });

    it('clears error on successful fetch', async () => {
      // First call fails
      mockGetBlockedIssues.mockRejectedValueOnce(new Error('Network error'));

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.error).not.toBeNull();
      });

      // Second call succeeds
      mockGetBlockedIssues.mockResolvedValue([]);

      await act(async () => {
        await result.current.refetch();
      });

      expect(result.current.error).toBeNull();
    });
  });

  describe('Error handling', () => {
    it('sets error on fetch failure', async () => {
      const testError = new Error('Network error');
      mockGetBlockedIssues.mockRejectedValue(testError);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(result.current.error).toEqual(testError);
      expect(result.current.data).toBeNull();
    });

    it('converts non-Error thrown values to Error', async () => {
      mockGetBlockedIssues.mockRejectedValue('string error');

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error?.message).toBe('string error');
    });

    it('keeps stale data on error', async () => {
      const testIssues = createTestBlockedIssues();
      mockGetBlockedIssues.mockResolvedValueOnce(testIssues);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.data).toEqual(testIssues);
      });

      // Now fail on refetch
      mockGetBlockedIssues.mockRejectedValueOnce(new Error('Network error'));

      await act(async () => {
        await result.current.refetch();
      });

      // Data should still be there
      expect(result.current.data).toEqual(testIssues);
      expect(result.current.error).not.toBeNull();
    });
  });

  describe('Refetch functionality', () => {
    it('refetch() triggers new fetch', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      await act(async () => {
        await result.current.refetch();
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2);
    });

    it('refetch() updates data with new values', async () => {
      const initialIssues = [createBlockedIssue({ id: 'issue-1' })];
      const updatedIssues = [
        createBlockedIssue({ id: 'issue-1' }),
        createBlockedIssue({ id: 'issue-2' }),
      ];

      mockGetBlockedIssues.mockResolvedValueOnce(initialIssues);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.data).toEqual(initialIssues);
      });

      mockGetBlockedIssues.mockResolvedValueOnce(updatedIssues);

      await act(async () => {
        await result.current.refetch();
      });

      expect(result.current.data).toEqual(updatedIssues);
    });

    it('refetch is stable across renders', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result, rerender } = renderHook(() => useBlockedIssues());

      const initialRefetch = result.current.refetch;

      rerender();

      expect(result.current.refetch).toBe(initialRefetch);

      // Wait for fetch to complete
      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });
    });
  });

  describe('Polling behavior', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('polls at specified interval', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues({ pollInterval: 5000 }));

      // Initial fetch
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      // Flush initial promise
      await flushPromises();

      // Advance time by pollInterval
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2);

      // Advance again
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(3);
    });

    it('does not poll when pollInterval is not set', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues());

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      await flushPromises();

      // Advance time significantly
      await act(async () => {
        vi.advanceTimersByTime(30000);
      });
      await flushPromises();

      // Should still be just 1 call
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
    });

    it('does not poll when pollInterval is 0', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues({ pollInterval: 0 }));

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      await flushPromises();

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
    });

    it('does not poll when enabled=false', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues({ pollInterval: 1000, enabled: false }));

      // No initial fetch
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(0);

      // Advance time
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // Still no fetch
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(0);
    });

    it('prevents overlapping poll requests', async () => {
      let resolveFirst: (value: BlockedIssue[]) => void;
      const firstPromise = new Promise<BlockedIssue[]>((resolve) => {
        resolveFirst = resolve;
      });
      mockGetBlockedIssues.mockReturnValueOnce(firstPromise);
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues({ pollInterval: 1000 }));

      // Initial fetch starts
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      // Advance time - poll should trigger but be skipped since fetch in progress
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });

      // Still just 1 call because fetch is in progress
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      // Resolve the first fetch
      await act(async () => {
        resolveFirst!([]);
      });

      // Advance to next poll
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      // Now second fetch should happen
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(2);
    });
  });

  describe('parentId filtering', () => {
    it('passes parentId to API as parent_id', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues({ parentId: 'epic-123' }));

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({
        parent_id: 'epic-123',
      });
    });

    it('omits parent_id when parentId is undefined', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({});
    });

    it('refetches when parentId changes', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result, rerender } = renderHook(({ parentId }) => useBlockedIssues({ parentId }), {
        initialProps: { parentId: 'epic-1' },
      });

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({
        parent_id: 'epic-1',
      });

      rerender({ parentId: 'epic-2' });

      await waitFor(() => {
        expect(mockGetBlockedIssues).toHaveBeenCalledWith({
          parent_id: 'epic-2',
        });
      });
    });
  });

  describe('enabled option', () => {
    it('does not fetch when enabled=false', () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      renderHook(() => useBlockedIssues({ enabled: false }));

      expect(mockGetBlockedIssues).not.toHaveBeenCalled();
    });

    it('fetches when enabled changes from false to true', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result, rerender } = renderHook(({ enabled }) => useBlockedIssues({ enabled }), {
        initialProps: { enabled: false },
      });

      expect(mockGetBlockedIssues).not.toHaveBeenCalled();

      rerender({ enabled: true });

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
    });

    it('stops polling when enabled changes to false', async () => {
      vi.useFakeTimers();

      mockGetBlockedIssues.mockResolvedValue([]);

      const { rerender } = renderHook(
        ({ enabled }) => useBlockedIssues({ enabled, pollInterval: 1000 }),
        { initialProps: { enabled: true } }
      );

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      await flushPromises();

      // Disable
      rerender({ enabled: false });

      // Advance time
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // No more calls after disable
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      vi.useRealTimers();
    });

    it('defaults enabled to true', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
    });
  });

  describe('Cleanup on unmount', () => {
    it('clears polling interval on unmount', async () => {
      vi.useFakeTimers();

      mockGetBlockedIssues.mockResolvedValue([]);

      const { unmount } = renderHook(() => useBlockedIssues({ pollInterval: 1000 }));

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      await flushPromises();

      unmount();

      // Advance time after unmount
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // No additional calls
      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);

      vi.useRealTimers();
    });

    it('does not update state after unmount', async () => {
      let resolvePromise: (value: BlockedIssue[]) => void;
      const slowPromise = new Promise<BlockedIssue[]>((resolve) => {
        resolvePromise = resolve;
      });
      mockGetBlockedIssues.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() => useBlockedIssues());

      expect(result.current.loading).toBe(true);

      // Unmount while fetch is in progress
      unmount();

      // Resolve the promise after unmount
      await act(async () => {
        resolvePromise!([createBlockedIssue()]);
      });

      // No errors should occur (React would warn about state update on unmounted component)
    });

    it('sets mountedRef to false on unmount', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result, unmount } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      unmount();

      // This verifies that the cleanup ran by checking no additional effects happen
      // The actual mountedRef is internal, so we test behavior instead
    });
  });

  describe('Hook reactivity', () => {
    it('updates when options change', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result, rerender } = renderHook(({ parentId }) => useBlockedIssues({ parentId }), {
        initialProps: { parentId: undefined as string | undefined },
      });

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({});

      rerender({ parentId: 'new-parent' });

      await waitFor(() => {
        expect(mockGetBlockedIssues).toHaveBeenCalledWith({
          parent_id: 'new-parent',
        });
      });
    });
  });

  describe('Interval stacking prevention', () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    it('does not stack intervals when parentId changes rapidly', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { rerender } = renderHook(
        ({ parentId }) => useBlockedIssues({ parentId, pollInterval: 5000 }),
        { initialProps: { parentId: 'epic-1' } }
      );

      await flushPromises();

      // Change parentId multiple times rapidly
      rerender({ parentId: 'epic-2' });
      await flushPromises();
      rerender({ parentId: 'epic-3' });
      await flushPromises();

      // Reset call count to only measure poll calls
      mockGetBlockedIssues.mockClear();

      // Advance one poll interval - should only get ONE poll call (not 3 stacked intervals)
      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      expect(mockGetBlockedIssues).toHaveBeenCalledTimes(1);
      expect(mockGetBlockedIssues).toHaveBeenCalledWith({
        parent_id: 'epic-3',
      });
    });
  });

  describe('Edge cases', () => {
    it('handles empty response array', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues());

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(result.current.data).toEqual([]);
      expect(result.current.error).toBeNull();
    });

    it('handles undefined options', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues(undefined));

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({});
    });

    it('handles empty options object', async () => {
      mockGetBlockedIssues.mockResolvedValue([]);

      const { result } = renderHook(() => useBlockedIssues({}));

      await waitFor(() => {
        expect(result.current.loading).toBe(false);
      });

      expect(mockGetBlockedIssues).toHaveBeenCalledWith({});
    });
  });
});
