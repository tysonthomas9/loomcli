/**
 * useIssueFilter - React hook for client-side issue filtering and search.
 * Provides memoized filtering with type-safe criteria.
 */

import { useMemo } from 'react';

import type { Issue, Status, Priority, IssueType } from '@/types';

/**
 * Options for the useIssueFilter hook.
 */
export interface UseIssueFilterOptions {
  /** Search term for text matching (title/description/notes) */
  searchTerm?: string;
  /** Filter by status */
  status?: Status;
  /** Filter by priority (exact match) */
  priority?: Priority;
  /** Filter by priority range (minimum) */
  priorityMin?: Priority;
  /** Filter by priority range (maximum) */
  priorityMax?: Priority;
  /** Filter by issue type */
  issueType?: IssueType;
  /** Filter by assignee (exact match) */
  assignee?: string;
  /** Only show unassigned issues */
  unassigned?: boolean;
  /** Filter by labels (all must match) */
  labels?: string[];
  /** Filter by labels (any must match) */
  labelsAny?: string[];
}

/**
 * Return type for the useIssueFilter hook.
 */
export interface UseIssueFilterReturn {
  /** Filtered array of issues */
  filteredIssues: Issue[];
  /** Count of filtered issues */
  count: number;
  /** Total count before filtering */
  totalCount: number;
  /** Whether any filters are active */
  hasActiveFilters: boolean;
  /** List of active filter names for UI display */
  activeFilters: string[];
}

/**
 * Check if an issue matches the search term.
 * Searches title, description, and notes fields (case-insensitive).
 * Defensively handles null/undefined values that may occur at runtime.
 */
function matchesSearchTerm(issue: Issue, term: string): boolean {
  const normalizedTerm = term.toLowerCase();

  // Check title (guard against null/undefined)
  if (typeof issue.title === 'string' && issue.title.toLowerCase().includes(normalizedTerm)) {
    return true;
  }

  // Check description (guard against null/undefined)
  if (
    typeof issue.description === 'string' &&
    issue.description.toLowerCase().includes(normalizedTerm)
  ) {
    return true;
  }

  // Check notes (guard against null/undefined)
  if (typeof issue.notes === 'string' && issue.notes.toLowerCase().includes(normalizedTerm)) {
    return true;
  }

  return false;
}

/**
 * Check if an issue matches all filter criteria.
 */
function matchesFilters(issue: Issue, options: UseIssueFilterOptions): boolean {
  // Status filter
  if (options.status !== undefined && issue.status !== options.status) {
    return false;
  }

  // Priority exact match
  if (options.priority !== undefined && issue.priority !== options.priority) {
    return false;
  }

  // Priority range (min)
  if (options.priorityMin !== undefined && issue.priority < options.priorityMin) {
    return false;
  }

  // Priority range (max)
  if (options.priorityMax !== undefined && issue.priority > options.priorityMax) {
    return false;
  }

  // Issue type filter
  if (options.issueType !== undefined && issue.issue_type !== options.issueType) {
    return false;
  }

  // Assignee filter (exact takes precedence over unassigned)
  if (options.assignee !== undefined) {
    if (issue.assignee !== options.assignee) {
      return false;
    }
  } else if (options.unassigned === true) {
    // Only check unassigned if assignee filter is not set
    if (issue.assignee !== undefined && issue.assignee !== '') {
      return false;
    }
  }

  // Labels filter (all must match)
  if (options.labels !== undefined && options.labels.length > 0) {
    const issueLabels = issue.labels ?? [];
    const allLabelsMatch = options.labels.every((label) => issueLabels.includes(label));
    if (!allLabelsMatch) {
      return false;
    }
  }

  // Labels filter (any must match)
  if (options.labelsAny !== undefined && options.labelsAny.length > 0) {
    const issueLabels = issue.labels ?? [];
    const anyLabelMatches = options.labelsAny.some((label) => issueLabels.includes(label));
    if (!anyLabelMatches) {
      return false;
    }
  }

  return true;
}

/**
 * Get list of active filter names for UI display.
 */
function getActiveFilters(options: UseIssueFilterOptions): string[] {
  const active: string[] = [];

  if (options.searchTerm && options.searchTerm.trim() !== '') {
    active.push('search');
  }
  if (options.status !== undefined) {
    active.push('status');
  }
  if (options.priority !== undefined) {
    active.push('priority');
  }
  if (options.priorityMin !== undefined || options.priorityMax !== undefined) {
    active.push('priorityRange');
  }
  if (options.issueType !== undefined) {
    active.push('type');
  }
  if (options.assignee !== undefined) {
    active.push('assignee');
  }
  if (options.unassigned === true) {
    active.push('unassigned');
  }
  if (options.labels !== undefined && options.labels.length > 0) {
    active.push('labels');
  }
  if (options.labelsAny !== undefined && options.labelsAny.length > 0) {
    active.push('labelsAny');
  }

  return active;
}

/**
 * React hook for filtering issues by various criteria.
 *
 * @param issues - Array of issues to filter
 * @param options - Filter criteria
 * @returns Filtered issues and filter state metadata
 *
 * @example
 * ```tsx
 * function IssueListView() {
 *   const [searchTerm, setSearchTerm] = useState('')
 *   const [statusFilter, setStatusFilter] = useState<Status | undefined>()
 *   const debouncedSearch = useDebounce(searchTerm, 300)
 *
 *   const { filteredIssues, count, hasActiveFilters } = useIssueFilter(issues, {
 *     searchTerm: debouncedSearch,
 *     status: statusFilter,
 *   })
 *
 *   return (
 *     <div>
 *       <SearchInput value={searchTerm} onChange={setSearchTerm} />
 *       <span>{count} issues found</span>
 *       {hasActiveFilters && <button onClick={clearFilters}>Clear</button>}
 *       <IssueTable issues={filteredIssues} />
 *     </div>
 *   )
 * }
 * ```
 */
export function useIssueFilter(
  issues: Issue[],
  options: UseIssueFilterOptions
): UseIssueFilterReturn {
  // Destructure searchTerm for normalization
  const { searchTerm } = options;

  // Calculate active filters first (used for memoization key and return value)
  const activeFilters = useMemo(() => getActiveFilters(options), [options]);

  const hasActiveFilters = activeFilters.length > 0;

  // Normalize search term
  const normalizedSearchTerm = useMemo(() => {
    return searchTerm?.trim() ?? '';
  }, [searchTerm]);

  // Memoized filtered issues
  const filteredIssues = useMemo(() => {
    // If no filters active, return original array
    if (!hasActiveFilters) {
      return issues;
    }

    return issues.filter((issue) => {
      // Check search term first (if provided)
      if (normalizedSearchTerm !== '') {
        if (!matchesSearchTerm(issue, normalizedSearchTerm)) {
          return false;
        }
      }

      // Check all other filters
      return matchesFilters(issue, options);
    });
  }, [issues, normalizedSearchTerm, hasActiveFilters, options]);

  return {
    filteredIssues,
    count: filteredIssues.length,
    totalCount: issues.length,
    hasActiveFilters,
    activeFilters,
  };
}
