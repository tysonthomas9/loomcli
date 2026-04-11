/**
 * useRouteView — derives the active ViewMode from the current route path
 * and provides navigation helpers for view switching.
 *
 * Replaces useViewState's query-param approach (?view=kanban) with
 * route-segment URLs (/ws/:id/kanban). Eliminates the flushSync workaround
 * since route navigation via navigate() is synchronous in React Router.
 */

import { useCallback, useMemo } from "react";
import { useLocation, useNavigate, useParams } from "react-router-dom";

import { type ViewMode, DEFAULT_VIEW } from "@/components/ViewSwitcher";

/**
 * Valid route segments that map 1:1 to ViewMode values.
 * "issue-detail" is not a segment — it's derived from the issues/:issueId route.
 */
const VALID_VIEW_SEGMENTS: ReadonlySet<string> = new Set<ViewMode>([
  "kanban",
  "table",
  "graph",
  "monitor",
  "observability",
  "terminal",
  "workspace",
  "settings",
  "files",
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
 * Build a route path for the given view, preserving current search params.
 */
function buildViewPath(
  workspaceId: string,
  view: ViewMode,
  search: string,
): string {
  const segment = view === "issue-detail" ? "" : view;
  const base = `/ws/${workspaceId}/${segment}`;
  return search ? `${base}${search}` : base;
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
