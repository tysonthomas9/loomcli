/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';

import type { Priority } from '@/types';

import {
  useFilterState,
  toQueryString,
  parseFromUrl,
  isEmptyFilter,
  type FilterState,
  type GroupByOption,
} from '../useFilterState';

/**
 * Mock window.location and window.history for URL sync tests.
 */
function mockWindowLocation(search = ''): void {
  Object.defineProperty(window, 'location', {
    value: {
      pathname: '/issues',
      search,
      href: `http://localhost:3000/issues${search}`,
    },
    writable: true,
    configurable: true,
  });
}

function mockWindowHistory(): { replaceState: ReturnType<typeof vi.fn> } {
  const replaceState = vi.fn();
  Object.defineProperty(window, 'history', {
    value: {
      replaceState,
      pushState: vi.fn(),
    },
    writable: true,
    configurable: true,
  });
  return { replaceState };
}

describe('useFilterState', () => {
  beforeEach(() => {
    mockWindowLocation();
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe('initial state', () => {
    it('has all fields undefined by default', () => {
      const { result } = renderHook(() => useFilterState());

      const [state] = result.current;
      expect(state.priority).toBeUndefined();
      expect(state.type).toBeUndefined();
      expect(state.labels).toBeUndefined();
      expect(state.search).toBeUndefined();
    });

    it('returns empty object when no URL params present', () => {
      mockWindowLocation('');
      const { result } = renderHook(() => useFilterState());

      const [state] = result.current;
      expect(Object.keys(state)).toHaveLength(0);
    });

    it('does not read URL when syncUrl is false', () => {
      mockWindowLocation('?priority=2&type=bug');
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      const [state] = result.current;
      expect(state.priority).toBeUndefined();
      expect(state.type).toBeUndefined();
    });
  });

  describe('setPriority', () => {
    it('updates state with valid priority', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(2 as Priority);
      });

      expect(result.current[0].priority).toBe(2);
    });

    it('handles P0 (critical) priority correctly', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(0 as Priority);
      });

      expect(result.current[0].priority).toBe(0);
    });

    it('handles P4 (backlog) priority correctly', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(4 as Priority);
      });

      expect(result.current[0].priority).toBe(4);
    });

    it('clears priority when set to undefined', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(2 as Priority);
      });
      expect(result.current[0].priority).toBe(2);

      act(() => {
        result.current[1].setPriority(undefined);
      });
      expect(result.current[0].priority).toBeUndefined();
    });
  });

  describe('setType', () => {
    it('updates state with bug type', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setType('bug');
      });

      expect(result.current[0].type).toBe('bug');
    });

    it('handles all known issue types', () => {
      const types = ['bug', 'feature', 'task', 'epic', 'chore'] as const;
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      for (const type of types) {
        act(() => {
          result.current[1].setType(type);
        });
        expect(result.current[0].type).toBe(type);
      }
    });

    it('handles custom issue types', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setType('custom-type');
      });

      expect(result.current[0].type).toBe('custom-type');
    });

    it('clears type when set to undefined', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setType('bug');
      });
      expect(result.current[0].type).toBe('bug');

      act(() => {
        result.current[1].setType(undefined);
      });
      expect(result.current[0].type).toBeUndefined();
    });
  });

  describe('setLabels', () => {
    it('updates state with single label', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setLabels(['phase-1']);
      });

      expect(result.current[0].labels).toEqual(['phase-1']);
    });

    it('updates state with multiple labels', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setLabels(['phase-1', 'frontend']);
      });

      expect(result.current[0].labels).toEqual(['phase-1', 'frontend']);
    });

    it('handles empty array', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setLabels(['phase-1']);
      });
      expect(result.current[0].labels).toEqual(['phase-1']);

      act(() => {
        result.current[1].setLabels([]);
      });
      // Empty array is set directly (not converted to undefined by setLabels)
      expect(result.current[0].labels).toEqual([]);
    });

    it('clears labels when set to undefined', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setLabels(['phase-1']);
      });

      act(() => {
        result.current[1].setLabels(undefined);
      });

      expect(result.current[0].labels).toBeUndefined();
    });
  });

  describe('setSearch', () => {
    it('updates state with search text', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setSearch('authentication');
      });

      expect(result.current[0].search).toBe('authentication');
    });

    it('handles search with special characters', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setSearch('bug & feature');
      });

      expect(result.current[0].search).toBe('bug & feature');
    });

    it('handles empty string search', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setSearch('test');
      });
      act(() => {
        result.current[1].setSearch('');
      });

      // Empty string is set directly (not converted to undefined by setSearch)
      expect(result.current[0].search).toBe('');
    });

    it('clears search when set to undefined', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setSearch('test');
      });
      act(() => {
        result.current[1].setSearch(undefined);
      });

      expect(result.current[0].search).toBeUndefined();
    });
  });

  describe('clearFilter', () => {
    it('clears only priority when specified', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      // Set multiple filters
      act(() => {
        result.current[1].setPriority(2 as Priority);
        result.current[1].setType('bug');
        result.current[1].setSearch('test');
      });

      // Clear just priority
      act(() => {
        result.current[1].clearFilter('priority');
      });

      expect(result.current[0].priority).toBeUndefined();
      expect(result.current[0].type).toBe('bug');
      expect(result.current[0].search).toBe('test');
    });

    it('clears only type when specified', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(1 as Priority);
        result.current[1].setType('feature');
      });

      act(() => {
        result.current[1].clearFilter('type');
      });

      expect(result.current[0].priority).toBe(1);
      expect(result.current[0].type).toBeUndefined();
    });

    it('clears only labels when specified', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setLabels(['phase-1', 'frontend']);
        result.current[1].setSearch('test');
      });

      act(() => {
        result.current[1].clearFilter('labels');
      });

      expect(result.current[0].labels).toBeUndefined();
      expect(result.current[0].search).toBe('test');
    });

    it('clears only search when specified', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setSearch('authentication');
        result.current[1].setType('bug');
      });

      act(() => {
        result.current[1].clearFilter('search');
      });

      expect(result.current[0].search).toBeUndefined();
      expect(result.current[0].type).toBe('bug');
    });
  });

  describe('clearAll', () => {
    it('resets all filters to empty state', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      // Set all filters
      act(() => {
        result.current[1].setPriority(2 as Priority);
        result.current[1].setType('bug');
        result.current[1].setLabels(['phase-1', 'frontend']);
        result.current[1].setSearch('authentication');
      });

      // Verify all are set
      expect(result.current[0].priority).toBe(2);
      expect(result.current[0].type).toBe('bug');
      expect(result.current[0].labels).toEqual(['phase-1', 'frontend']);
      expect(result.current[0].search).toBe('authentication');

      // Clear all
      act(() => {
        result.current[1].clearAll();
      });

      // Verify all are cleared
      expect(result.current[0].priority).toBeUndefined();
      expect(result.current[0].type).toBeUndefined();
      expect(result.current[0].labels).toBeUndefined();
      expect(result.current[0].search).toBeUndefined();
    });

    it('returns empty object after clearAll', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setPriority(1 as Priority);
      });

      act(() => {
        result.current[1].clearAll();
      });

      expect(Object.keys(result.current[0])).toHaveLength(0);
    });

    it('clears groupBy along with other filters', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setGroupBy('epic');
        result.current[1].setPriority(2 as Priority);
      });

      expect(result.current[0].groupBy).toBe('epic');
      expect(result.current[0].priority).toBe(2);

      act(() => {
        result.current[1].clearAll();
      });

      expect(result.current[0].groupBy).toBeUndefined();
      expect(result.current[0].priority).toBeUndefined();
    });
  });

  describe('setGroupBy', () => {
    it('updates state with valid groupBy option', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setGroupBy('epic');
      });

      expect(result.current[0].groupBy).toBe('epic');
    });

    it('handles all known groupBy options', () => {
      const options: GroupByOption[] = ['none', 'epic', 'assignee', 'priority', 'type', 'label'];
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      for (const option of options) {
        act(() => {
          result.current[1].setGroupBy(option);
        });
        expect(result.current[0].groupBy).toBe(option);
      }
    });

    it('clears groupBy when set to undefined', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setGroupBy('epic');
      });
      expect(result.current[0].groupBy).toBe('epic');

      act(() => {
        result.current[1].setGroupBy(undefined);
      });
      expect(result.current[0].groupBy).toBeUndefined();
    });
  });

  describe('clearFilter for groupBy', () => {
    it('clears only groupBy when specified', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setGroupBy('assignee');
        result.current[1].setType('bug');
      });

      act(() => {
        result.current[1].clearFilter('groupBy');
      });

      expect(result.current[0].groupBy).toBeUndefined();
      expect(result.current[0].type).toBe('bug');
    });
  });

  describe('toggling filters', () => {
    it('setting then clearing priority works correctly', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      // Set priority
      act(() => {
        result.current[1].setPriority(3 as Priority);
      });
      expect(result.current[0].priority).toBe(3);

      // Clear priority
      act(() => {
        result.current[1].setPriority(undefined);
      });
      expect(result.current[0].priority).toBeUndefined();

      // Set again
      act(() => {
        result.current[1].setPriority(1 as Priority);
      });
      expect(result.current[0].priority).toBe(1);
    });

    it('changing type multiple times works correctly', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      act(() => {
        result.current[1].setType('bug');
      });
      expect(result.current[0].type).toBe('bug');

      act(() => {
        result.current[1].setType('feature');
      });
      expect(result.current[0].type).toBe('feature');

      act(() => {
        result.current[1].setType('task');
      });
      expect(result.current[0].type).toBe('task');
    });

    it('adding and removing labels works correctly', () => {
      const { result } = renderHook(() => useFilterState({ syncUrl: false }));

      // Add initial labels
      act(() => {
        result.current[1].setLabels(['phase-1']);
      });
      expect(result.current[0].labels).toEqual(['phase-1']);

      // Add more labels
      act(() => {
        result.current[1].setLabels(['phase-1', 'frontend', 'urgent']);
      });
      expect(result.current[0].labels).toEqual(['phase-1', 'frontend', 'urgent']);

      // Remove some labels
      act(() => {
        result.current[1].setLabels(['phase-1']);
      });
      expect(result.current[0].labels).toEqual(['phase-1']);

      // Clear all labels
      act(() => {
        result.current[1].clearFilter('labels');
      });
      expect(result.current[0].labels).toBeUndefined();
    });
  });
});

