/**
 * useRouteView — derives the active ViewMode from the current route path
 * and provides navigation helpers for view switching.
 *
 * Uses route-segment URLs (/ws/:id/kanban) as the view state source.
 */

import { useCallback, useMemo } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";

import { type ViewMode, DEFAULT_VIEW } from "@/types";

/**
 * Valid route segments that map 1:1 to ViewMode values.
 * "issue-detail" is not a segment — it's derived from the issues/:issueId route.
 */
const VALID_VIEW_SEGMENTS: ReadonlySet<string> = new Set<ViewMode>([
  "kanban",
  "list",
  "table",
  "graph",
  "monitor",
  "observability",
  "terminal",
  "agents",
  "prs",
  "workspace",
  "settings",
  "files",
  "agents",
]);

/**
 * Search params that describe a *detail sub-state within a view* rather than
 * workspace-wide state. Switching views via the nav rail resets the target
 * section to its root, so these are dropped; anything else (repoFilter, repos,
 * board filters) is preserved.
 *
 * Without this, /ws/:id/prs?review=X → "prs" rebuilt the identical URL and
 * React Router no-op'd, stranding the user on the review detail view.
 */
const VIEW_SCOPED_SEARCH_PARAMS: ReadonlySet<string> = new Set([
  "review", // PRsPage: issue-backed review detail
  "review-pr", // PRsPage: PR-backed review detail
  "discuss", // PRsPage: discussion panel within a review
]);

export interface UseRouteViewReturn {
  /** Current view mode derived from the route path */
  view: ViewMode;
  /** Set view with replace semantics (no history entry) — use for redirects */
  setView: (view: ViewMode) => void;
  /** Navigate to a view with push semantics (creates history entry for back/forward) */
  navigateToView: (view: ViewMode) => void;
}

/**
 * Extract the view segment from the URL path.
 *
 * /ws/abc/kanban       → "kanban"
 * /ws/abc/terminal     → "terminal"
 * /ws/abc/issues/T-5   → "issue-detail"
 * /ws/abc/             → DEFAULT_VIEW
 * /ws/abc/nonexistent  → DEFAULT_VIEW
 */
function deriveView(pathname: string, issueId: string | undefined): ViewMode {
  if (issueId) return "issue-detail";

  // Path segments: ['', 'ws', workspaceId, viewSegment, ...]
  const segments = pathname.split("/");
  const viewSegment = segments[3];

  if (viewSegment && VALID_VIEW_SEGMENTS.has(viewSegment)) {
    return viewSegment as ViewMode;
  }

  return DEFAULT_VIEW;
}

/**
 * Drop view-scoped detail params from a search string, keeping everything else.
 * Never emits a bare trailing "?" — an unchanged URL is what caused the bug.
 */
function stripViewScopedParams(search: string): string {
  if (!search) return "";

  const params = new URLSearchParams(search);
  for (const key of VIEW_SCOPED_SEARCH_PARAMS) {
    params.delete(key);
  }

  const next = params.toString();
  return next ? `?${next}` : "";
}

/**
 * Build a route path for the given view, preserving workspace-scoped search
 * params and discarding the ones scoped to a view's detail sub-state.
 */
function buildViewPath(
  workspaceId: string,
  view: ViewMode,
  search: string,
): string {
  const segment = view === "issue-detail" ? "" : view;
  return `/ws/${workspaceId}/${segment}${stripViewScopedParams(search)}`;
}

export function useRouteView(): UseRouteViewReturn {
  const location = useLocation();
  const navigate = useNavigate();
  const { workspaceId = "", issueId } = useParams<{
    workspaceId: string;
    issueId: string;
  }>();

  const view = useMemo(
    () => deriveView(location.pathname, issueId),
    [location.pathname, issueId],
  );

  // Replace semantics — for programmatic redirects (single-repo guard, error fallback)
  const setView = useCallback(
    (newView: ViewMode) => {
      navigate(buildViewPath(workspaceId, newView, location.search), {
        replace: true,
      });
    },
    [navigate, workspaceId, location.search],
  );

  // Push semantics — for user-initiated view switches (NavRail, keyboard, etc.)
  const navigateToView = useCallback(
    (newView: ViewMode) => {
      navigate(buildViewPath(workspaceId, newView, location.search));
    },
    [navigate, workspaceId, location.search],
  );

  return { view, setView, navigateToView };
}
