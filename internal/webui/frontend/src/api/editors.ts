/**
 * API client for editor endpoints with module-level caching.
 * Interfaces with GET /api/editors and POST /api/editors/open endpoints.
 */

import { get, post } from "./client";
import type { EditorInfo } from "@/types/editor";

// ============= Types =============

interface EditorsListResponse {
  editors: EditorInfo[];
}

// ============= Module-level Cache =============

let editorCache: EditorInfo[] | null = null; // null = not yet fetched
let fetchPromise: Promise<EditorInfo[]> | null = null; // dedup concurrent calls

// ============= API Functions =============

/**
 * Fetch the list of available editors. Returns cached data if available.
 * Deduplicates concurrent in-flight requests.
 */
export async function fetchEditors(): Promise<EditorInfo[]> {
  if (editorCache !== null) {
    return editorCache;
  }
  if (fetchPromise !== null) {
    return fetchPromise;
  }
  fetchPromise = get<EditorsListResponse>("/api/editors").then(
    (response) => {
      editorCache = response.editors;
      fetchPromise = null;
      return editorCache;
    },
    (err) => {
      fetchPromise = null;
      throw err;
    },
  );
  return fetchPromise;
}

/**
 * Invalidate the cache and re-fetch editors from the backend.
 */
export async function refreshEditors(): Promise<EditorInfo[]> {
  editorCache = null;
  fetchPromise = null;
  return fetchEditors();
}

/**
 * Synchronous getter for current cache state.
 * Returns null if not yet fetched, or the cached editor list.
 */
export function getCachedEditors(): EditorInfo[] | null {
  return editorCache;
}

/**
 * Open a file/path in the specified editor.
 */
export async function openInEditor(
  editorId: string,
  path: string,
): Promise<void> {
  await post<{ success: boolean }>("/api/editors/open", {
    editor_id: editorId,
    path,
  });
}