describe('toQueryString', () => {
  it('returns empty string for empty filter state', () => {
    const result = toQueryString({});
    expect(result).toBe('');
  });

  it('serializes priority correctly', () => {
    const result = toQueryString({ priority: 2 as Priority });
    expect(result).toBe('priority=2');
  });

  it('serializes P0 priority correctly', () => {
    const result = toQueryString({ priority: 0 as Priority });
    expect(result).toBe('priority=0');
  });

  it('serializes type correctly', () => {
    const result = toQueryString({ type: 'bug' });
    expect(result).toBe('type=bug');
  });

  it('serializes single label correctly', () => {
    const result = toQueryString({ labels: ['phase-1'] });
    expect(result).toBe('labels=phase-1');
  });

  it('serializes multiple labels as comma-separated', () => {
    const result = toQueryString({ labels: ['phase-1', 'frontend'] });
    expect(result).toBe('labels=phase-1%2Cfrontend');
  });

  it('serializes search correctly', () => {
    const result = toQueryString({ search: 'authentication' });
    expect(result).toBe('search=authentication');
  });

  it('encodes special characters in search', () => {
    const result = toQueryString({ search: 'bug & feature' });
    expect(result).toBe('search=bug+%26+feature');
  });

  it('serializes multiple filter fields', () => {
    const state: FilterState = {
      priority: 1 as Priority,
      type: 'bug',
      labels: ['urgent'],
      search: 'auth',
    };
    const result = toQueryString(state);

    // Check each param is present (order may vary)
    expect(result).toContain('priority=1');
    expect(result).toContain('type=bug');
    expect(result).toContain('labels=urgent');
    expect(result).toContain('search=auth');
  });

  it('omits empty labels array', () => {
    const result = toQueryString({ labels: [] });
    expect(result).toBe('');
  });

  it('omits empty search string', () => {
    const result = toQueryString({ search: '' });
    expect(result).toBe('');
  });

  it('serializes groupBy correctly', () => {
    const result = toQueryString({ groupBy: 'assignee' });
    expect(result).toBe('groupBy=assignee');
  });

  it('omits groupBy when value is none', () => {
    const result = toQueryString({ groupBy: 'none' });
    expect(result).toBe('');
  });

  it('serializes groupBy when value is epic (no longer default)', () => {
    const result = toQueryString({ groupBy: 'epic' });
    expect(result).toBe('groupBy=epic');
  });

  it('serializes all groupBy options except none and epic', () => {
    const options: GroupByOption[] = ['assignee', 'priority', 'type', 'label'];
    for (const option of options) {
      const result = toQueryString({ groupBy: option });
      expect(result).toBe(`groupBy=${option}`);
    }
  });
});

