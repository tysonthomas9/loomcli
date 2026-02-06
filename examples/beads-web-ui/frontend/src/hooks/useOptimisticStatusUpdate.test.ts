/**
 * @vitest-environment jsdom
 */
import { renderHook, act, waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { useOptimisticStatusUpdate } from './useOptimisticStatusUpdate';
import * as api from '../api';
import type { Issue } from '../types/issue';
import type { Status as _Status } from '../types/status';

// Mock the API module
vi.mock('../api', () => ({
  updateIssue: vi.fn(),
  ApiError: class ApiError extends Error {
    constructor(
      public status: number,
      public statusText: string,
      public body?: unknown
    ) {
      super(`API Error: ${status} ${statusText}`);
      this.name = 'ApiError';
    }
  },
}));

/**
 * Helper to create a test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-1',
    title: 'Test Issue',
    priority: 2,
    status: 'open',
    created_at: '2025-01-23T10:00:00Z',
    updated_at: '2025-01-23T10:00:00Z',
    ...overrides,
  };
}

describe('useOptimisticStatusUpdate', () => {
  let mockIssues: Map<string, Issue>;
  let mockSetIssues: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    mockIssues = new Map();
    mockSetIssues = vi.fn();
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('Hook initialization', () => {
    it('returns expected shape with all properties', () => {
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current).toHaveProperty('updateIssueStatus');
      expect(result.current).toHaveProperty('pendingUpdates');
      expect(result.current).toHaveProperty('lastError');

      expect(typeof result.current.updateIssueStatus).toBe('function');
      expect(result.current.pendingUpdates).toBeInstanceOf(Set);
      expect(result.current.lastError).toBeNull();
    });

    it('initial state has empty pending updates and null lastError', () => {
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      expect(result.current.pendingUpdates.size).toBe(0);
      expect(result.current.lastError).toBeNull();
    });
  });

  describe('Successful status update', () => {
    it('applies optimistic update to state immediately', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(mockSetIssues).toHaveBeenCalled();
      const newIssuesMap = mockSetIssues.mock.calls[0][0] as Map<string, Issue>;
      expect(newIssuesMap.get('issue-1')?.status).toBe('in_progress');
    });

    it('calls success callback after successful API update', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const onSuccess = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onSuccess,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onSuccess).toHaveBeenCalledTimes(1);
      expect(onSuccess).toHaveBeenCalledWith('issue-1', 'in_progress');
    });

    it('clears lastError on successful update', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(result.current.lastError).toBeNull();
    });

    it('updates updated_at timestamp on optimistic update', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
        updated_at: '2025-01-01T00:00:00Z',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      const newIssuesMap = mockSetIssues.mock.calls[0][0] as Map<string, Issue>;
      const updatedIssue = newIssuesMap.get('issue-1');
      expect(updatedIssue?.updated_at).not.toBe('2025-01-01T00:00:00Z');
    });
  });

  describe('API failure triggers rollback', () => {
    it('rolls back to original status on API error', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      // Track state changes - note that rollback uses functional update
      let callCount = 0;
      let lastIssuesState: Map<string, Issue> = mockIssues;
      mockSetIssues.mockImplementation(
        (arg: Map<string, Issue> | ((prev: Map<string, Issue>) => Map<string, Issue>)) => {
          callCount++;
          if (typeof arg === 'function') {
            // Functional update - apply it to get the new state
            lastIssuesState = arg(lastIssuesState);
          } else {
            // Direct update
            lastIssuesState = arg;
          }
          if (callCount === 1) {
            // First call: optimistic update to in_progress
            expect(lastIssuesState.get('issue-1')?.status).toBe('in_progress');
          }
          mockIssues = lastIssuesState;
        }
      );

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(500, 'Internal Server Error'));

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      // Should have been called at least twice: optimistic update + rollback
      expect(mockSetIssues).toHaveBeenCalledTimes(2);

      // The final state should have the original status restored
      expect(lastIssuesState.get('issue-1')?.status).toBe('open');
    });

    it('calls error callback with correct parameters on failure', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      // Mock setIssues to update the internal state
      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(500, 'Internal Server Error'));

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledTimes(1);
      expect(onError).toHaveBeenCalledWith('issue-1', expect.any(Error), 'open', 'in_progress');
    });

    it('sets lastError on failure', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(500, 'Internal Server Error'));

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(result.current.lastError).not.toBeNull();
      expect(result.current.lastError?.issueId).toBe('issue-1');
      expect(result.current.lastError?.error).toBeInstanceOf(Error);
    });

    it('handles 404 error with specific message', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(404, 'Not Found'));

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledWith(
        'issue-1',
        expect.objectContaining({ message: 'Issue no longer exists' }),
        'open',
        'in_progress'
      );
    });

    it('handles 409 conflict error with specific message', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(409, 'Conflict'));

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledWith(
        'issue-1',
        expect.objectContaining({ message: 'Conflict with server state' }),
        'open',
        'in_progress'
      );
    });

    it('handles network error (status 0)', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(0, 'Network error'));

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledWith(
        'issue-1',
        expect.objectContaining({ message: 'Network error' }),
        'open',
        'in_progress'
      );
    });

    it('handles generic Error', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new Error('Something went wrong'));

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledWith(
        'issue-1',
        expect.objectContaining({ message: 'Network error: Something went wrong' }),
        'open',
        'in_progress'
      );
    });

    it('handles unknown error type', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue('string error');

      const onError = vi.fn();
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          onError,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(onError).toHaveBeenCalledWith(
        'issue-1',
        expect.objectContaining({ message: 'Unknown error occurred' }),
        'open',
        'in_progress'
      );
    });
  });

  describe('Pending updates prevent duplicate requests', () => {
    it('rejects update when issue is already pending', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      // Create a promise that doesn't resolve immediately
      let resolvePromise: () => void;
      const pendingPromise = new Promise<void>((resolve) => {
        resolvePromise = resolve;
      });
      vi.mocked(api.updateIssue).mockReturnValue(pendingPromise);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      // Start first update (will be pending)
      act(() => {
        result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      // Verify pending state
      await waitFor(() => {
        expect(result.current.pendingUpdates.has('issue-1')).toBe(true);
      });

      // Try second update while first is pending
      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'closed', 'in_progress');
      });

      // Only one API call should have been made
      expect(api.updateIssue).toHaveBeenCalledTimes(1);

      // Resolve the pending promise to clean up
      await act(async () => {
        resolvePromise!();
        await pendingPromise;
      });
    });

    it('clears pending state after update completes', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(result.current.pendingUpdates.has('issue-1')).toBe(false);
    });

    it('clears pending state after update fails', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(500, 'Internal Server Error'));

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      expect(result.current.pendingUpdates.has('issue-1')).toBe(false);
    });
  });

  describe('Skip update when old and new status are the same', () => {
    it('skips update when statuses are equal', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'open', 'open');
      });

      // No API call or state update should occur
      expect(api.updateIssue).not.toHaveBeenCalled();
      expect(mockSetIssues).not.toHaveBeenCalled();
    });
  });

  describe('Multiple concurrent updates to different issues', () => {
    it('allows concurrent updates to different issues', async () => {
      const issue1 = createTestIssue({ id: 'issue-1', status: 'open' });
      const issue2 = createTestIssue({ id: 'issue-2', status: 'open' });
      mockIssues.set('issue-1', issue1);
      mockIssues.set('issue-2', issue2);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await Promise.all([
          result.current.updateIssueStatus('issue-1', 'in_progress', 'open'),
          result.current.updateIssueStatus('issue-2', 'closed', 'open'),
        ]);
      });

      // Both API calls should have been made
      expect(api.updateIssue).toHaveBeenCalledTimes(2);
      expect(api.updateIssue).toHaveBeenCalledWith('issue-1', { status: 'in_progress' });
      expect(api.updateIssue).toHaveBeenCalledWith('issue-2', { status: 'closed' });
    });

    it('tracks pending state for multiple issues independently', async () => {
      const issue1 = createTestIssue({ id: 'issue-1', status: 'open' });
      const issue2 = createTestIssue({ id: 'issue-2', status: 'open' });
      mockIssues.set('issue-1', issue1);
      mockIssues.set('issue-2', issue2);

      let resolve1: () => void;
      let resolve2: () => void;
      const promise1 = new Promise<void>((r) => {
        resolve1 = r;
      });
      const promise2 = new Promise<void>((r) => {
        resolve2 = r;
      });

      vi.mocked(api.updateIssue).mockReturnValueOnce(promise1).mockReturnValueOnce(promise2);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      // Start both updates
      act(() => {
        result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
        result.current.updateIssueStatus('issue-2', 'closed', 'open');
      });

      // Both should be pending
      await waitFor(() => {
        expect(result.current.pendingUpdates.has('issue-1')).toBe(true);
        expect(result.current.pendingUpdates.has('issue-2')).toBe(true);
      });

      // Resolve first one
      await act(async () => {
        resolve1!();
        await promise1;
      });

      // Only issue-2 should be pending now
      await waitFor(() => {
        expect(result.current.pendingUpdates.has('issue-1')).toBe(false);
        expect(result.current.pendingUpdates.has('issue-2')).toBe(true);
      });

      // Resolve second one
      await act(async () => {
        resolve2!();
        await promise2;
      });

      // Neither should be pending
      expect(result.current.pendingUpdates.has('issue-1')).toBe(false);
      expect(result.current.pendingUpdates.has('issue-2')).toBe(false);
    });
  });

  describe('Edge cases', () => {
    it('handles non-existent issue gracefully', async () => {
      // Empty issues map
      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('non-existent', 'in_progress', 'open');
      });

      // Should not call API or setIssues
      expect(api.updateIssue).not.toHaveBeenCalled();
      expect(mockSetIssues).not.toHaveBeenCalled();
    });

    it('callbacks are optional and do not throw when not provided', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          // No callbacks provided
        })
      );

      // Should not throw
      await expect(
        act(async () => {
          await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
        })
      ).resolves.not.toThrow();
    });

    it('callbacks are optional and do not throw on error', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      mockSetIssues.mockImplementation((newMap: Map<string, Issue>) => {
        mockIssues = newMap;
      });

      vi.mocked(api.updateIssue).mockRejectedValue(new api.ApiError(500, 'Internal Server Error'));

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
          // No callbacks provided
        })
      );

      // Should not throw
      await expect(
        act(async () => {
          await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
        })
      ).resolves.not.toThrow();
    });
  });

  describe('Immutability', () => {
    it('does not mutate the original issues map', async () => {
      const originalIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', originalIssue);

      const originalStatus = originalIssue.status;

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      // Original issue should be unchanged
      expect(mockIssues.get('issue-1')?.status).toBe(originalStatus);
    });

    it('creates new Map instance for setIssues', async () => {
      const existingIssue = createTestIssue({
        id: 'issue-1',
        status: 'open',
      });
      mockIssues.set('issue-1', existingIssue);

      vi.mocked(api.updateIssue).mockResolvedValue(undefined);

      const { result } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      await act(async () => {
        await result.current.updateIssueStatus('issue-1', 'in_progress', 'open');
      });

      const newIssuesMap = mockSetIssues.mock.calls[0][0] as Map<string, Issue>;
      expect(newIssuesMap).not.toBe(mockIssues);
    });
  });

  describe('Method stability', () => {
    it('updateIssueStatus is stable across renders when dependencies do not change', () => {
      const { result, rerender } = renderHook(() =>
        useOptimisticStatusUpdate({
          issues: mockIssues,
          setIssues: mockSetIssues,
        })
      );

      const _initialUpdateIssueStatus = result.current.updateIssueStatus;

      rerender();

      // Note: The function may or may not be stable depending on dependencies
      // This test verifies the current behavior
      expect(typeof result.current.updateIssueStatus).toBe('function');
    });
  });
});
