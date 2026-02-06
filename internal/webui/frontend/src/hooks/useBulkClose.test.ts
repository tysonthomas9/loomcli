/**
 * @vitest-environment jsdom
 */

import { renderHook, act, waitFor as _waitFor } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';

import * as api from '@/api';

import { useBulkClose } from './useBulkClose';

// Mock the API module
vi.mock('@/api', () => ({
  closeIssue: vi.fn(),
}));

const mockCloseIssue = vi.mocked(api.closeIssue);

describe('useBulkClose', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockCloseIssue.mockResolvedValue(undefined);
  });

  describe('initial state', () => {
    it('starts with isLoading false', () => {
      const { result } = renderHook(() => useBulkClose());
      expect(result.current.isLoading).toBe(false);
    });

    it('starts with error null', () => {
      const { result } = renderHook(() => useBulkClose());
      expect(result.current.error).toBeNull();
    });

    it('starts with empty failedIds', () => {
      const { result } = renderHook(() => useBulkClose());
      expect(result.current.failedIds.size).toBe(0);
    });

    it('starts with successCount 0', () => {
      const { result } = renderHook(() => useBulkClose());
      expect(result.current.successCount).toBe(0);
    });
  });

  describe('bulkClose', () => {
    it('sets isLoading to true during operation', async () => {
      const { result } = renderHook(() => useBulkClose());

      let resolveClose: () => void;
      mockCloseIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveClose = r as () => void;
          })
      );

      act(() => {
        result.current.bulkClose(new Set(['id-1']));
      });

      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveClose();
      });

      expect(result.current.isLoading).toBe(false);
    });

    it('accepts Set of IDs', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose(new Set(['id-1', 'id-2']));
      });

      expect(mockCloseIssue).toHaveBeenCalledTimes(2);
    });

    it('accepts array of IDs', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2', 'id-3']);
      });

      expect(mockCloseIssue).toHaveBeenCalledTimes(3);
    });

    it('does nothing for empty set', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose(new Set());
      });

      expect(mockCloseIssue).not.toHaveBeenCalled();
    });

    it('does nothing for empty array', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose([]);
      });

      expect(mockCloseIssue).not.toHaveBeenCalled();
    });

    it('passes closeReason to API', async () => {
      const { result } = renderHook(() => useBulkClose({ closeReason: 'Batch cleanup' }));

      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });

      expect(mockCloseIssue).toHaveBeenCalledWith('id-1', 'Batch cleanup');
    });
  });

  describe('success callback', () => {
    it('calls onSuccess when all issues close successfully', async () => {
      const onSuccess = vi.fn();
      const { result } = renderHook(() => useBulkClose({ onSuccess }));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(onSuccess).toHaveBeenCalledWith(['id-1', 'id-2']);
    });

    it('sets successCount correctly', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2', 'id-3']);
      });

      expect(result.current.successCount).toBe(3);
    });

    it('clears error on success', async () => {
      const { result } = renderHook(() => useBulkClose());

      // First, simulate a failure
      mockCloseIssue.mockRejectedValueOnce(new Error('Failed'));
      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });
      expect(result.current.error).not.toBeNull();

      // Now success
      mockCloseIssue.mockResolvedValue(undefined);
      await act(async () => {
        await result.current.bulkClose(['id-2']);
      });
      expect(result.current.error).toBeNull();
    });
  });

  describe('partial success callback', () => {
    it('calls onPartialSuccess when some issues fail', async () => {
      const onPartialSuccess = vi.fn();
      const { result } = renderHook(() => useBulkClose({ onPartialSuccess }));

      mockCloseIssue.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(onPartialSuccess).toHaveBeenCalledWith(['id-1'], ['id-2']);
    });

    it('sets error message for partial success', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(result.current.error).toBe('Closed 1 of 2 issues');
    });

    it('tracks failed IDs', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockResolvedValueOnce(undefined).mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(result.current.failedIds.has('id-2')).toBe(true);
      expect(result.current.failedIds.size).toBe(1);
    });

    it('sets correct successCount for partial success', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue
        .mockResolvedValueOnce(undefined)
        .mockResolvedValueOnce(undefined)
        .mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2', 'id-3']);
      });

      expect(result.current.successCount).toBe(2);
      expect(result.current.failedIds.size).toBe(1);
    });
  });

  describe('error callback', () => {
    it('calls onError when all issues fail', async () => {
      const onError = vi.fn();
      const { result } = renderHook(() => useBulkClose({ onError }));

      mockCloseIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(onError).toHaveBeenCalled();
      expect(onError.mock.calls[0][0]).toBeInstanceOf(Error);
      expect(onError.mock.calls[0][1]).toEqual(['id-1', 'id-2']);
    });

    it('sets error message from first failure', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });

      expect(result.current.error).toBe('Network error');
    });

    it('sets all IDs as failed when all fail', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2', 'id-3']);
      });

      expect(result.current.failedIds.size).toBe(3);
      expect(result.current.failedIds.has('id-1')).toBe(true);
      expect(result.current.failedIds.has('id-2')).toBe(true);
      expect(result.current.failedIds.has('id-3')).toBe(true);
    });

    it('sets successCount to 0 when all fail', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkClose(['id-1', 'id-2']);
      });

      expect(result.current.successCount).toBe(0);
    });
  });

  describe('createBulkAction', () => {
    it('creates a valid BulkAction object', () => {
      const { result } = renderHook(() => useBulkClose());

      const action = result.current.createBulkAction();

      expect(action.id).toBe('close');
      expect(action.label).toBe('Close');
      expect(action.variant).toBe('danger');
      expect(typeof action.onClick).toBe('function');
    });

    it('uses custom label', () => {
      const { result } = renderHook(() => useBulkClose());

      const action = result.current.createBulkAction({ label: 'Archive' });

      expect(action.label).toBe('Archive');
    });

    it('reflects loading state', async () => {
      const { result } = renderHook(() => useBulkClose());

      let resolveClose: () => void;
      mockCloseIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveClose = r as () => void;
          })
      );

      act(() => {
        result.current.bulkClose(['id-1']);
      });

      const action = result.current.createBulkAction();
      expect(action.loading).toBe(true);
      expect(action.disabled).toBe(true);

      await act(async () => {
        resolveClose();
      });

      const actionAfter = result.current.createBulkAction();
      expect(actionAfter.loading).toBe(false);
      expect(actionAfter.disabled).toBe(false);
    });

    it('onClick calls bulkClose with selectedIds', async () => {
      const { result } = renderHook(() => useBulkClose());

      const action = result.current.createBulkAction();
      const selectedIds = new Set(['id-1', 'id-2']);

      await act(async () => {
        await action.onClick(selectedIds);
      });

      expect(mockCloseIssue).toHaveBeenCalledTimes(2);
    });
  });

  describe('reset', () => {
    it('clears error state', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockRejectedValue(new Error('Failed'));
      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });
      expect(result.current.error).not.toBeNull();

      act(() => {
        result.current.reset();
      });

      expect(result.current.error).toBeNull();
    });

    it('clears failedIds', async () => {
      const { result } = renderHook(() => useBulkClose());

      mockCloseIssue.mockRejectedValue(new Error('Failed'));
      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });
      expect(result.current.failedIds.size).toBeGreaterThan(0);

      act(() => {
        result.current.reset();
      });

      expect(result.current.failedIds.size).toBe(0);
    });

    it('resets successCount', async () => {
      const { result } = renderHook(() => useBulkClose());

      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });
      expect(result.current.successCount).toBe(1);

      act(() => {
        result.current.reset();
      });

      expect(result.current.successCount).toBe(0);
    });
  });

  describe('callback ref updates', () => {
    it('uses latest callback without re-triggering bulkClose', async () => {
      const onSuccess1 = vi.fn();
      const onSuccess2 = vi.fn();

      const { result, rerender } = renderHook(({ onSuccess }) => useBulkClose({ onSuccess }), {
        initialProps: { onSuccess: onSuccess1 },
      });

      // Update the callback
      rerender({ onSuccess: onSuccess2 });

      // Execute bulkClose
      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });

      // Should call the new callback, not the old one
      expect(onSuccess1).not.toHaveBeenCalled();
      expect(onSuccess2).toHaveBeenCalledWith(['id-1']);
    });
  });

  describe('unmount safety', () => {
    it('does not update state after unmount', async () => {
      const { result, unmount } = renderHook(() => useBulkClose());

      let resolveClose: () => void;
      mockCloseIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveClose = r as () => void;
          })
      );

      // Start the operation
      act(() => {
        result.current.bulkClose(['id-1']);
      });

      // Unmount before completion
      unmount();

      // Resolve the promise (shouldn't throw or update state)
      await act(async () => {
        resolveClose();
      });

      // The test passes if no error is thrown
      // We can't check state after unmount, but we can verify no React warnings
    });
  });

  describe('concurrent operations', () => {
    it('resets state at the start of each operation', async () => {
      const { result } = renderHook(() => useBulkClose());

      // First operation - failure
      mockCloseIssue.mockRejectedValueOnce(new Error('Failed'));
      await act(async () => {
        await result.current.bulkClose(['id-1']);
      });
      expect(result.current.failedIds.has('id-1')).toBe(true);
      expect(result.current.error).toBe('Failed');

      // Second operation - success with different IDs
      mockCloseIssue.mockResolvedValue(undefined);
      await act(async () => {
        await result.current.bulkClose(['id-2', 'id-3']);
      });

      // Previous failed state should be cleared
      expect(result.current.failedIds.size).toBe(0);
      expect(result.current.error).toBeNull();
      expect(result.current.successCount).toBe(2);
    });
  });
});
