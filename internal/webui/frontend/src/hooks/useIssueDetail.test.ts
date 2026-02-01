/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act, waitFor as _waitFor } from '@testing-library/react';
import { useIssueDetail } from './useIssueDetail';
import type { IssueDetails } from '@/types';

// Mock the getIssue API function
vi.mock('@/api/issues', () => ({
  getIssue: vi.fn(),
}));

// Import after mocking
import { getIssue } from '@/api/issues';

const mockGetIssue = vi.mocked(getIssue);

/**
 * Helper to create a minimal valid IssueDetails for testing.
 */
function createIssueDetails(overrides: Partial<IssueDetails> = {}): IssueDetails {
  return {
    id: overrides.id ?? 'issue-1',
    title: overrides.title ?? 'Test Issue',
    priority: overrides.priority ?? 2,
    created_at: overrides.created_at ?? '2024-01-01T00:00:00Z',
    updated_at: overrides.updated_at ?? '2024-01-01T00:00:00Z',
    ...overrides,
  };
}

describe('useIssueDetail', () => {
  beforeEach(() => {
    mockGetIssue.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('Initial state', () => {
    it('returns expected shape with all properties', () => {
      const { result } = renderHook(() => useIssueDetail());

      expect(result.current).toHaveProperty('issueDetails');
      expect(result.current).toHaveProperty('isLoading');
      expect(result.current).toHaveProperty('error');
      expect(result.current).toHaveProperty('fetchIssue');
      expect(result.current).toHaveProperty('clearIssue');

      expect(typeof result.current.fetchIssue).toBe('function');
      expect(typeof result.current.clearIssue).toBe('function');
    });

    it('starts with null issueDetails, not loading, no error', () => {
      const { result } = renderHook(() => useIssueDetail());

      expect(result.current.issueDetails).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe('Successful fetch flow', () => {
    it('fetches issue details when fetchIssue is called', async () => {
      const testIssue = createIssueDetails({ id: 'issue-123', title: 'Test Issue' });
      mockGetIssue.mockResolvedValue(testIssue);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-123');
      });

      expect(result.current.issueDetails).toEqual(testIssue);
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(mockGetIssue).toHaveBeenCalledTimes(1);
      expect(mockGetIssue).toHaveBeenCalledWith('issue-123');
    });

    it('sets isLoading to true during fetch', async () => {
      let resolvePromise: (value: IssueDetails) => void;
      const slowPromise = new Promise<IssueDetails>((resolve) => {
        resolvePromise = resolve;
      });
      mockGetIssue.mockReturnValue(slowPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start fetch
      act(() => {
        result.current.fetchIssue('issue-1');
      });

      // Should be loading
      expect(result.current.isLoading).toBe(true);

      // Resolve the promise
      await act(async () => {
        resolvePromise!(createIssueDetails());
      });

      // Should no longer be loading
      expect(result.current.isLoading).toBe(false);
    });

    it('clears previous error on new fetch', async () => {
      // First call fails
      mockGetIssue.mockRejectedValueOnce(new Error('Network error'));

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.error).toBe('Network error');

      // Second call succeeds
      mockGetIssue.mockResolvedValueOnce(createIssueDetails());

      await act(async () => {
        await result.current.fetchIssue('issue-2');
      });

      expect(result.current.error).toBeNull();
    });

    it('does not fetch when id is empty string', async () => {
      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('');
      });

      expect(mockGetIssue).not.toHaveBeenCalled();
      expect(result.current.isLoading).toBe(false);
    });

    it('fetchIssue is stable across renders', async () => {
      const { result, rerender } = renderHook(() => useIssueDetail());

      const initialFetchIssue = result.current.fetchIssue;

      rerender();

      expect(result.current.fetchIssue).toBe(initialFetchIssue);
    });
  });

  describe('Error handling', () => {
    it('sets error on fetch failure with Error object', async () => {
      const testError = new Error('Network error');
      mockGetIssue.mockRejectedValue(testError);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.error).toBe('Network error');
      expect(result.current.isLoading).toBe(false);
    });

    it('converts non-Error thrown values to string', async () => {
      mockGetIssue.mockRejectedValue('string error');

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.error).toBe('string error');
    });

    it('does not clear existing issueDetails on error (shows stale data)', async () => {
      const testIssue = createIssueDetails({ id: 'issue-1', title: 'Test Issue' });
      mockGetIssue.mockResolvedValueOnce(testIssue);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.issueDetails).toEqual(testIssue);

      // Now fail on next fetch
      mockGetIssue.mockRejectedValueOnce(new Error('Network error'));

      await act(async () => {
        await result.current.fetchIssue('issue-2');
      });

      // Data should still be there
      expect(result.current.issueDetails).toEqual(testIssue);
      expect(result.current.error).toBe('Network error');
    });
  });

  describe('Clearing issue', () => {
    it('clearIssue resets all state', async () => {
      const testIssue = createIssueDetails();
      mockGetIssue.mockResolvedValueOnce(testIssue);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.issueDetails).toEqual(testIssue);

      act(() => {
        result.current.clearIssue();
      });

      expect(result.current.issueDetails).toBeNull();
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(false);
    });

    it('clearIssue also clears error state', async () => {
      mockGetIssue.mockRejectedValueOnce(new Error('Network error'));

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });

      expect(result.current.error).toBe('Network error');

      act(() => {
        result.current.clearIssue();
      });

      expect(result.current.error).toBeNull();
    });

    it('clearIssue cancels in-flight requests', async () => {
      let resolvePromise: (value: IssueDetails) => void;
      const slowPromise = new Promise<IssueDetails>((resolve) => {
        resolvePromise = resolve;
      });
      mockGetIssue.mockReturnValue(slowPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start fetch
      act(() => {
        result.current.fetchIssue('issue-1');
      });

      expect(result.current.isLoading).toBe(true);

      // Clear before fetch completes
      act(() => {
        result.current.clearIssue();
      });

      expect(result.current.isLoading).toBe(false);
      expect(result.current.issueDetails).toBeNull();

      // Resolve the promise after clear
      await act(async () => {
        resolvePromise!(createIssueDetails({ id: 'issue-1', title: 'Should not appear' }));
      });

      // State should still be cleared (request was cancelled)
      expect(result.current.issueDetails).toBeNull();
    });

    it('clearIssue is stable across renders', async () => {
      const { result, rerender } = renderHook(() => useIssueDetail());

      const initialClearIssue = result.current.clearIssue;

      rerender();

      expect(result.current.clearIssue).toBe(initialClearIssue);
    });
  });

  describe('Concurrent fetch handling (latest request wins)', () => {
    it('only uses result from latest request when multiple fetches are made', async () => {
      const firstIssue = createIssueDetails({ id: 'first', title: 'First Issue' });
      const secondIssue = createIssueDetails({ id: 'second', title: 'Second Issue' });

      let resolveFirst: (value: IssueDetails) => void;
      let resolveSecond: (value: IssueDetails) => void;

      const firstPromise = new Promise<IssueDetails>((resolve) => {
        resolveFirst = resolve;
      });
      const secondPromise = new Promise<IssueDetails>((resolve) => {
        resolveSecond = resolve;
      });

      mockGetIssue.mockReturnValueOnce(firstPromise).mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start first fetch
      act(() => {
        result.current.fetchIssue('first');
      });

      // Start second fetch before first completes
      act(() => {
        result.current.fetchIssue('second');
      });

      // Resolve second first (out of order)
      await act(async () => {
        resolveSecond!(secondIssue);
      });

      expect(result.current.issueDetails).toEqual(secondIssue);
      expect(result.current.isLoading).toBe(false);

      // Now resolve first (should be ignored)
      await act(async () => {
        resolveFirst!(firstIssue);
      });

      // Should still be second issue
      expect(result.current.issueDetails).toEqual(secondIssue);
    });

    it('ignores stale request errors when newer request succeeds', async () => {
      const successIssue = createIssueDetails({ id: 'success', title: 'Success Issue' });

      let rejectFirst: (error: Error) => void;
      let resolveSecond: (value: IssueDetails) => void;

      const firstPromise = new Promise<IssueDetails>((_, reject) => {
        rejectFirst = reject;
      });
      const secondPromise = new Promise<IssueDetails>((resolve) => {
        resolveSecond = resolve;
      });

      mockGetIssue.mockReturnValueOnce(firstPromise).mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start first fetch
      act(() => {
        result.current.fetchIssue('first');
      });

      // Start second fetch before first completes
      act(() => {
        result.current.fetchIssue('second');
      });

      // Resolve second first
      await act(async () => {
        resolveSecond!(successIssue);
      });

      expect(result.current.issueDetails).toEqual(successIssue);
      expect(result.current.error).toBeNull();

      // Now reject first (should be ignored)
      await act(async () => {
        rejectFirst!(new Error('First request failed'));
      });

      // Should still have success state
      expect(result.current.issueDetails).toEqual(successIssue);
      expect(result.current.error).toBeNull();
    });

    it('ignores stale request success when newer request fails', async () => {
      const staleIssue = createIssueDetails({ id: 'stale', title: 'Stale Issue' });

      let resolveFirst: (value: IssueDetails) => void;
      let rejectSecond: (error: Error) => void;

      const firstPromise = new Promise<IssueDetails>((resolve) => {
        resolveFirst = resolve;
      });
      const secondPromise = new Promise<IssueDetails>((_, reject) => {
        rejectSecond = reject;
      });

      mockGetIssue.mockReturnValueOnce(firstPromise).mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start first fetch
      act(() => {
        result.current.fetchIssue('first');
      });

      // Start second fetch before first completes
      act(() => {
        result.current.fetchIssue('second');
      });

      // Reject second first
      await act(async () => {
        rejectSecond!(new Error('Second request failed'));
      });

      expect(result.current.error).toBe('Second request failed');
      expect(result.current.isLoading).toBe(false);

      // Now resolve first (should be ignored)
      await act(async () => {
        resolveFirst!(staleIssue);
      });

      // Should still have error state, not stale data
      expect(result.current.error).toBe('Second request failed');
      // Note: issueDetails might still be null since error occurred on second request
      // and we don't update issueDetails from stale request
    });

    it('only latest request controls loading state', async () => {
      let resolveFirst: (value: IssueDetails) => void;
      let resolveSecond: (value: IssueDetails) => void;

      const firstPromise = new Promise<IssueDetails>((resolve) => {
        resolveFirst = resolve;
      });
      const secondPromise = new Promise<IssueDetails>((resolve) => {
        resolveSecond = resolve;
      });

      mockGetIssue.mockReturnValueOnce(firstPromise).mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useIssueDetail());

      // Start first fetch
      act(() => {
        result.current.fetchIssue('first');
      });

      expect(result.current.isLoading).toBe(true);

      // Start second fetch
      act(() => {
        result.current.fetchIssue('second');
      });

      expect(result.current.isLoading).toBe(true);

      // Resolve first (stale)
      await act(async () => {
        resolveFirst!(createIssueDetails({ id: 'first' }));
      });

      // Should STILL be loading because second request is pending
      expect(result.current.isLoading).toBe(true);

      // Resolve second
      await act(async () => {
        resolveSecond!(createIssueDetails({ id: 'second' }));
      });

      // Now loading should be false
      expect(result.current.isLoading).toBe(false);
    });
  });

  describe('Cleanup on unmount', () => {
    it('does not update state after unmount', async () => {
      let resolvePromise: (value: IssueDetails) => void;
      const slowPromise = new Promise<IssueDetails>((resolve) => {
        resolvePromise = resolve;
      });
      mockGetIssue.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() => useIssueDetail());

      // Start fetch
      act(() => {
        result.current.fetchIssue('issue-1');
      });

      expect(result.current.isLoading).toBe(true);

      // Unmount while fetch is in progress
      unmount();

      // Resolve the promise after unmount
      await act(async () => {
        resolvePromise!(createIssueDetails());
      });

      // No errors should occur (React would warn about state update on unmounted component)
    });

    it('does not set error after unmount', async () => {
      let rejectPromise: (error: Error) => void;
      const slowPromise = new Promise<IssueDetails>((_, reject) => {
        rejectPromise = reject;
      });
      mockGetIssue.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() => useIssueDetail());

      // Start fetch
      act(() => {
        result.current.fetchIssue('issue-1');
      });

      // Unmount while fetch is in progress
      unmount();

      // Reject the promise after unmount
      await act(async () => {
        rejectPromise!(new Error('Network error'));
      });

      // No errors should occur
    });
  });

  describe('Edge cases', () => {
    it('handles multiple sequential fetches correctly', async () => {
      const issue1 = createIssueDetails({ id: 'issue-1', title: 'Issue 1' });
      const issue2 = createIssueDetails({ id: 'issue-2', title: 'Issue 2' });
      const issue3 = createIssueDetails({ id: 'issue-3', title: 'Issue 3' });

      mockGetIssue
        .mockResolvedValueOnce(issue1)
        .mockResolvedValueOnce(issue2)
        .mockResolvedValueOnce(issue3);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-1');
      });
      expect(result.current.issueDetails).toEqual(issue1);

      await act(async () => {
        await result.current.fetchIssue('issue-2');
      });
      expect(result.current.issueDetails).toEqual(issue2);

      await act(async () => {
        await result.current.fetchIssue('issue-3');
      });
      expect(result.current.issueDetails).toEqual(issue3);

      expect(mockGetIssue).toHaveBeenCalledTimes(3);
    });

    it('handles issue with full details', async () => {
      const fullIssue = createIssueDetails({
        id: 'issue-full',
        title: 'Full Issue',
        description: 'A detailed description',
        status: 'open',
        issue_type: 'feature',
        labels: ['frontend', 'urgent'],
        dependencies: [
          {
            id: 'dep-1',
            title: 'Dependency 1',
            priority: 2,
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
            dependency_type: 'blocks',
          },
        ],
        dependents: [],
        comments: [
          {
            id: 'comment-1',
            content: 'A comment',
            created_at: '2024-01-02T00:00:00Z',
            author: 'user-1',
          },
        ],
        parent: 'epic-1',
      });

      mockGetIssue.mockResolvedValueOnce(fullIssue);

      const { result } = renderHook(() => useIssueDetail());

      await act(async () => {
        await result.current.fetchIssue('issue-full');
      });

      expect(result.current.issueDetails).toEqual(fullIssue);
      expect(result.current.issueDetails?.dependencies).toHaveLength(1);
      expect(result.current.issueDetails?.comments).toHaveLength(1);
      expect(result.current.issueDetails?.parent).toBe('epic-1');
    });
  });
});
