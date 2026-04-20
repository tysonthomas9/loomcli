/**
 * buildShareUrl - Pure utility for constructing shareable deep-link URLs.
 * Builds a full URL using route-segment-based view paths
 * (e.g. /ws/abc/table?priority=1 instead of /ws/abc/?view=table&priority=1).
 * Preserves existing filter params from the current URL.
 *
 * Panel overlay mode: when a panel-supporting view is paired with an issue
 * (e.g. kanban + T-5), the issue is carried in the path as
 * /ws/abc/kanban/issues/T-5 rather than as a query param. Other views with
 * an issue fall back to the ?issue= query param for backwards compatibility.
 */

import { DEFAULT_VIEW } from "@/types/common";

/**
 * Views that support the issue panel overlay via URL (path-based issue).
 * Must stay aligned with PANEL_SUPPORTING_VIEWS in useRouteView.ts.
 */
const PANEL_VIEWS: ReadonlySet<string> = new Set([
  "kanban",
  "table",
  "graph",
  "monitor",
]);

/**
 * Parameters for building a share URL.
 */
export interface ShareUrlParams {
  /** View mode to include in the URL */
  view?: string;
  /** Issue ID — used with "issue-detail" view to build /issues/:id route */
  issue?: string | null;
}

/**
 * Extract the workspace path prefix from the current URL.
 * Returns e.g. "/ws/abc" from "/ws/abc/kanban" or "/ws/abc/issues/T-5".
 */
function getWorkspaceBase(pathname: string): string {
  const match = pathname.match(/^\/ws\/[^/]+/);
  return match ? match[0] : pathname;
}

/**
 * Build a shareable URL by constructing a route-segment-based path.
 * Preserves existing filter params (priority, search, labels, etc.).
 * The view becomes a path segment, not a query param.
 *
 * Examples:
 *   buildShareUrl({ view: "table" })                        → /ws/abc/table
 *   buildShareUrl({ view: "kanban" })                       → /ws/abc/kanban
 *   buildShareUrl({ view: "issue-detail", issue: "T-5" })   → /ws/abc/issues/T-5
 *   buildShareUrl({ view: "kanban", issue: "T-5" })         → /ws/abc/kanban/issues/T-5
 *   buildShareUrl({ view: "table", issue: null })           → /ws/abc/table
 *
 * @param params - The view and issue params to include
 * @returns Full URL string suitable for sharing
 */
export function buildShareUrl(params: ShareUrlParams = {}): string {
  if (typeof window === "undefined" || typeof window.location === "undefined") {
    return "";
  }

  const url = new URL(window.location.href);
  const wsBase = getWorkspaceBase(url.pathname);

  // Remove legacy view query param if present
  url.searchParams.delete("view");

  const hasIssue = typeof params.issue === "string" && params.issue.length > 0;
  const isPanelUrl =
    hasIssue && params.view !== undefined && PANEL_VIEWS.has(params.view);

  // Build the path based on view + issue combination
  if (params.view !== undefined) {
    if (params.view === "issue-detail") {
      // Full-page detail needs an issue id; without one, fall back to the
      // default view so we never emit an invalid /ws/:id/issue-detail path.
      url.pathname = hasIssue
        ? `${wsBase}/issues/${params.issue}`
        : `${wsBase}/${DEFAULT_VIEW}`;
    } else if (isPanelUrl) {
      url.pathname = `${wsBase}/${params.view}/issues/${params.issue}`;
    } else {
      const segment = params.view || DEFAULT_VIEW;
      url.pathname = `${wsBase}/${segment}`;
    }
  }

  // Issue identity belongs in the path for issue-detail and panel URLs;
  // other views fall back to the ?issue= query param.
  if (params.view === "issue-detail" || isPanelUrl) {
    url.searchParams.delete("issue");
  } else if (params.issue !== undefined) {
    if (params.issue === null || params.issue === "") {
      url.searchParams.delete("issue");
    } else {
      url.searchParams.set("issue", params.issue);
    }
  }

  return url.toString();
}
