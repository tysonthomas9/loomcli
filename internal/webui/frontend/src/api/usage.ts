/**
 * Loom Usage API client.
 * Fetches token usage and cost data from the loom server.
 */

import type { UsageResponse, UsageParams } from "@/types";
import { get } from "./client";

const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? "/api/loom";

/**
 * Fetch usage data from the loom server with optional filters.
 * Throws on network errors or non-OK responses.
 */
export async function fetchUsage(params?: UsageParams): Promise<UsageResponse> {
  let url = `${LOOM_SERVER_URL}/api/usage`;

  if (params) {
    const qs = new URLSearchParams();
    if (params.agent) qs.set("agent", params.agent);
    if (params.backend) qs.set("backend", params.backend);
    if (params.epic) qs.set("epic", params.epic);
    if (params.since) qs.set("since", params.since);
    if (params.until) qs.set("until", params.until);
    const str = qs.toString();
    if (str) url += `?${str}`;
  }

  return get<UsageResponse>(url, { timeout: 15000 });
}
