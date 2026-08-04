/**
 * API client for per-workspace configuration endpoints.
 * Uses openapi-fetch generated client.
 */

import { api, ApiError, apiErrorFromResponse } from "@/api/common";
import { cacheBackendConfig } from "@/api/common/config";
import type { BackendConfigData } from "@/api/common";
import type { WorkspaceData } from "./workspace";

/**
 * Get the store-backed backend config for a specific workspace.
 */
export async function getWorkspaceBackendConfig(
  workspaceId: string,
): Promise<BackendConfigData> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/config/backend",
    { params: { path: { ws: workspaceId } } },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const envelope = data as {
    success?: boolean;
    data?: BackendConfigData;
    error?: string;
  };
  if (!envelope.success || !envelope.data) {
    throw new ApiError(
      response?.status ?? 0,
      response?.statusText || envelope.error || "Unknown error",
      envelope.error,
    );
  }
  cacheBackendConfig(envelope.data);
  return envelope.data;
}

/**
 * Update a workspace's AI backend. Returns the updated workspace data.
 * Callers are responsible for triggering a refetch on the workspace context.
 */
export async function updateWorkspaceBackend(
  workspaceId: string,
  backend: string,
): Promise<WorkspaceData> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/config/backend",
    {
      params: { path: { ws: workspaceId } },
      body: { backend },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // The response is a MessageResponse, refetch workspace data
  if (data && typeof data === "object" && "success" in data) {
    const msg = data as { success: boolean; message?: string };
    if (!msg.success) {
      throw new ApiError(
        response?.status ?? 0,
        response?.statusText || msg.message || "Unknown error",
        msg.message,
      );
    }
  }
  // Refetch full workspace data after backend update
  const { fetchWorkspaceApi } = await import("./workspace");
  return fetchWorkspaceApi(workspaceId);
}

/** Update the design format used by planners and the issue design renderer. */
export async function updateWorkspaceDesignFormat(
  workspaceId: string,
  designFormat: "markdown" | "html",
): Promise<WorkspaceData> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/config/design-format",
    {
      params: { path: { ws: workspaceId } },
      body: { design_format: designFormat },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  if (data && typeof data === "object" && "success" in data) {
    const msg = data as { success: boolean; message?: string };
    if (!msg.success) {
      throw new ApiError(
        response?.status ?? 0,
        response?.statusText || msg.message || "Unknown error",
        msg.message,
      );
    }
  }
  const { fetchWorkspaceApi } = await import("./workspace");
  return fetchWorkspaceApi(workspaceId);
}
