/**
 * useRepoFilter - React hook for managing repo selection with URL synchronization.
 * Uses React Router's useSearchParams instead of manual pushState/replaceState.
 */

import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

const REPOS_PARAM = "repos";

/**
 * Options for useRepoFilter hook.
 */
export interface UseRepoFilterOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
}

/**
 * Return type for useRepoFilter hook.
 */
export type UseRepoFilterReturn = [string[], (repos: string[]) => void];

/**
 * Parse repos from URLSearchParams.
 */
function parseReposFromSearchParams(params: URLSearchParams): string[] {
  const raw = params.get(REPOS_PARAM);
  if (!raw) return [];
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/**
 * Parse repos from URL search parameters.
 */
export function parseReposFromUrl(): string[] {
  if (typeof window === "undefined" || !window.location) return [];
  return parseReposFromSearchParams(
    new URLSearchParams(window.location.search),
  );
}

/**
 * React hook for managing repo selection with URL synchronization.
 * Empty array means "all repos" (no filtering).
 */
export function useRepoFilter(
  _options: UseRepoFilterOptions = {},
): UseRepoFilterReturn {
  const [searchParams, setSearchParams] = useSearchParams();

  const repos = useMemo(
    () => parseReposFromSearchParams(searchParams),
    [searchParams],
  );

  const setRepos = useCallback(
    (newRepos: string[]) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (newRepos.length === 0) {
            next.delete(REPOS_PARAM);
          } else {
            next.set(REPOS_PARAM, newRepos.join(","));
          }
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  return [repos, setRepos];
}
