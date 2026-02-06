/**
 * useFilterState - React hook for managing filter state with URL synchronization.
 * Provides centralized filter state management for Kanban board and list views.
 */

import { useState, useCallback, useEffect, useMemo } from 'react';

import type { Priority, IssueType } from '@/types';

/**
 * Group by option for swim lane grouping.
 * 'none' = flat view (no grouping).
 */
export type GroupByOption = 'none' | 'epic' | 'assignee' | 'priority' | 'type' | 'label';

/**
 * Valid group by options for URL validation.
 */
const VALID_GROUP_BY_OPTIONS: ReadonlySet<string> = new Set([
  'none',
  'epic',
  'assignee',
  'priority',
  'type',
  'label',
]);

/**
 * Filter state for UI filtering.
 * Subset of IssueFilter relevant for client-side filtering.
 */
export interface FilterState {
  /** Priority filter (0-4), undefined for "all" */
  priority?: Priority;
  /** Issue type filter, undefined for "all" */
  type?: IssueType;
  /** Label filters */
  labels?: string[];
  /** Free-text search */
  search?: string;
  /** Whether to show blocked issues (default: false = hide blocked) */
  showBlocked?: boolean;
  /** Group by option for swim lanes */
  groupBy?: GroupByOption;
}

/**
 * Actions for updating filter state.
 */
export interface FilterActions {
  /** Set priority filter */
  setPriority: (priority: Priority | undefined) => void;
  /** Set issue type filter */
  setType: (type: IssueType | undefined) => void;
  /** Set label filters */
  setLabels: (labels: string[] | undefined) => void;
  /** Set search text */
  setSearch: (search: string | undefined) => void;
  /** Set show blocked toggle */
  setShowBlocked: (showBlocked: boolean | undefined) => void;
  /** Set group by option */
  setGroupBy: (groupBy: GroupByOption | undefined) => void;
  /** Clear a specific filter */
  clearFilter: (key: keyof FilterState) => void;
  /** Clear all filters */
  clearAll: () => void;
}

/**
 * Options for useFilterState hook.
 */
export interface UseFilterStateOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
}

/**
 * Return type for useFilterState hook.
 */
export type UseFilterStateReturn = [FilterState, FilterActions];

/**
 * Default group by option for swim lane display.
 * When no groupBy is specified in URL, the UI defaults to epic swim lanes.
 */
export const DEFAULT_GROUP_BY: GroupByOption = 'none';

/**
 * Default empty filter state.
 */
const DEFAULT_FILTER_STATE: FilterState = {};

/**
 * Check if running in browser environment.
 */
function isBrowser(): boolean {
  return typeof window !== 'undefined' && typeof window.location !== 'undefined';
}

/**
 * Parse priority from URL parameter.
 * Returns undefined for invalid values.
 */
function parsePriority(value: string | null): Priority | undefined {
  if (value === null) return undefined;
  const num = parseInt(value, 10);
  if (isNaN(num) || num < 0 || num > 4) return undefined;
  return num as Priority;
}

/**
 * Parse labels from URL parameter.
 * Labels are comma-separated in URL.
 */
function parseLabels(value: string | null): string[] | undefined {
  if (value === null || value === '') return undefined;
  const labels = value.split(',').filter((l) => l.length > 0);
  return labels.length > 0 ? labels : undefined;
}

/**
 * Parse issue type from URL parameter.
 * Returns undefined for empty values, allows any non-empty string.
 */
function parseType(value: string | null): IssueType | undefined {
  if (value === null || value === '') return undefined;
  return value as IssueType;
}

/**
 * Parse search from URL parameter.
 */
function parseSearch(value: string | null): string | undefined {
  if (value === null || value === '') return undefined;
  return value;
}

/**
 * Parse showBlocked from URL parameter.
 * Returns true if 'true', undefined otherwise (default is false/hidden).
 */
function parseShowBlocked(value: string | null): boolean | undefined {
  if (value === 'true') return true;
  return undefined;
}

/**
 * Parse groupBy from URL parameter.
 * Returns undefined for invalid values (defaults to 'none' in UI).
 */
function parseGroupBy(value: string | null): GroupByOption | undefined {
  if (value === null || value === '') return undefined;
  if (VALID_GROUP_BY_OPTIONS.has(value)) return value as GroupByOption;
  return undefined;
}

/**
 * Build filter state from parsed values.
 * Only includes keys with defined values to satisfy exactOptionalPropertyTypes.
 */
function buildFilterState(
  priority: Priority | undefined,
  type: IssueType | undefined,
  labels: string[] | undefined,
  search: string | undefined,
  showBlocked: boolean | undefined,
  groupBy: GroupByOption | undefined
): FilterState {
  const state: FilterState = {};
  if (priority !== undefined) state.priority = priority;
  if (type !== undefined) state.type = type;
  if (labels !== undefined) state.labels = labels;
  if (search !== undefined) state.search = search;
  if (showBlocked !== undefined) state.showBlocked = showBlocked;
  if (groupBy !== undefined) state.groupBy = groupBy;
  return state;
}

/**
 * Parse filter state from URL search parameters.
 */