describe('parseFromUrl', () => {
  beforeEach(() => {
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('returns empty state when no URL params', () => {
    mockWindowLocation('');
    const result = parseFromUrl();
    expect(Object.keys(result)).toHaveLength(0);
  });

  it('parses priority from URL', () => {
    mockWindowLocation('?priority=2');
    const result = parseFromUrl();
    expect(result.priority).toBe(2);
  });

  it('parses P0 priority from URL', () => {
    mockWindowLocation('?priority=0');
    const result = parseFromUrl();
    expect(result.priority).toBe(0);
  });

  it('ignores invalid priority values', () => {
    mockWindowLocation('?priority=invalid');
    const result = parseFromUrl();
    expect(result.priority).toBeUndefined();
  });

  it('ignores out-of-range priority values', () => {
    mockWindowLocation('?priority=5');
    const result = parseFromUrl();
    expect(result.priority).toBeUndefined();
  });

  it('ignores negative priority values', () => {
    mockWindowLocation('?priority=-1');
    const result = parseFromUrl();
    expect(result.priority).toBeUndefined();
  });

  it('parses type from URL', () => {
    mockWindowLocation('?type=bug');
    const result = parseFromUrl();
    expect(result.type).toBe('bug');
  });

  it('parses custom type from URL', () => {
    mockWindowLocation('?type=custom-type');
    const result = parseFromUrl();
    expect(result.type).toBe('custom-type');
  });

  it('ignores empty type in URL', () => {
    mockWindowLocation('?type=');
    const result = parseFromUrl();
    expect(result.type).toBeUndefined();
  });

  it('parses single label from URL', () => {
    mockWindowLocation('?labels=phase-1');
    const result = parseFromUrl();
    expect(result.labels).toEqual(['phase-1']);
  });

  it('parses multiple comma-separated labels from URL', () => {
    mockWindowLocation('?labels=phase-1,frontend,urgent');
    const result = parseFromUrl();
    expect(result.labels).toEqual(['phase-1', 'frontend', 'urgent']);
  });

  it('ignores empty labels in URL', () => {
    mockWindowLocation('?labels=');
    const result = parseFromUrl();
    expect(result.labels).toBeUndefined();
  });

  it('filters out empty labels from comma-separated list', () => {
    mockWindowLocation('?labels=phase-1,,frontend');
    const result = parseFromUrl();
    expect(result.labels).toEqual(['phase-1', 'frontend']);
  });

  it('parses search from URL', () => {
    mockWindowLocation('?search=authentication');
    const result = parseFromUrl();
    expect(result.search).toBe('authentication');
  });

  it('decodes URL-encoded search', () => {
    mockWindowLocation('?search=bug%20%26%20feature');
    const result = parseFromUrl();
    expect(result.search).toBe('bug & feature');
  });

  it('ignores empty search in URL', () => {
    mockWindowLocation('?search=');
    const result = parseFromUrl();
    expect(result.search).toBeUndefined();
  });

  it('parses multiple filter params from URL', () => {
    mockWindowLocation('?priority=1&type=bug&labels=urgent,frontend&search=auth');
    const result = parseFromUrl();
    expect(result.priority).toBe(1);
    expect(result.type).toBe('bug');
    expect(result.labels).toEqual(['urgent', 'frontend']);
    expect(result.search).toBe('auth');
  });

  it('parses groupBy from URL', () => {
    mockWindowLocation('?groupBy=epic');
    const result = parseFromUrl();
    expect(result.groupBy).toBe('epic');
  });

  it('parses all valid groupBy options from URL', () => {
    const options: GroupByOption[] = ['none', 'epic', 'assignee', 'priority', 'type', 'label'];
    for (const option of options) {
      mockWindowLocation(`?groupBy=${option}`);
      const result = parseFromUrl();
      expect(result.groupBy).toBe(option);
    }
  });

  it('ignores invalid groupBy values', () => {
    mockWindowLocation('?groupBy=invalid');
    const result = parseFromUrl();
    expect(result.groupBy).toBeUndefined();
  });

  it('ignores empty groupBy in URL', () => {
    mockWindowLocation('?groupBy=');
    const result = parseFromUrl();
    expect(result.groupBy).toBeUndefined();
  });

  it('parses groupBy with other params from URL', () => {
    mockWindowLocation('?priority=2&groupBy=assignee&type=bug');
    const result = parseFromUrl();
    expect(result.priority).toBe(2);
    expect(result.groupBy).toBe('assignee');
    expect(result.type).toBe('bug');
  });
});

describe('isEmptyFilter', () => {
  it('returns true for empty object', () => {
    expect(isEmptyFilter({})).toBe(true);
  });

  it('returns true when all fields are undefined', () => {
    const state: FilterState = {
      priority: undefined,
      type: undefined,
      labels: undefined,
      search: undefined,
    };
    expect(isEmptyFilter(state)).toBe(true);
  });

  it('returns true for empty labels array', () => {
    expect(isEmptyFilter({ labels: [] })).toBe(true);
  });

  it('returns true for empty search string', () => {
    expect(isEmptyFilter({ search: '' })).toBe(true);
  });

  it('returns true for combination of empty values', () => {
    expect(isEmptyFilter({ labels: [], search: '' })).toBe(true);
  });

  it('returns false when priority is set', () => {
    expect(isEmptyFilter({ priority: 2 as Priority })).toBe(false);
  });

  it('returns false when P0 priority is set', () => {
    expect(isEmptyFilter({ priority: 0 as Priority })).toBe(false);
  });

  it('returns false when type is set', () => {
    expect(isEmptyFilter({ type: 'bug' })).toBe(false);
  });

  it('returns false when labels has values', () => {
    expect(isEmptyFilter({ labels: ['phase-1'] })).toBe(false);
  });

  it('returns false when search has text', () => {
    expect(isEmptyFilter({ search: 'test' })).toBe(false);
  });

  it('returns true when groupBy is none', () => {
    expect(isEmptyFilter({ groupBy: 'none' })).toBe(true);
  });

  it('returns true when groupBy is undefined', () => {
    expect(isEmptyFilter({ groupBy: undefined })).toBe(true);
  });

  it('returns false when groupBy is epic (no longer default)', () => {
    expect(isEmptyFilter({ groupBy: 'epic' })).toBe(false);
  });

  it('returns false when groupBy is set to a non-default value', () => {
    expect(isEmptyFilter({ groupBy: 'assignee' })).toBe(false);
    expect(isEmptyFilter({ groupBy: 'priority' })).toBe(false);
    expect(isEmptyFilter({ groupBy: 'type' })).toBe(false);
    expect(isEmptyFilter({ groupBy: 'label' })).toBe(false);
  });

  it('returns false when any field is set', () => {
    const fullState: FilterState = {
      priority: 1 as Priority,
      type: 'feature',
      labels: ['urgent'],
      search: 'auth',
    };
    expect(isEmptyFilter(fullState)).toBe(false);
  });
});

describe('URL synchronization', () => {
  let historyMock: { replaceState: ReturnType<typeof vi.fn> };

  beforeEach(() => {
    mockWindowLocation('');
    historyMock = mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('updates URL when filter state changes', () => {
    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setPriority(2 as Priority);
    });

    expect(historyMock.replaceState).toHaveBeenCalledWith(null, '', '/issues?priority=2');
  });

  it('removes query string when all filters cleared', () => {
    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setPriority(1 as Priority);
    });

    act(() => {
      result.current[1].clearAll();
    });

    // Last call should be to pathname only (no query string)
    const lastCall = historyMock.replaceState.mock.calls.at(-1);
    expect(lastCall?.[2]).toBe('/issues');
  });

  it('preserves pathname when updating URL', () => {
    mockWindowLocation('');
    Object.defineProperty(window.location, 'pathname', {
      value: '/board',
      configurable: true,
    });

    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setType('bug');
    });

    expect(historyMock.replaceState).toHaveBeenCalledWith(null, '', '/board?type=bug');
  });

  it('initializes from URL params on mount', () => {
    mockWindowLocation('?priority=3&type=feature');

    const { result } = renderHook(() => useFilterState());

    expect(result.current[0].priority).toBe(3);
    expect(result.current[0].type).toBe('feature');
  });

  it('does not sync URL when syncUrl is false', () => {
    const { result } = renderHook(() => useFilterState({ syncUrl: false }));

    act(() => {
      result.current[1].setPriority(2 as Priority);
    });

    // replaceState should not be called for filter changes
    // (it may be called initially, so we check specifically for our change)
    const calls = historyMock.replaceState.mock.calls;
    const priorityCall = calls.find((call) => call[2]?.includes('priority=2'));
    expect(priorityCall).toBeUndefined();
  });

  it('updates URL when groupBy changes', () => {
    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setGroupBy('assignee');
    });

    expect(historyMock.replaceState).toHaveBeenCalledWith(null, '', '/issues?groupBy=assignee');
  });

  it('adds groupBy=epic to URL (epic is no longer default)', () => {
    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setGroupBy('epic');
    });

    const lastCall = historyMock.replaceState.mock.calls.at(-1);
    expect(lastCall?.[2]).toBe('/issues?groupBy=epic');
  });

  it('initializes groupBy from URL params on mount', () => {
    mockWindowLocation('?groupBy=priority');

    const { result } = renderHook(() => useFilterState());

    expect(result.current[0].groupBy).toBe('priority');
  });

  it('does not add groupBy=none to URL', () => {
    const { result } = renderHook(() => useFilterState());

    act(() => {
      result.current[1].setGroupBy('none');
    });

    // When groupBy is 'none', the URL should not include it (it's the default)
    const lastCall = historyMock.replaceState.mock.calls.at(-1);
    expect(lastCall?.[2]).toBe('/issues');
  });
});

