/**
 * useFilterState - React hook for managing filter state with URL synchronization.
 * Uses React Router's useSearchParams instead of manual pushState/replaceState.
 */

import { useCallback, useMemo } from "react";
import { useLocation, useSearchParams } from "react-router-dom";

import type { Priority, IssueType } from "@/types";

/**
 * Group by option for swim lane grouping.
 * 'none' = flat view (no grouping).
 */
export type GroupByOption =
  | "none"
  | "epic"
  | "assignee"
  | "priority"
  | "type"
  | "label"
  | "repo";

/**
 * Valid group by options for URL validation.
 */
const VALID_GROUP_BY_OPTIONS: ReadonlySet<string> = new Set([
  "none",
  "epic",
  "assignee",
  "priority",
  "type",
  "label",
  "repo",
]);

/**
 * Filter state for UI filtering.
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
  setPriority: (priority: Priority | undefined) => void;
  setType: (type: IssueType | undefined) => void;
  setLabels: (labels: string[] | undefined) => void;
  setSearch: (search: string | undefined) => void;
  setShowBlocked: (showBlocked: boolean | undefined) => void;
  setGroupBy: (groupBy: GroupByOption | undefined) => void;
  clearFilter: (key: keyof FilterState) => void;
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
 */
export const DEFAULT_GROUP_BY: GroupByOption = "epic";

/**
 * Filter-specific URL param keys.
 */
const FILTER_PARAM_KEYS = [
  "priority",
  "type",
  "labels",
  "search",
  "showBlocked",
  "groupBy",
] as const;

/**
 * Parse priority from string value.
 */
function parsePriority(value: string | null): Priority | undefined {
  if (value === null) return undefined;
  const num = parseInt(value, 10);
  if (isNaN(num) || num < 0 || num > 4) return undefined;
  return num as Priority;
}

function parseLabels(value: string | null): string[] | undefined {
  if (value === null || value === "") return undefined;
  const labels = value.split(",").filter((l) => l.length > 0);
  return labels.length > 0 ? labels : undefined;
}

function parseType(value: string | null): IssueType | undefined {
  if (value === null || value === "") return undefined;
  return value as IssueType;
}

function parseSearch(value: string | null): string | undefined {
  if (value === null || value === "") return undefined;
  return value;
}

function parseShowBlocked(value: string | null): boolean | undefined {
  if (value === "true") return true;
  return undefined;
}

function parseGroupBy(value: string | null): GroupByOption | undefined {
  if (value === null || value === "") return undefined;
  if (VALID_GROUP_BY_OPTIONS.has(value)) return value as GroupByOption;
  return undefined;
}

/**
 * Build filter state from parsed values.
 */
function buildFilterState(
  priority: Priority | undefined,
  type: IssueType | undefined,
  labels: string[] | undefined,
  search: string | undefined,
  showBlocked: boolean | undefined,
  groupBy: GroupByOption | undefined,
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
 * Serialize filter state to URL query string.
 */
function toQueryString(state: FilterState): string {
  const params = new URLSearchParams();
  if (state.priority !== undefined)
    params.set("priority", state.priority.toString());
  if (state.type !== undefined) params.set("type", state.type);
  if (state.labels !== undefined && state.labels.length > 0)
    params.set("labels", state.labels.join(","));
  if (state.search !== undefined && state.search !== "")
    params.set("search", state.search);
  if (state.showBlocked === true) params.set("showBlocked", "true");
  if (
    state.groupBy !== undefined &&
    state.groupBy !== "none" &&
    state.groupBy !== DEFAULT_GROUP_BY
  )
    params.set("groupBy", state.groupBy);
  return params.toString();
}

/**
 * Parse filter state from URLSearchParams.
 */
function parseFromSearchParams(params: URLSearchParams): FilterState {
  return buildFilterState(
    parsePriority(params.get("priority")),
    parseType(params.get("type")),
    parseLabels(params.get("labels")),
    parseSearch(params.get("search")),
    parseShowBlocked(params.get("showBlocked")),
    parseGroupBy(params.get("groupBy")),
  );
}

/**
 * Check if filter state is empty (all undefined).
 */
function isEmptyFilter(state: FilterState): boolean {
  return (
    state.priority === undefined &&
    state.type === undefined &&
    (state.labels === undefined || state.labels.length === 0) &&
    (state.search === undefined || state.search === "") &&
    state.showBlocked === undefined &&
    (state.groupBy === undefined ||
      state.groupBy === "none" ||
      state.groupBy === DEFAULT_GROUP_BY)
  );
}

/**
 * Apply a filter update to URLSearchParams (mutates in place).
 */
function applyFilterToParams(
  params: URLSearchParams,
  key: string,
  value: string | undefined,
): void {
  if (value === undefined) {
    params.delete(key);
  } else {
    params.set(key, value);
  }
}

/**
 * React hook for managing filter state with URL synchronization.
 * Uses React Router's useSearchParams — no manual history or popstate needed.
 */
export function useFilterState(
  _options: UseFilterStateOptions = {},
): UseFilterStateReturn {
  const location = useLocation();
  const [, setSearchParams] = useSearchParams();

  // Derive filter state from current search params
  const state = useMemo(
    () => parseFromSearchParams(new URLSearchParams(location.search)),
    [location.search],
  );

  // Helper to update a single filter param with replace semantics
  const updateParam = useCallback(
    (key: string, value: string | undefined) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          applyFilterToParams(next, key, value);
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  const setPriority = useCallback(
    (priority: Priority | undefined) => {
      updateParam("priority", priority?.toString());
    },
    [updateParam],
  );

  const setType = useCallback(
    (type: IssueType | undefined) => {
      updateParam("type", type);
    },
    [updateParam],
  );

  const setLabels = useCallback(
    (labels: string[] | undefined) => {
      updateParam(
        "labels",
        labels && labels.length > 0 ? labels.join(",") : undefined,
      );
    },
    [updateParam],
  );

  const setSearch = useCallback(
    (search: string | undefined) => {
      updateParam("search", search || undefined);
    },
    [updateParam],
  );

  const setShowBlocked = useCallback(
    (showBlocked: boolean | undefined) => {
      updateParam("showBlocked", showBlocked ? "true" : undefined);
    },
    [updateParam],
  );

  const setGroupBy = useCallback(
    (groupBy: GroupByOption | undefined) => {
      const value =
        groupBy !== undefined &&
        groupBy !== "none" &&
        groupBy !== DEFAULT_GROUP_BY
          ? groupBy
          : undefined;
      updateParam("groupBy", value);
    },
    [updateParam],
  );

  const clearFilter = useCallback(
    (key: keyof FilterState) => {
      updateParam(key, undefined);
    },
    [updateParam],
  );

  const clearAll = useCallback(() => {
    setSearchParams(
      (prev) => {
        const next = new URLSearchParams(prev);
        for (const key of FILTER_PARAM_KEYS) {
          next.delete(key);
        }
        return next;
      },
      { replace: true },
    );
  }, [setSearchParams]);

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
    [
      setPriority,
      setType,
      setLabels,
      setSearch,
      setShowBlocked,
      setGroupBy,
      clearFilter,
      clearAll,
    ],
  );

  return [state, actions];
}

// Window-location parser used by tests.
function parseFromUrl(): FilterState {
  if (typeof window === "undefined" || !window.location) return {};
  return parseFromSearchParams(new URLSearchParams(window.location.search));
}

// Export helpers for testing
export { toQueryString, parseFromUrl, isEmptyFilter };
