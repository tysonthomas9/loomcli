/**
 * useFilteredSelection - React hook that composes useIssueFilter and useSelection
 * with automatic pruning of selection when filters change.
 *
 * When filters hide issues that are currently selected, those issues are
 * automatically removed from the selection to prevent confusing UX where
 * bulk actions could affect issues the user can no longer see.
 */

import { useEffect, useRef } from 'react';

import {
  useIssueFilter,
  useSelection,
  type UseIssueFilterOptions,
  type UseSelectionReturn,
} from '@/hooks';
import type { Issue } from '@/types';

/**
 * Options for the useFilteredSelection hook.
 */
export interface UseFilteredSelectionOptions {
  /** Issues to filter and select from */
  issues: Issue[];
  /** Filter options for useIssueFilter */
  filterOptions: UseIssueFilterOptions;
  /** Initial selection (passed to useSelection) */
  initialSelection?: Set<string> | string[];
  /** Callback when selection changes (passed to useSelection) */
  onSelectionChange?: (selectedIds: Set<string>) => void;
  /** Whether to auto-prune selection when filter changes (default: true) */
  autoPrune?: boolean;
}

/**
 * Return type for the useFilteredSelection hook.
 */
export interface UseFilteredSelectionReturn {
  /** Filtered issues */
  filteredIssues: Issue[];
  /** Filter metadata from useIssueFilter */
  filterMeta: {
    count: number;
    totalCount: number;
    hasActiveFilters: boolean;
    activeFilters: string[];
  };
  /** Selection state and actions from useSelection */
  selection: UseSelectionReturn;
}

/**
 * React hook that combines filtering and selection with auto-pruning.
 *
 * When filters change and hide previously selected issues, those issues
 * are automatically removed from the selection. This prevents situations
 * where bulk actions would affect issues that are no longer visible to
 * the user.
 *
 * @param options - Configuration options
 * @returns Filtered issues, filter metadata, and selection state/actions
 *
 * @example
 * ```tsx
 * function IssueListPage() {
 *   const [filterOptions, setFilterOptions] = useState<UseIssueFilterOptions>({});
 *
 *   const { filteredIssues, filterMeta, selection } = useFilteredSelection({
 *     issues: allIssues,
 *     filterOptions,
 *   });
 *
 *   return (
 *     <>
 *       <FilterBar onFilterChange={setFilterOptions} />
 *       <IssueTable
 *         issues={filteredIssues}
 *         showCheckbox
 *         selectedIds={selection.selectedIds}
 *         onSelectionChange={selection.toggleSelection}
 *       />
 *       {selection.selectedIds.size > 0 && (
 *         <BulkActionToolbar
 *           selectedIds={selection.selectedIds}
 *           onClearSelection={selection.clearSelection}
 *         />
 *       )}
 *     </>
 *   );
 * }
 * ```
 */
export function useFilteredSelection(
  options: UseFilteredSelectionOptions
): UseFilteredSelectionReturn {
  const { issues, filterOptions, initialSelection, onSelectionChange, autoPrune = true } = options;

  // Apply filtering
  const { filteredIssues, count, totalCount, hasActiveFilters, activeFilters } = useIssueFilter(
    issues,
    filterOptions
  );

  // Manage selection with filtered issues as visible items
  // Only pass optional props when defined (required for exactOptionalPropertyTypes)
  const selection = useSelection({
    visibleItems: filteredIssues,
    ...(initialSelection !== undefined && { initialSelection }),
    ...(onSelectionChange !== undefined && { onSelectionChange }),
  });

  // Store pruneSelection in a ref to ensure stable dependency for useEffect
  // (Follows the pattern used in useSelection.ts for onSelectionChange)
  const pruneSelectionRef = useRef(selection.pruneSelection);

  useEffect(() => {
    pruneSelectionRef.current = selection.pruneSelection;
  }, [selection.pruneSelection]);

  // Auto-prune selection when filtered issues change
  useEffect(() => {
    if (autoPrune) {
      pruneSelectionRef.current(filteredIssues);
    }
  }, [filteredIssues, autoPrune]);

  return {
    filteredIssues,
    filterMeta: {
      count,
      totalCount,
      hasActiveFilters,
      activeFilters,
    },
    selection,
  };
}
