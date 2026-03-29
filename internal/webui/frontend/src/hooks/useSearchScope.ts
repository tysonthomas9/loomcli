/**
 * useSearchScope - Derives the active search scope name from workspace context.
 * Returns the scope name for display in the SearchScopeIndicator and
 * a clear handler to reset the scope.
 */

import { useMemo, useCallback } from "react";

import { useWorkspaceContext } from "./useWorkspaceContext";
import { useRepoFilterParam } from "./useRepoFilterParam";

export interface UseSearchScopeReturn {
  /** Display name for the active scope. Undefined when showing all repos. */
  scopeName: string | undefined;
  /** Clears the repo scope to show all repos. */
  clearScope: () => void;
}

/**
 * Derive a display name for the current repo selection.
 * Single repo → repo name; matches a group → group name; else → "N repos".
 */
export function useSearchScope(): UseSearchScopeReturn {
  const {
    groups,
    getReposByGroup,
    selectedRepoNames,
    isAllSelected,
    selectAll,
  } = useWorkspaceContext();

  const [, setRepoFilterParam] = useRepoFilterParam();

  const scopeName = useMemo(() => {
    if (isAllSelected || selectedRepoNames.size === 0) return undefined;

    if (selectedRepoNames.size === 1) {
      return [...selectedRepoNames][0];
    }

    for (const group of groups) {
      const groupRepos = getReposByGroup(group);
      if (groupRepos.length === selectedRepoNames.size) {
        const allMatch = groupRepos.every((r) => selectedRepoNames.has(r.name));
        if (allMatch) return group;
      }
    }

    return `${selectedRepoNames.size} repos`;
  }, [isAllSelected, selectedRepoNames, groups, getReposByGroup]);

  const clearScope = useCallback(() => {
    selectAll();
    setRepoFilterParam(null);
  }, [selectAll, setRepoFilterParam]);

  return { scopeName, clearScope };
}