function parseFromUrl(): FilterState {
  if (!isBrowser()) return DEFAULT_FILTER_STATE;

  const params = new URLSearchParams(window.location.search);

  return buildFilterState(
    parsePriority(params.get('priority')),
    parseType(params.get('type')),
    parseLabels(params.get('labels')),
    parseSearch(params.get('search')),
    parseShowBlocked(params.get('showBlocked')),
    parseGroupBy(params.get('groupBy'))
  );
}

/**
 * Serialize filter state to URL query string.
 */
function toQueryString(state: FilterState): string {
  const params = new URLSearchParams();

  if (state.priority !== undefined) {
    params.set('priority', state.priority.toString());
  }
  if (state.type !== undefined) {
    params.set('type', state.type);
  }
  if (state.labels !== undefined && state.labels.length > 0) {
    params.set('labels', state.labels.join(','));
  }
  if (state.search !== undefined && state.search !== '') {
    params.set('search', state.search);
  }
  if (state.showBlocked === true) {
    params.set('showBlocked', 'true');
  }
  if (
    state.groupBy !== undefined &&
    state.groupBy !== 'none' &&
    state.groupBy !== DEFAULT_GROUP_BY
  ) {
    params.set('groupBy', state.groupBy);
  }

  return params.toString();
}

/**
 * Update URL with filter state without triggering navigation.
 */
function updateUrl(state: FilterState): void {
  if (!isBrowser()) return;

  const queryString = toQueryString(state);
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  // Use replaceState to avoid polluting browser history
  window.history.replaceState(null, '', newUrl);
}

/**
 * Check if filter state is empty (all undefined).
 * Note: showBlocked is not considered for "empty" since it's a visibility toggle.
 * groupBy is considered - 'none' or undefined means no active grouping.
 */
function isEmptyFilter(state: FilterState): boolean {
  return (
    state.priority === undefined &&
    state.type === undefined &&
    (state.labels === undefined || state.labels.length === 0) &&
    (state.search === undefined || state.search === '') &&
    (state.groupBy === undefined || state.groupBy === 'none' || state.groupBy === DEFAULT_GROUP_BY)
  );
}

/**
 * Update a filter state with a new value for a specific key.
 * Deletes the key if value is undefined to satisfy exactOptionalPropertyTypes.
 */
function updateFilterState<K extends keyof FilterState>(
  prev: FilterState,
  key: K,
  value: FilterState[K] | undefined
): FilterState {
  const next = { ...prev };
  if (value === undefined) {
    delete next[key];
  } else {
    next[key] = value;
  }
  return next;
}

/**
 * React hook for managing filter state with URL synchronization.
 *
 * @example
 * ```tsx
 * function MyComponent() {
 *   const [filters, { setPriority, setType, clearAll }] = useFilterState()
 *
 *   return (
 *     <div>
 *       <select value={filters.priority ?? ''} onChange={e => setPriority(e.target.value ? Number(e.target.value) as Priority : undefined)}>
 *         <option value="">All priorities</option>
 *         <option value="0">P0</option>
 *         <option value="1">P1</option>
 *       </select>
 *       <button onClick={clearAll}>Clear filters</button>
 *     </div>
 *   )
 * }
 * ```
 */
export function useFilterState(options: UseFilterStateOptions = {}): UseFilterStateReturn {
  const { syncUrl = true } = options;

  // Initialize state from URL if in browser
  const [state, setState] = useState<FilterState>(() => {
    if (syncUrl) {
      return parseFromUrl();
    }
    return DEFAULT_FILTER_STATE;
  });

  // Sync URL when state changes
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      updateUrl(state);
    }
  }, [state, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = () => {
      setState(parseFromUrl());
    };

    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [syncUrl]);

  // Memoized actions
  const setPriority = useCallback((priority: Priority | undefined) => {
    setState((prev) => updateFilterState(prev, 'priority', priority));
  }, []);

  const setType = useCallback((type: IssueType | undefined) => {
    setState((prev) => updateFilterState(prev, 'type', type));
  }, []);

  const setLabels = useCallback((labels: string[] | undefined) => {
    setState((prev) => updateFilterState(prev, 'labels', labels));
  }, []);

  const setSearch = useCallback((search: string | undefined) => {
    setState((prev) => updateFilterState(prev, 'search', search));
  }, []);

  const setShowBlocked = useCallback((showBlocked: boolean | undefined) => {
    setState((prev) => updateFilterState(prev, 'showBlocked', showBlocked));
  }, []);

  const setGroupBy = useCallback((groupBy: GroupByOption | undefined) => {
    setState((prev) => updateFilterState(prev, 'groupBy', groupBy));
  }, []);

  const clearFilter = useCallback((key: keyof FilterState) => {
    setState((prev) => {
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const clearAll = useCallback(() => {
    setState(DEFAULT_FILTER_STATE);
  }, []);

  // Stable actions object
  const actions = useMemo<FilterActions>(
    () => ({
      setPriority,
      setType,
      setLabels,
      setSearch,
      setShowBlocked,
      setGroupBy,
      clearFilter,
      clearAll,
    }),
    [setPriority, setType, setLabels, setSearch, setShowBlocked, setGroupBy, clearFilter, clearAll]
  );

  return [state, actions];
}

// Export helpers for testing
export { toQueryString, parseFromUrl, isEmptyFilter };
