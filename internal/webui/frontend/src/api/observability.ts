/**
 * Observability API client.
 * Fetches observability metrics from the loom server.
 */

import type { MetricsSnapshot, ObservabilityMetricsResponse } from "@/types";
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
 * Fetch observability metrics from the loom server.
 * Throws on network errors or non-OK responses.
 */
export async function fetchObservabilityMetrics(): Promise<MetricsSnapshot> {
  const response = await fetchWithTimeout(
    `${LOOM_SERVER_URL}/api/observability/metrics`,
    {
      method: "GET",
      headers: buildLoomHeaders(),
    },
  );

  if (!response.ok) {
    throw new Error(
      `Observability metrics: ${response.status} ${response.statusText}`,
    );
  }

  const data: ObservabilityMetricsResponse = await response.json();
  if (!data.success || !data.data) {
    throw new Error(data.error ?? "Failed to fetch observability metrics");
  }

  return data.data;
}
