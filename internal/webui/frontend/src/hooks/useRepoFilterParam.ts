/**
 * useRepoFilterParam - React hook for syncing the `repoFilter` URL parameter.
 * Follows useRepoFilter pattern: URL-synced state via replaceState + popstate.
 */

import { useState, useCallback, useEffect } from "react";

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
 * Check if running in browser environment.
 */
function isBrowser(): boolean {
  return (
    typeof window !== "undefined" && typeof window.location !== "undefined"
  );
}

/**
 * Parse repo filter from URL search parameters.
 * Returns null for missing or empty param (meaning "all repos").
 */
export function parseRepoFilterFromUrl(): string | null {
  if (!isBrowser()) return null;

  const params = new URLSearchParams(window.location.search);
  const raw = params.get(REPO_FILTER_PARAM);

  if (!raw || raw.trim() === "") return null;
  return raw;
}

/**
 * Update URL with repo filter param without triggering navigation.
 * Removes the param when repoFilter is null (all repos) for clean URLs.
 */
function updateRepoFilterUrl(repoFilter: string | null): void {
  if (!isBrowser()) return;

  const params = new URLSearchParams(window.location.search);

  if (repoFilter === null) {
    params.delete(REPO_FILTER_PARAM);
  } else {
    params.set(REPO_FILTER_PARAM, repoFilter);
  }

  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  window.history.replaceState(null, "", newUrl);
}

/**
 * React hook for syncing the repo filter URL parameter.
 * null means "all repos" (no filtering).
 */
export function useRepoFilterParam(
  options: UseRepoFilterParamOptions = {},
): UseRepoFilterParamReturn {
  const { syncUrl = true } = options;

  const [repoFilter, setRepoFilterState] = useState<string | null>(() => {
    if (syncUrl) {
      return parseRepoFilterFromUrl();
    }
    return null;
  });

  // Sync URL when state changes
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      updateRepoFilterUrl(repoFilter);
    }
  }, [repoFilter, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = () => {
      setRepoFilterState(parseRepoFilterFromUrl());
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [syncUrl]);

  // Memoized setter
  const setRepoFilter = useCallback((name: string | null) => {
    setRepoFilterState(name);
  }, []);

  return [repoFilter, setRepoFilter];
}
