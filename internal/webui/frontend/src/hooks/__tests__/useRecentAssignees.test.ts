/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useRecentAssignees hook.
 * Tests localStorage persistence of recent assignee names.
 */

import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import { useRecentAssignees } from '../useRecentAssignees';

/**
 * Mock localStorage implementation for testing.
 */
function createMockLocalStorage(): {
  store: Map<string, string>;
  getItem: ReturnType<typeof vi.fn>;
  setItem: ReturnType<typeof vi.fn>;
  removeItem: ReturnType<typeof vi.fn>;
  clear: ReturnType<typeof vi.fn>;
} {
  const store = new Map<string, string>();

  return {
    store,
    getItem: vi.fn((key: string) => store.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => {
      store.set(key, value);
    }),
    removeItem: vi.fn((key: string) => {
      store.delete(key);
    }),
    clear: vi.fn(() => {
      store.clear();
    }),
  };
}

describe('useRecentAssignees', () => {
  let mockStorage: ReturnType<typeof createMockLocalStorage>;
  let originalLocalStorage: Storage;

  beforeEach(() => {
    // Save original localStorage
    originalLocalStorage = window.localStorage;

    // Create fresh mock storage
    mockStorage = createMockLocalStorage();

    // Replace localStorage with mock
    Object.defineProperty(window, 'localStorage', {
      value: {
        getItem: mockStorage.getItem,
        setItem: mockStorage.setItem,
        removeItem: mockStorage.removeItem,
        clear: mockStorage.clear,
        get length() {
          return mockStorage.store.size;
        },
        key: (index: number) => {
          const keys = Array.from(mockStorage.store.keys());
          return keys[index] ?? null;
        },
      },
      writable: true,
      configurable: true,
    });

    vi.clearAllMocks();
  });

  afterEach(() => {
    // Restore original localStorage
    Object.defineProperty(window, 'localStorage', {
      value: originalLocalStorage,
      writable: true,
      configurable: true,
    });
  });

  describe('Initial state', () => {
    it('returns empty array when localStorage is empty', () => {
      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toEqual([]);
    });

    it('loads initial state from localStorage', () => {
      mockStorage.store.set('beads-recent-assignees', JSON.stringify(['Alice', 'Bob', 'Charlie']));

      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toEqual(['Alice', 'Bob', 'Charlie']);
    });

    it('handles invalid JSON in localStorage gracefully', () => {
      mockStorage.store.set('beads-recent-assignees', 'not valid json {{{');

      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toEqual([]);
    });

    it('handles non-array JSON in localStorage gracefully', () => {
      mockStorage.store.set('beads-recent-assignees', JSON.stringify({ invalid: 'object' }));

      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toEqual([]);
    });

    it('filters out non-string items from localStorage', () => {
      mockStorage.store.set(
        'beads-recent-assignees',
        JSON.stringify(['Alice', 123, 'Bob', null, 'Charlie'])
      );

      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toEqual(['Alice', 'Bob', 'Charlie']);
    });
  });

  describe('addRecentAssignee', () => {
    it('adds a name to the front of the list', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      expect(result.current.recentAssignees).toEqual(['Alice']);

      act(() => {
        result.current.addRecentAssignee('Bob');
      });

      expect(result.current.recentAssignees).toEqual(['Bob', 'Alice']);
    });

    it('deduplicates names case-insensitively', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
        result.current.addRecentAssignee('Bob');
        result.current.addRecentAssignee('ALICE'); // Same as Alice, different case
      });

      // Should have ALICE at front (preserving case of most recent), followed by Bob
      expect(result.current.recentAssignees).toEqual(['ALICE', 'Bob']);
    });

    it('preserves the case of the most recent addition', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('alice');
      });
      expect(result.current.recentAssignees[0]).toBe('alice');

      act(() => {
        result.current.addRecentAssignee('ALICE');
      });
      expect(result.current.recentAssignees[0]).toBe('ALICE');

      act(() => {
        result.current.addRecentAssignee('Alice');
      });
      expect(result.current.recentAssignees[0]).toBe('Alice');
    });

    it('trims the list to max 5 items', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('One');
        result.current.addRecentAssignee('Two');
        result.current.addRecentAssignee('Three');
        result.current.addRecentAssignee('Four');
        result.current.addRecentAssignee('Five');
        result.current.addRecentAssignee('Six');
      });

      expect(result.current.recentAssignees).toHaveLength(5);
      expect(result.current.recentAssignees).toEqual(['Six', 'Five', 'Four', 'Three', 'Two']);
      // 'One' should have been dropped
      expect(result.current.recentAssignees).not.toContain('One');
    });

    it('ignores empty strings', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
        result.current.addRecentAssignee('');
      });

      expect(result.current.recentAssignees).toEqual(['Alice']);
    });

    it('ignores whitespace-only strings', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
        result.current.addRecentAssignee('   ');
        result.current.addRecentAssignee('\t\n');
      });

      expect(result.current.recentAssignees).toEqual(['Alice']);
    });

    it('trims whitespace from names', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('  Alice  ');
      });

      expect(result.current.recentAssignees).toEqual(['Alice']);
    });

    it('moves existing name to front when re-added', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
        result.current.addRecentAssignee('Bob');
        result.current.addRecentAssignee('Charlie');
      });

      expect(result.current.recentAssignees).toEqual(['Charlie', 'Bob', 'Alice']);

      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      expect(result.current.recentAssignees).toEqual(['Alice', 'Charlie', 'Bob']);
    });
  });

  describe('clearRecentAssignees', () => {
    it('clears all recent assignees', () => {
      mockStorage.store.set('beads-recent-assignees', JSON.stringify(['Alice', 'Bob', 'Charlie']));

      const { result } = renderHook(() => useRecentAssignees());

      expect(result.current.recentAssignees).toHaveLength(3);

      act(() => {
        result.current.clearRecentAssignees();
      });

      expect(result.current.recentAssignees).toEqual([]);
    });

    it('persists the empty state to localStorage', () => {
      mockStorage.store.set('beads-recent-assignees', JSON.stringify(['Alice', 'Bob']));

      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.clearRecentAssignees();
      });

      // Check localStorage was updated to empty array
      expect(mockStorage.setItem).toHaveBeenCalledWith('beads-recent-assignees', '[]');
    });
  });

  describe('localStorage persistence', () => {
    it('persists added names to localStorage', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      expect(mockStorage.setItem).toHaveBeenCalledWith(
        'beads-recent-assignees',
        JSON.stringify(['Alice'])
      );
    });

    it('persists multiple names in correct order', () => {
      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      act(() => {
        result.current.addRecentAssignee('Bob');
      });

      // Get the last call to setItem
      const lastCall = mockStorage.setItem.mock.calls[mockStorage.setItem.mock.calls.length - 1];
      expect(lastCall[0]).toBe('beads-recent-assignees');
      expect(JSON.parse(lastCall[1] as string)).toEqual(['Bob', 'Alice']);
    });

    it('survives component remount', () => {
      const { result, unmount } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
        result.current.addRecentAssignee('Bob');
      });

      unmount();

      // Render new hook instance
      const { result: result2 } = renderHook(() => useRecentAssignees());

      expect(result2.current.recentAssignees).toEqual(['Bob', 'Alice']);
    });
  });

  describe('localStorage unavailable', () => {
    it('handles localStorage getItem throwing gracefully', () => {
      mockStorage.getItem.mockImplementation(() => {
        throw new Error('localStorage not available');
      });

      const { result } = renderHook(() => useRecentAssignees());

      // Should return empty array instead of crashing
      expect(result.current.recentAssignees).toEqual([]);
    });

    it('handles localStorage setItem throwing gracefully', () => {
      mockStorage.setItem.mockImplementation(() => {
        throw new Error('localStorage quota exceeded');
      });

      const { result } = renderHook(() => useRecentAssignees());

      // Should still work in memory even if storage fails
      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      expect(result.current.recentAssignees).toEqual(['Alice']);
    });

    it('continues working after localStorage error', () => {
      // First call fails
      mockStorage.setItem.mockImplementationOnce(() => {
        throw new Error('First error');
      });

      const { result } = renderHook(() => useRecentAssignees());

      act(() => {
        result.current.addRecentAssignee('Alice');
      });

      // State should still update
      expect(result.current.recentAssignees).toEqual(['Alice']);

      // Subsequent adds should also work
      act(() => {
        result.current.addRecentAssignee('Bob');
      });

      expect(result.current.recentAssignees).toEqual(['Bob', 'Alice']);
    });
  });

  describe('return value stability', () => {
    it('returns stable function references', () => {
      const { result, rerender } = renderHook(() => useRecentAssignees());

      const addFn1 = result.current.addRecentAssignee;
      const clearFn1 = result.current.clearRecentAssignees;

      rerender();

      const addFn2 = result.current.addRecentAssignee;
      const clearFn2 = result.current.clearRecentAssignees;

      expect(addFn1).toBe(addFn2);
      expect(clearFn1).toBe(clearFn2);
    });
  });
});
