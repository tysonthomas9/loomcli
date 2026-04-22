/**
 * Loom Usage API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type { UsageResponse, UsageParams } from "@/types";
import { api, apiErrorFromResponse, cleanQuery } from "@/api/common";
import { DEFAULT_USAGE } from "./defaults";

/**
 * Fetch usage data for a specific workspace with optional filters. Empty or
 * unknown wsID short-circuits to an empty response — matches the fetchStatus
 * / fetchTasks convention so sidebars don't inherit the launch workspace's
 * usage when activeWorkspaceID hasn't resolved yet.
 */
export async function fetchUsage(
  wsID: string,
  params?: UsageParams,
): Promise<UsageResponse> {
  if (wsID === "") {
    return DEFAULT_USAGE;
  }
  const query = cleanQuery<{
    agent?: string;
    backend?: string;
    epic?: string;
    since?: string;
    until?: string;
  }>({
    agent: params?.agent,
    backend: params?.backend,
    epic: params?.epic,
    since: params?.since,
    until: params?.until,
  });
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/usage",
    {
      params: { path: { ws: wsID }, query },
      signal: AbortSignal.timeout(15000),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data as unknown as UsageResponse;
}
