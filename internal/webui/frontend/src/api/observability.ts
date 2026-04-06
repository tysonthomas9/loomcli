/**
 * Observability API client.
 * Fetches observability metrics from the loom server.
 */

import type { MetricsSnapshot, ObservabilityMetricsResponse } from "@/types";
import { get } from "./client";

const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? "";

/**
 * Fetch observability metrics from the loom server.
 * Throws on network errors or non-OK responses.
 */
export async function fetchObservabilityMetrics(): Promise<MetricsSnapshot> {
  const data = await get<ObservabilityMetricsResponse>(
    `${LOOM_SERVER_URL}/api/observability/metrics`,
    { timeout: 15000 },
  );
  if (!data.success || !data.data) {
    throw new Error(data.error ?? "Failed to fetch observability metrics");
  }
  return data.data;
}
