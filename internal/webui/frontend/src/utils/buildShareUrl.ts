/**
 * buildShareUrl - Pure utility for constructing shareable deep-link URLs.
 * Builds a full URL using route-segment-based view paths
 * (e.g. /ws/abc/table?priority=1 instead of /ws/abc/?view=table&priority=1).
 * Preserves existing filter params from the current URL.
 */

import { DEFAULT_VIEW } from "@/types/common";

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
 *   buildShareUrl({ view: "table" })           → /ws/abc/table
 *   buildShareUrl({ view: "kanban" })           → /ws/abc/kanban
 *   buildShareUrl({ view: "issue-detail", issue: "T-5" }) → /ws/abc/issues/T-5
 *   buildShareUrl({ view: "table", issue: null })  → /ws/abc/table
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

  // Build the path based on view
  if (params.view !== undefined) {
    if (params.view === "issue-detail" && params.issue) {
      url.pathname = `${wsBase}/issues/${params.issue}`;
    } else {
      const segment = params.view || DEFAULT_VIEW;
      url.pathname = `${wsBase}/${segment}`;
    }
  }

  // Set or remove issue query param (for non-issue-detail views)
  if (params.view !== "issue-detail") {
    if (params.issue !== undefined) {
      if (params.issue === null || params.issue === "") {
        url.searchParams.delete("issue");
      } else {
        url.searchParams.set("issue", params.issue);
      }
    }
  } else {
    // issue-detail uses route segment, not query param
    url.searchParams.delete("issue");
  }

  return url.toString();
}
