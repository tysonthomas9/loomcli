/**
 * useRepoFilterParam - React hook for syncing the `repoFilter` URL parameter.
 * Uses React Router's useSearchParams instead of manual pushState/replaceState.
 */

import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

const REPO_FILTER_PARAM = "repoFilter";

/**
 * Options for useRepoFilterParam hook.
 */
export interface UseRepoFilterParamOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
}

/**
 * Return type for useRepoFilterParam hook.
 */
export type UseRepoFilterParamReturn = [
  string | null,
  (name: string | null) => void,
];

/**
 * Parse repo filter from URL search parameters.
 */
export function parseRepoFilterFromUrl(): string | null {
  if (typeof window === "undefined" || !window.location) return null;
  const params = new URLSearchParams(window.location.search);
  const raw = params.get(REPO_FILTER_PARAM);
  if (!raw || raw.trim() === "") return null;
  return raw;
}

/**
 * React hook for syncing the repo filter URL parameter.
 * null means "all repos" (no filtering).
 */
export function useRepoFilterParam(
  _options: UseRepoFilterParamOptions = {},
): UseRepoFilterParamReturn {
  const [searchParams, setSearchParams] = useSearchParams();

  const repoFilter = useMemo(() => {
    const raw = searchParams.get(REPO_FILTER_PARAM);
    if (!raw || raw.trim() === "") return null;
    return raw;
  }, [searchParams]);

  const setRepoFilter = useCallback(
    (name: string | null) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (name === null) {
            next.delete(REPO_FILTER_PARAM);
          } else {
            next.set(REPO_FILTER_PARAM, name);
          }
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  return [repoFilter, setRepoFilter];
}
