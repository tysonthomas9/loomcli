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

import { type ViewMode, DEFAULT_VIEW } from "@/types";

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

/**
 * Views that support the issue panel overlay via URL
 * (/ws/:id/{view}/issues/:issueId). When switching between these views
 * with a panel open, the panel's issue ID is preserved in the new URL.
 */
const PANEL_SUPPORTING_VIEWS: ReadonlySet<string> = new Set<ViewMode>([
  "kanban",
  "table",
  "graph",
  "monitor",
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
 * /ws/abc/kanban               → "kanban"
 * /ws/abc/kanban/issues/T-5    → "kanban" (panel overlay mode)
 * /ws/abc/terminal             → "terminal"
 * /ws/abc/issues/T-5           → "issue-detail" (full-page)
 * /ws/abc/                     → DEFAULT_VIEW
 * /ws/abc/nonexistent          → DEFAULT_VIEW
 *
 * View segment at position 3 takes priority over issueId — this lets
 * panel-overlay URLs keep the view active while the panel shows the issue.
 * Only falls through to "issue-detail" when position 3 is "issues" (no view prefix).
 */
function deriveView(pathname: string, issueId: string | undefined): ViewMode {
  // Path segments: ['', 'ws', workspaceId, viewSegment, ...]
  const segments = pathname.split("/");
  const viewSegment = segments[3];

  if (viewSegment && VALID_VIEW_SEGMENTS.has(viewSegment)) {
    return viewSegment as ViewMode;
  }

  // No valid view prefix — if issueId is present, URL is /ws/:id/issues/:issueId
  if (issueId) return "issue-detail";

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

  // Replace semantics — for programmatic redirects (single-repo guard, error
  // fallback). Drops the issueId so that a redirect away from a bad deep-link
  // doesn't carry the failing issue into the new view.
  const setView = useCallback(
    (newView: ViewMode) => {
      navigate(buildViewPath(workspaceId, newView, location.search), {
        replace: true,
      });
    },
    [navigate, workspaceId, location.search],
  );

  // Push semantics — for user-initiated view switches (NavRail, keyboard,
  // etc.). Preserves the panel issue when switching between panel-supporting
  // views so the panel stays open across view changes (e.g. kanban → table
  // with the same issue still visible).
  const navigateToView = useCallback(
    (newView: ViewMode) => {
      if (issueId && PANEL_SUPPORTING_VIEWS.has(newView)) {
        const searchSuffix = location.search || "";
        navigate(
          `/ws/${workspaceId}/${newView}/issues/${issueId}${searchSuffix}`,
        );
        return;
      }
      navigate(buildViewPath(workspaceId, newView, location.search));
    },
    [navigate, workspaceId, issueId, location.search],
  );

  return { view, setView, navigateToView };
}
