/**
 * useWorkspaceParam - React hook for syncing the `workspace` URL parameter.
 * Follows useRepoFilter pattern: URL-synced state via replaceState + popstate.
 */

import { useState, useCallback, useEffect } from "react";

const WORKSPACE_PARAM = "workspace";

/**
 * Options for useWorkspaceParam hook.
 */
export interface UseWorkspaceParamOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
}

/**
 * Return type for useWorkspaceParam hook.
 */
export type UseWorkspaceParamReturn = [
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
 * Parse workspace from URL search parameters.
 * Returns null for missing or empty param (meaning "all workspaces").
 */
export function parseWorkspaceFromUrl(): string | null {
  if (!isBrowser()) return null;

  const params = new URLSearchParams(window.location.search);
  const raw = params.get(WORKSPACE_PARAM);

  if (!raw || raw.trim() === "") return null;
  return raw;
}

/**
 * Update URL with workspace param without triggering navigation.
 * Removes the param when workspace is null (all workspaces) for clean URLs.
 */
function updateWorkspaceUrl(workspace: string | null): void {
  if (!isBrowser()) return;

  const params = new URLSearchParams(window.location.search);

  if (workspace === null) {
    params.delete(WORKSPACE_PARAM);
  } else {
    params.set(WORKSPACE_PARAM, workspace);
  }

  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  window.history.replaceState(null, "", newUrl);
}

/**
 * React hook for syncing the workspace URL parameter.
 * null means "all workspaces" (no filtering).
 */
export function useWorkspaceParam(
  options: UseWorkspaceParamOptions = {},
): UseWorkspaceParamReturn {
  const { syncUrl = true } = options;

  const [workspace, setWorkspaceState] = useState<string | null>(() => {
    if (syncUrl) {
      return parseWorkspaceFromUrl();
    }
    return null;
  });

  // Sync URL when state changes
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      updateWorkspaceUrl(workspace);
    }
  }, [workspace, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = () => {
      setWorkspaceState(parseWorkspaceFromUrl());
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [syncUrl]);

  // Memoized setter
  const setWorkspace = useCallback((name: string | null) => {
    setWorkspaceState(name);
  }, []);

  return [workspace, setWorkspace];
}
