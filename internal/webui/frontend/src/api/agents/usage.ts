/**
 * Loom Usage API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type { UsageResponse, UsageParams } from "@/types";
import { api, apiErrorFromResponse, cleanQuery } from "@/api/common";

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
    return emptyUsage();
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

function emptyUsage(): UsageResponse {
  return {
    total_input_tokens: 0,
    total_output_tokens: 0,
    total_cache_read_tokens: 0,
    total_cache_write_tokens: 0,
    total_cost: 0,
    session_count: 0,
    by_agent: [],
    by_backend: [],
    daily_costs: [],
    sessions: [],
    timestamp: "",
  } as unknown as UsageResponse;
}
