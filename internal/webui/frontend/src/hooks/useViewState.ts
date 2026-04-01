/**
 * useViewState — thin wrapper around useRouteView for backwards compatibility.
 *
 * Previously managed view state via ?view= query params. Now delegates to
 * useRouteView which derives the active view from route segments
 * (e.g. /ws/:id/kanban instead of /ws/:id/?view=kanban).
 */

import type { ViewMode } from "@/components/ViewSwitcher";

import { useRouteView } from "./useRouteView";

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
 * Options for useViewState hook (kept for interface compatibility).
 */
export interface UseViewStateOptions {
  /** Callback invoked when the user navigates back/forward (handled by React Router) */
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
 * React hook for managing view mode state — delegates to useRouteView.
 */
export function useViewState(
  _options: UseViewStateOptions = {},
): UseViewStateReturn {
  const { view, setView, navigateToView } = useRouteView();

  return { view, setView, navigateToView };
}

// Export helpers for testing
export { isValidViewMode };
