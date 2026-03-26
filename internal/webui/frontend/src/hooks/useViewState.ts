/**
 * useViewState - React hook for managing view mode state with URL synchronization.
 * Uses React Router's useSearchParams instead of manual pushState/replaceState.
 */

import { useCallback, useMemo } from "react";
import { useSearchParams } from "react-router-dom";

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
 * Options for useViewState hook.
 */
export interface UseViewStateOptions {
  /** Callback invoked when the user navigates back/forward (React Router handles this) */
  onPopState?: (state: Record<string, unknown> | null) => void;
}

/**
 * Return type for useViewState hook.
 */
export interface UseViewStateReturn {
  /** Current view mode */
  view: ViewMode;
  /** Set view mode (replace semantics — no history entry) */
  setView: (view: ViewMode) => void;
  /** Navigate to a view with push semantics (creates history entry for back/forward) */
  navigateToView: (view: ViewMode, state?: Record<string, unknown>) => void;
}

/**
 * Check if a value is a valid view mode.
 */
function isValidViewMode(value: string | null): value is ViewMode {
  return value !== null && VALID_VIEWS.includes(value as ViewMode);
}

/**
 * React hook for managing view mode state with URL synchronization.
 * Uses React Router's useSearchParams for URL sync — no manual
 * pushState/replaceState or popstate listeners needed.
 */
export function useViewState(
  _options: UseViewStateOptions = {},
): UseViewStateReturn {
  const [searchParams, setSearchParams] = useSearchParams();

  // Read current view from search params
  const view = useMemo((): ViewMode => {
    const raw = searchParams.get(VIEW_PARAM);
    return isValidViewMode(raw) ? raw : DEFAULT_VIEW;
  }, [searchParams]);

  // Set view with replace semantics (no history entry)
  const setView = useCallback(
    (newView: ViewMode) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (newView === DEFAULT_VIEW) {
            next.delete(VIEW_PARAM);
          } else {
            next.set(VIEW_PARAM, newView);
          }
          return next;
        },
        { replace: true },
      );
    },
    [setSearchParams],
  );

  // Navigate to a view with push semantics (creates history entry)
  const navigateToView = useCallback(
    (newView: ViewMode, _state?: Record<string, unknown>) => {
      setSearchParams(
        (prev) => {
          const next = new URLSearchParams(prev);
          if (newView === DEFAULT_VIEW) {
            next.delete(VIEW_PARAM);
          } else {
            next.set(VIEW_PARAM, newView);
          }
          return next;
        },
        { replace: false },
      );
    },
    [setSearchParams],
  );

  return { view, setView, navigateToView };
}

// Export helpers for testing
export { isValidViewMode };
