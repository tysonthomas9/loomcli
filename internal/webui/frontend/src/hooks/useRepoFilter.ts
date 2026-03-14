/**
 * useRepoFilter - React hook for managing repo selection with URL synchronization.
 * Follows useViewState pattern: URL-synced state via replaceState + popstate.
 */

import { useState, useCallback, useEffect } from "react";

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
 * Check if running in browser environment.
 */
function isBrowser(): boolean {
  return (
    typeof window !== "undefined" && typeof window.location !== "undefined"
  );
}

/**
 * Parse repos from URL search parameters.
 * Returns empty array for missing or empty param (meaning "all repos").
 */
export function parseReposFromUrl(): string[] {
  if (!isBrowser()) return [];

  const params = new URLSearchParams(window.location.search);
  const raw = params.get(REPOS_PARAM);

  if (!raw) return [];

  return raw
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

/**
 * Update URL with repos param without triggering navigation.
 * Removes the param when repos is empty (all repos) for clean URLs.
 */
function updateReposUrl(repos: string[]): void {
  if (!isBrowser()) return;

  const params = new URLSearchParams(window.location.search);

  if (repos.length === 0) {
    params.delete(REPOS_PARAM);
  } else {
    params.set(REPOS_PARAM, repos.join(","));
  }

  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  window.history.replaceState(null, "", newUrl);
}

/**
 * React hook for managing repo selection with URL synchronization.
 * Empty array means "all repos" (no filtering).
 */
export function useRepoFilter(
  options: UseRepoFilterOptions = {},
): UseRepoFilterReturn {
  const { syncUrl = true } = options;

  const [repos, setReposState] = useState<string[]>(() => {
    if (syncUrl) {
      return parseReposFromUrl();
    }
    return [];
  });

  // Sync URL when state changes
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      updateReposUrl(repos);
    }
  }, [repos, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = () => {
      setReposState(parseReposFromUrl());
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [syncUrl]);

  // Memoized setter
  const setRepos = useCallback((newRepos: string[]) => {
    setReposState(newRepos);
  }, []);

  return [repos, setRepos];
}
