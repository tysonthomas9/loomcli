/**
 * API functions for workspace service health checking.
 * Uses openapi-fetch generated client.
 */

import { api, apiErrorFromResponse } from "./client";

/** Health endpoint response shape (matches Go HealthStatus struct). */
export interface HealthResponse {
  status: "ok" | "degraded" | "unhealthy" | "starting";
  error?: string;
}

/**
 * Check workspace service health via the API health endpoint.
 * Uses a short timeout (5s) since this is a connectivity probe.
 */
export async function checkWorkspaceHealth(): Promise<HealthResponse> {
  const { data, error, response } = await api.GET("/api/health", {
    signal: AbortSignal.timeout(5000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return data as unknown as HealthResponse;
}
