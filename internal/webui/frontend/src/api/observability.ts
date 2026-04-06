/**
 * Observability API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type { MetricsSnapshot } from "@/types";
import { api, apiErrorFromResponse } from "./client";

/**
 * Fetch observability metrics from the loom server.
 * Throws on network errors or non-OK responses.
 */
export async function fetchObservabilityMetrics(): Promise<MetricsSnapshot> {
  const { data, error, response } = await api.GET(
    "/api/observability/metrics",
    {
      signal: AbortSignal.timeout(15000),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const envelope = data as {
    success: boolean;
    data?: MetricsSnapshot;
    error?: string;
  };
  if (!envelope.success || !envelope.data) {
    throw new Error(envelope.error ?? "Failed to fetch observability metrics");
  }
  return envelope.data;
}
