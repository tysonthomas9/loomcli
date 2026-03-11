/**
 * Loom Usage API client.
 * Fetches token usage and cost data from the loom server.
 * Follows the same pattern as agents.ts for consistency.
 */

import type { UsageResponse, UsageParams } from "@/types";
import { getAuthToken } from "./client";

const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? "/api/loom";
const LOOM_REQUEST_TIMEOUT_MS = 15000;

function buildLoomHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: "application/json",
  };
  const token = getAuthToken();
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  return headers;
}

async function fetchWithTimeout(
  input: RequestInfo,
  init: RequestInit,
  timeoutMs = LOOM_REQUEST_TIMEOUT_MS,
) {
  const controller = new AbortController();
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } finally {
    window.clearTimeout(timeoutId);
  }
}

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

  const response = await fetchWithTimeout(url, {
    method: "GET",
    headers: buildLoomHeaders(),
  });

  if (!response.ok) {
    throw new Error(`Loom usage: ${response.status} ${response.statusText}`);
  }

  return response.json();
}
