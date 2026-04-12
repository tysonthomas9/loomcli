/**
 * API client for editor endpoints.
 * Uses openapi-fetch generated client.
 */

import { api, apiErrorFromResponse } from "@/api/common";
import type { EditorInfo } from "@/types/common";

// ============= API Functions =============

/**
 * Fetch the list of available editors. Always hits the network.
 */
export async function fetchEditors(): Promise<EditorInfo[]> {
  const { data, error, response } = await api.GET("/api/editors");
  if (error) throw apiErrorFromResponse(error, response);
  return data.data?.editors ?? [];
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
  const { error, response } = await api.POST("/api/editors/open", {
    body: { editor_id: editorId, path },
  });
  if (error) throw apiErrorFromResponse(error, response);
}
