/**
 * Workspace-scoped agent status API client.
 *
 * Calls GET /api/workspaces/{ws}/agents/status, the merged daemon+git agent
 * endpoint introduced by loomcli-lg0de.8. Wrapped envelope is unwrapped here
 * so callers receive the inner WorkspaceAgentStatusResponse directly.
 */

import { api, apiErrorFromResponse, ApiError } from "@/api/common";
import type { WorkspaceAgentStatusResponse } from "@/types";

/**
 * Fetch merged daemon supervision + git enrichment for every agent in a
 * workspace. Empty wsID returns an empty response without firing the request,
 * mirroring the early-return guard in fetchAgents/fetchStatus.
 *
 * Throws ApiError on transport failure, non-OK responses (4xx/5xx including
 * 503 daemon_unavailable), or a falsey envelope.success flag.
 */
export async function fetchWorkspaceAgentStatus(
  wsID: string,
  options?: { signal?: AbortSignal },
): Promise<WorkspaceAgentStatusResponse> {
  if (wsID === "") {
    return {
      agents: [],
      ipc_socket_active: false,
      daemon_pid: 0,
      daemon_started_at: "",
      workspace_name: "",
      timestamp: "",
    };
  }
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/agents/status",
    {
      params: { path: { ws: wsID } },
      ...(options?.signal ? { signal: options.signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  if (!data || !data.success) {
    throw new ApiError(
      response?.status ?? 0,
      response?.statusText ?? "agent status request failed",
      data,
    );
  }
  return data.data;
}
