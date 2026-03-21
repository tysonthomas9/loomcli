/**
 * buildShareUrl - Pure utility for constructing shareable deep-link URLs.
 * Builds a full URL from current window.location, merging provided params
 * with existing URL params. Omits view param when it matches DEFAULT_VIEW
 * for cleaner URLs. Omits issue param when null/undefined.
 */

import { DEFAULT_VIEW } from "@/components/ViewSwitcher";

/**
 * Parameters for building a share URL.
 */
export interface ShareUrlParams {
  /** View mode to include in the URL */
  view?: string;
  /** Issue ID to include in the URL (omitted when null/undefined) */
  issue?: string | null;
}

/**
 * Build a shareable URL by merging provided params into the current URL.
 * Preserves existing filter params (priority, search, labels, etc.).
 * Omits the view param when it matches DEFAULT_VIEW for clean URLs.
 * Omits the issue param when null/undefined.
 *
 * @param params - The view and issue params to include
 * @returns Full URL string suitable for sharing
 */
export function buildShareUrl(params: ShareUrlParams = {}): string {
  if (typeof window === "undefined" || typeof window.location === "undefined") {
    return "";
  }

  const url = new URL(window.location.href);

  // Set or remove view param
  if (params.view !== undefined) {
    if (params.view === DEFAULT_VIEW) {
      url.searchParams.delete("view");
    } else {
      url.searchParams.set("view", params.view);
    }
  }

  // Set or remove issue param
  if (params.issue !== undefined) {
    if (params.issue === null || params.issue === "") {
      url.searchParams.delete("issue");
    } else {
      url.searchParams.set("issue", params.issue);
    }
  }

  return url.toString();
}
