/**
 * Loom Usage API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type { UsageResponse, UsageParams } from "@/types";
import { api, apiErrorFromResponse, cleanQuery } from "./client";

/**
 * Fetch usage data from the loom server with optional filters.
 * Throws on network errors or non-OK responses.
 */
export async function fetchUsage(params?: UsageParams): Promise<UsageResponse> {
  const { data, error, response } = await api.GET("/api/monitor/usage", {
    params: {
      query: cleanQuery({
        agent: params?.agent,
        backend: params?.backend,
        epic: params?.epic,
        since: params?.since,
        until: params?.until,
      }),
    },
    signal: AbortSignal.timeout(15000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return data as unknown as UsageResponse;
}
