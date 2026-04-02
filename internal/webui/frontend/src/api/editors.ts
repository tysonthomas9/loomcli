/**
 * API client for editor endpoints.
 * Stateless — no module-level caches. Caching belongs in editorStore.
 * Interfaces with GET /api/editors and POST /api/editors/open endpoints.
 */

import { get, post } from "./client";
import type { EditorInfo } from "@/types/editor";

// ============= Types =============

interface EditorsListResponse {
  editors: EditorInfo[];
}

// ============= API Functions =============

/**
 * Fetch the list of available editors. Always hits the network.
 */
export async function fetchEditors(): Promise<EditorInfo[]> {
  const response = await get<EditorsListResponse>("/api/editors");
  return response.editors;
}

/**
 * Re-fetch editors from the backend. Alias for fetchEditors (no cache to invalidate).
 */
export async function refreshEditors(): Promise<EditorInfo[]> {
  return fetchEditors();
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
