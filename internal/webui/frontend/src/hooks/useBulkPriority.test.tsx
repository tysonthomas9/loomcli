/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';
import { useBulkPriority, PRIORITY_OPTIONS } from './useBulkPriority';
import * as api from '@/api';

// Mock the API module
vi.mock('@/api', () => ({
  updateIssue: vi.fn(),
}));

const mockUpdateIssue = vi.mocked(api.updateIssue);

describe('useBulkPriority', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUpdateIssue.mockResolvedValue(
      {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
    );
  });

  describe('initial state', () => {
    it('starts with isLoading false', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));
      expect(result.current.isLoading).toBe(false);
    });

    it('starts with error null', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));
      expect(result.current.error).toBeNull();
    });

    it('starts with empty failedIds', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));
      expect(result.current.failedIds.size).toBe(0);
    });

    it('starts with successCount 0', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));
      expect(result.current.successCount).toBe(0);
    });

    it('starts with menu closed', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));
      expect(result.current.isMenuOpen).toBe(false);
    });
  });

  describe('bulkUpdatePriority', () => {
    it('sets isLoading to true during operation', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      let resolveUpdate: (value: unknown) => void;
      mockUpdateIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveUpdate = r;
          })
      );

      act(() => {
        result.current.bulkUpdatePriority(new Set(['id-1']), 1);
      });

      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveUpdate({});
      });

      expect(result.current.isLoading).toBe(false);
    });

    it('calls updateIssue with correct priority', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']) })
      );

      await act(async () => {
        await result.current.bulkUpdatePriority(new Set(['id-1', 'id-2']), 2);
      });

      expect(mockUpdateIssue).toHaveBeenCalledTimes(2);
      expect(mockUpdateIssue).toHaveBeenCalledWith('id-1', { priority: 2 });
      expect(mockUpdateIssue).toHaveBeenCalledWith('id-2', { priority: 2 });
    });

    it('accepts array of IDs', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2', 'id-3'], 0);
      });

      expect(mockUpdateIssue).toHaveBeenCalledTimes(3);
    });

    it('does nothing for empty set', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));

      await act(async () => {
        await result.current.bulkUpdatePriority(new Set(), 1);
      });

      expect(mockUpdateIssue).not.toHaveBeenCalled();
    });

    it('does nothing for empty array', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));

      await act(async () => {
        await result.current.bulkUpdatePriority([], 1);
      });

      expect(mockUpdateIssue).not.toHaveBeenCalled();
    });

    it('closes menu when operation starts', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      act(() => {
        result.current.openMenu();
      });
      expect(result.current.isMenuOpen).toBe(true);

      await act(async () => {
        await result.current.bulkUpdatePriority(new Set(['id-1']), 1);
      });

      expect(result.current.isMenuOpen).toBe(false);
    });
  });

  describe('success callback', () => {
    it('calls onSuccess when all issues update successfully', async () => {
      const onSuccess = vi.fn();
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']), onSuccess })
      );

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 3);
      });

      expect(onSuccess).toHaveBeenCalledWith(['id-1', 'id-2'], 3);
    });

    it('sets successCount correctly', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2', 'id-3']) })
      );

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2', 'id-3'], 1);
      });

      expect(result.current.successCount).toBe(3);
    });

    it('clears error on success', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      // First, simulate a failure
      mockUpdateIssue.mockRejectedValueOnce(new Error('Failed'));
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });
      expect(result.current.error).not.toBeNull();

      // Now success
      mockUpdateIssue.mockResolvedValue(
        {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
      );
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-2'], 2);
      });
      expect(result.current.error).toBeNull();
    });
  });

  describe('partial success callback', () => {
    it('calls onPartialSuccess when some issues fail', async () => {
      const onPartialSuccess = vi.fn();
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']), onPartialSuccess })
      );

      mockUpdateIssue
        .mockResolvedValueOnce(
          {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
        )
        .mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 2);
      });

      expect(onPartialSuccess).toHaveBeenCalledWith(['id-1'], ['id-2'], 2);
    });

    it('sets error message for partial success', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']) })
      );

      mockUpdateIssue
        .mockResolvedValueOnce(
          {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
        )
        .mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 1);
      });

      expect(result.current.error).toBe('Updated 1 of 2 issues');
    });

    it('tracks failed IDs', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']) })
      );

      mockUpdateIssue
        .mockResolvedValueOnce(
          {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
        )
        .mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 1);
      });

      expect(result.current.failedIds.has('id-2')).toBe(true);
      expect(result.current.failedIds.size).toBe(1);
    });

    it('sets correct successCount for partial success', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2', 'id-3']) })
      );

      mockUpdateIssue
        .mockResolvedValueOnce(
          {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
        )
        .mockResolvedValueOnce(
          {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
        )
        .mockRejectedValueOnce(new Error('Failed'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2', 'id-3'], 1);
      });

      expect(result.current.successCount).toBe(2);
      expect(result.current.failedIds.size).toBe(1);
    });
  });

  describe('error callback', () => {
    it('calls onError when all issues fail', async () => {
      const onError = vi.fn();
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']), onError })
      );

      mockUpdateIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 4);
      });

      expect(onError).toHaveBeenCalled();
      expect(onError.mock.calls[0][0]).toBeInstanceOf(Error);
      expect(onError.mock.calls[0][1]).toEqual(['id-1', 'id-2']);
    });

    it('sets error message from first failure', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      mockUpdateIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });

      expect(result.current.error).toBe('Network error');
    });

    it('sets all IDs as failed when all fail', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2', 'id-3']) })
      );

      mockUpdateIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2', 'id-3'], 0);
      });

      expect(result.current.failedIds.size).toBe(3);
      expect(result.current.failedIds.has('id-1')).toBe(true);
      expect(result.current.failedIds.has('id-2')).toBe(true);
      expect(result.current.failedIds.has('id-3')).toBe(true);
    });

    it('sets successCount to 0 when all fail', async () => {
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1', 'id-2']) })
      );

      mockUpdateIssue.mockRejectedValue(new Error('Network error'));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1', 'id-2'], 3);
      });

      expect(result.current.successCount).toBe(0);
    });
  });

  describe('menu state', () => {
    it('openMenu opens the menu', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      act(() => {
        result.current.openMenu();
      });

      expect(result.current.isMenuOpen).toBe(true);
    });

    it('closeMenu closes the menu', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      act(() => {
        result.current.openMenu();
      });
      expect(result.current.isMenuOpen).toBe(true);

      act(() => {
        result.current.closeMenu();
      });
      expect(result.current.isMenuOpen).toBe(false);
    });

    it('closes menu when Escape key is pressed', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      act(() => {
        result.current.openMenu();
      });
      expect(result.current.isMenuOpen).toBe(true);

      // Simulate Escape key press
      act(() => {
        const event = new KeyboardEvent('keydown', { key: 'Escape' });
        document.dispatchEvent(event);
      });

      expect(result.current.isMenuOpen).toBe(false);
    });

    it('closes menu when clicking outside', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      // Render the component to attach the ref
      const { container: _container } = render(<>{result.current.renderAction()}</>);

      act(() => {
        result.current.openMenu();
      });
      expect(result.current.isMenuOpen).toBe(true);

      // Simulate click outside the menu
      act(() => {
        const event = new MouseEvent('mousedown', { bubbles: true });
        document.body.dispatchEvent(event);
      });

      expect(result.current.isMenuOpen).toBe(false);
    });
  });

  describe('renderAction', () => {
    it('renders priority button', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      render(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-button')).toBeInTheDocument();
    });

    it('opens menu when button clicked', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      const { rerender } = render(<>{result.current.renderAction()}</>);
      fireEvent.click(screen.getByTestId('bulk-priority-button'));

      // Re-render to see menu
      rerender(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-menu')).toBeInTheDocument();
    });

    it('shows all priority options in menu', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      act(() => {
        result.current.openMenu();
      });

      render(<>{result.current.renderAction()}</>);

      PRIORITY_OPTIONS.forEach((option) => {
        expect(screen.getByTestId(`bulk-priority-option-${option.value}`)).toBeInTheDocument();
      });
    });

    it('button shows "Updating..." when loading', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      let resolveUpdate: (value: unknown) => void;
      mockUpdateIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveUpdate = r;
          })
      );

      act(() => {
        result.current.bulkUpdatePriority(['id-1'], 1);
      });

      const { rerender } = render(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-button')).toHaveTextContent('Updating...');

      await act(async () => {
        resolveUpdate({});
      });

      rerender(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-button')).toHaveTextContent('Priority');
    });

    it('button is disabled when loading', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      let resolveUpdate: (value: unknown) => void;
      mockUpdateIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveUpdate = r;
          })
      );

      act(() => {
        result.current.bulkUpdatePriority(['id-1'], 1);
      });

      render(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-button')).toBeDisabled();

      await act(async () => {
        resolveUpdate({});
      });
    });

    it('button is disabled when no selection', () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set() }));

      render(<>{result.current.renderAction()}</>);
      expect(screen.getByTestId('bulk-priority-button')).toBeDisabled();
    });

    it('clicking menu item triggers update with correct priority', async () => {
      const onSuccess = vi.fn();
      const { result } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1']), onSuccess })
      );

      act(() => {
        result.current.openMenu();
      });

      const { rerender: _rerender } = render(<>{result.current.renderAction()}</>);

      await act(async () => {
        fireEvent.click(screen.getByTestId('bulk-priority-option-2'));
      });

      expect(mockUpdateIssue).toHaveBeenCalledWith('id-1', { priority: 2 });
      expect(onSuccess).toHaveBeenCalledWith(['id-1'], 2);
    });
  });

  describe('reset', () => {
    it('clears error state', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      mockUpdateIssue.mockRejectedValue(new Error('Failed'));
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });
      expect(result.current.error).not.toBeNull();

      act(() => {
        result.current.reset();
      });

      expect(result.current.error).toBeNull();
    });

    it('clears failedIds', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      mockUpdateIssue.mockRejectedValue(new Error('Failed'));
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });
      expect(result.current.failedIds.size).toBeGreaterThan(0);

      act(() => {
        result.current.reset();
      });

      expect(result.current.failedIds.size).toBe(0);
    });

    it('resets successCount', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });
      expect(result.current.successCount).toBe(1);

      act(() => {
        result.current.reset();
      });

      expect(result.current.successCount).toBe(0);
    });
  });

  describe('PRIORITY_OPTIONS', () => {
    it('contains all 5 priority levels', () => {
      expect(PRIORITY_OPTIONS).toHaveLength(5);
      expect(PRIORITY_OPTIONS.map((o) => o.value)).toEqual([0, 1, 2, 3, 4]);
    });

    it('has descriptive labels', () => {
      expect(PRIORITY_OPTIONS[0].label).toContain('Critical');
      expect(PRIORITY_OPTIONS[1].label).toContain('High');
      expect(PRIORITY_OPTIONS[2].label).toContain('Medium');
      expect(PRIORITY_OPTIONS[3].label).toContain('Normal');
      expect(PRIORITY_OPTIONS[4].label).toContain('Backlog');
    });
  });

  describe('callback ref updates', () => {
    it('uses latest callback without re-triggering bulkUpdatePriority', async () => {
      const onSuccess1 = vi.fn();
      const onSuccess2 = vi.fn();

      const { result, rerender } = renderHook(
        ({ selectedIds, onSuccess }) => useBulkPriority({ selectedIds, onSuccess }),
        {
          initialProps: { selectedIds: new Set(['id-1']), onSuccess: onSuccess1 },
        }
      );

      // Update the callback
      rerender({ selectedIds: new Set(['id-1']), onSuccess: onSuccess2 });

      // Execute bulkUpdatePriority
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 2);
      });

      // Should call the new callback, not the old one
      expect(onSuccess1).not.toHaveBeenCalled();
      expect(onSuccess2).toHaveBeenCalledWith(['id-1'], 2);
    });
  });

  describe('unmount safety', () => {
    it('does not update state after unmount', async () => {
      const { result, unmount } = renderHook(() =>
        useBulkPriority({ selectedIds: new Set(['id-1']) })
      );

      let resolveUpdate: (value: unknown) => void;
      mockUpdateIssue.mockImplementation(
        () =>
          new Promise((r) => {
            resolveUpdate = r;
          })
      );

      // Start the operation
      act(() => {
        result.current.bulkUpdatePriority(['id-1'], 1);
      });

      // Unmount before completion
      unmount();

      // Resolve the promise (shouldn't throw or update state)
      await act(async () => {
        resolveUpdate({});
      });

      // The test passes if no error is thrown
      // We can't check state after unmount, but we can verify no React warnings
    });
  });

  describe('concurrent operations', () => {
    it('resets state at the start of each operation', async () => {
      const { result } = renderHook(() => useBulkPriority({ selectedIds: new Set(['id-1']) }));

      // First operation - failure
      mockUpdateIssue.mockRejectedValueOnce(new Error('Failed'));
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-1'], 1);
      });
      expect(result.current.failedIds.has('id-1')).toBe(true);
      expect(result.current.error).toBe('Failed');

      // Second operation - success with different IDs
      mockUpdateIssue.mockResolvedValue(
        {} as ReturnType<typeof api.updateIssue> extends Promise<infer T> ? T : never
      );
      await act(async () => {
        await result.current.bulkUpdatePriority(['id-2', 'id-3'], 2);
      });

      // Previous failed state should be cleared
      expect(result.current.failedIds.size).toBe(0);
      expect(result.current.error).toBeNull();
      expect(result.current.successCount).toBe(2);
    });
  });
});
