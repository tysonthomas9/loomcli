/**
 * useViewState - React hook for managing view mode state with URL synchronization.
 * Provides centralized view state management for switching between Kanban, Table, and Graph views.
 */

import { useState, useCallback, useEffect } from "react";

import { type ViewMode, DEFAULT_VIEW } from "@/components/ViewSwitcher";

/**
 * Valid view modes for validation.
 */
const VALID_VIEWS: ViewMode[] = [
  "kanban",
  "table",
  "graph",
  "monitor",
  "observability",
  "terminal",
  "workspace",
  "settings",
  "files",
  "issue-detail",
];

/**
 * URL parameter name for view.
 */
const VIEW_PARAM = "view";

/**
 * URL parameter name for issue ID (used with issue-detail view).
 */
const ISSUE_PARAM = "issue";

/**
 * Options for useViewState hook.
 */
export interface UseViewStateOptions {
  /** Whether to sync with URL (default: true) */
  syncUrl?: boolean;
  /** Callback invoked on popstate events with the history state */
  onPopState?: (state: Record<string, unknown> | null) => void;
}

/**
 * Return type for useViewState hook.
 */
export interface UseViewStateReturn {
  /** Current view mode */
  view: ViewMode;
  /** Set view mode (replaceState — no history entry) */
  setView: (view: ViewMode) => void;
  /** Navigate to a view with pushState (creates history entry for back/forward) */
  navigateToView: (view: ViewMode, state?: Record<string, unknown>) => void;
  /** The issue ID from the URL (when in issue-detail view) */
  urlIssueId: string | null;
}

/**
 * Check if running in browser environment.
 */
function isBrowser(): boolean {
  return (
    typeof window !== "undefined" && typeof window.location !== "undefined"
  );
}

/**
 * Check if a value is a valid view mode.
 */
function isValidViewMode(value: string | null): value is ViewMode {
  return value !== null && VALID_VIEWS.includes(value as ViewMode);
}

/**
 * Parse view mode from URL search parameters.
 * Returns DEFAULT_VIEW for invalid or missing values.
 */
function parseViewFromUrl(): ViewMode {
  if (!isBrowser()) return DEFAULT_VIEW;

  const params = new URLSearchParams(window.location.search);
  const view = params.get(VIEW_PARAM);

  if (isValidViewMode(view)) {
    return view;
  }

  return DEFAULT_VIEW;
}

/**
 * Update URL with view mode without triggering navigation.
 * Uses replaceState by default to avoid polluting browser history.
 * Uses pushState when `push` is true (for meaningful navigation actions like opening/closing issue detail).
 * Removes the view param from URL when it matches DEFAULT_VIEW for cleaner URLs.
 * When view is 'issue-detail', also sets/clears the 'issue' param.
 */
function updateViewUrl(
  view: ViewMode,
  issueId?: string | null,
  push?: boolean,
  historyState?: Record<string, unknown> | null,
): void {
  if (!isBrowser()) return;

  const params = new URLSearchParams(window.location.search);

  if (view === DEFAULT_VIEW) {
    // Clean URL for default view
    params.delete(VIEW_PARAM);
  } else {
    params.set(VIEW_PARAM, view);
  }

  // Manage issue param: set when in issue-detail view, clear otherwise
  if (view === "issue-detail" && issueId) {
    params.set(ISSUE_PARAM, issueId);
  } else {
    params.delete(ISSUE_PARAM);
  }

  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;

  const method = push ? "pushState" : "replaceState";
  window.history[method](historyState ?? null, "", newUrl);
}

/**
 * Parse issue ID from URL search parameters.
 */
function parseIssueFromUrl(): string | null {
  if (!isBrowser()) return null;
  const params = new URLSearchParams(window.location.search);
  return params.get(ISSUE_PARAM);
}

/**
 * React hook for managing view mode state with URL synchronization.
 *
 * @example
 * ```tsx
 * function App() {
 *   const [activeView, setActiveView] = useViewState();
 *
 *   return (
 *     <ViewSwitcher
 *       activeView={activeView}
 *       onChange={setActiveView}
 *     />
 *   );
 * }
 * ```
 */
export function useViewState(
  options: UseViewStateOptions = {},
): UseViewStateReturn {
  const { syncUrl = true, onPopState } = options;

  // Initialize state from URL if syncing and in browser
  const [view, setViewState] = useState<ViewMode>(() => {
    if (syncUrl) {
      return parseViewFromUrl();
    }
    return DEFAULT_VIEW;
  });

  // Track issue ID for issue-detail view URL sync
  const [issueId, setIssueId] = useState<string | null>(() => {
    if (syncUrl) {
      return parseIssueFromUrl();
    }
    return null;
  });

  // Sync URL when state changes (replaceState only — skip if URL already matches
  // to avoid overwriting pushState entries from navigateToView)
  useEffect(() => {
    if (syncUrl && isBrowser()) {
      const currentView = parseViewFromUrl();
      const currentIssue = parseIssueFromUrl();
      if (currentView === view && currentIssue === issueId) return;
      updateViewUrl(view, issueId);
    }
  }, [view, issueId, syncUrl]);

  // Handle browser back/forward navigation
  useEffect(() => {
    if (!syncUrl || !isBrowser()) return;

    const handlePopState = (event: PopStateEvent) => {
      setViewState(parseViewFromUrl());
      setIssueId(parseIssueFromUrl());
      onPopState?.((event.state as Record<string, unknown> | null) ?? null);
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [syncUrl, onPopState]);

  // Memoized setter (replaceState) - clears issueId when switching away from issue-detail
  const setView = useCallback((newView: ViewMode) => {
    setViewState(newView);
    if (newView !== "issue-detail") {
      setIssueId(null);
    }
  }, []);

  // Navigate to a view with pushState (creates history entry for back/forward).
  // When view is 'issue-detail', reads state.issueId for the URL issue param.
  const navigateToView = useCallback(
    (newView: ViewMode, state?: Record<string, unknown>) => {
      const newIssueId =
        newView === "issue-detail" && state?.issueId
          ? String(state.issueId)
          : null;
      setViewState(newView);
      setIssueId(newIssueId);
      if (syncUrl) {
        updateViewUrl(newView, newIssueId, true, state ?? null);
      }
    },
    [syncUrl],
  );

  return { view, setView, navigateToView, urlIssueId: issueId };
}

// Export helpers for testing
export { parseViewFromUrl, isValidViewMode };