describe('popstate handling', () => {
  beforeEach(() => {
    mockWindowLocation('');
    mockWindowHistory();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('updates state on browser back/forward navigation', () => {
    mockWindowLocation('?priority=1');
    const { result } = renderHook(() => useFilterState());

    expect(result.current[0].priority).toBe(1);

    // Simulate browser navigation (change URL and fire popstate)
    act(() => {
      mockWindowLocation('?priority=3&type=bug');
      window.dispatchEvent(new PopStateEvent('popstate'));
    });

    expect(result.current[0].priority).toBe(3);
    expect(result.current[0].type).toBe('bug');
  });

  it('cleans up popstate listener on unmount', () => {
    const removeEventListenerSpy = vi.spyOn(window, 'removeEventListener');

    const { unmount } = renderHook(() => useFilterState());

    unmount();

    expect(removeEventListenerSpy).toHaveBeenCalledWith('popstate', expect.any(Function));
  });

  it('does not add popstate listener when syncUrl is false', () => {
    const addEventListenerSpy = vi.spyOn(window, 'addEventListener');

    renderHook(() => useFilterState({ syncUrl: false }));

    const popstateCall = addEventListenerSpy.mock.calls.find((call) => call[0] === 'popstate');
    expect(popstateCall).toBeUndefined();
  });
});

describe('action reference stability', () => {
  it('actions object is stable across re-renders', () => {
    const { result, rerender } = renderHook(() => useFilterState({ syncUrl: false }));

    const actions1 = result.current[1];

    rerender();

    const actions2 = result.current[1];

    expect(actions1).toBe(actions2);
  });

  it('individual action functions are stable', () => {
    const { result, rerender } = renderHook(() => useFilterState({ syncUrl: false }));

    const setPriority1 = result.current[1].setPriority;
    const setType1 = result.current[1].setType;
    const clearAll1 = result.current[1].clearAll;

    rerender();

    expect(result.current[1].setPriority).toBe(setPriority1);
    expect(result.current[1].setType).toBe(setType1);
    expect(result.current[1].clearAll).toBe(clearAll1);
  });
});
