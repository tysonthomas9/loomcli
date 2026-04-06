/**
 * API functions for daemon health checking.
 * Uses openapi-fetch generated client.
 */

import { api, apiErrorFromResponse } from "./client";

/** Health endpoint response shape (matches Go HealthStatus struct). */
export interface HealthResponse {
  status: "ok" | "degraded" | "unhealthy";
  daemon: {
    connected: boolean;
    status?: string;
    uptime?: number;
    version?: string;
    error?: string;
  };
}

/**
 * Check daemon health via the API health endpoint.
 * Uses a short timeout (5s) since this is a connectivity probe.
 */
export async function checkDaemonHealth(): Promise<HealthResponse> {
  const { data, error, response } = await api.GET("/api/health", {
    signal: AbortSignal.timeout(5000),
  });
  if (error) throw apiErrorFromResponse(error, response);
  return data as unknown as HealthResponse;
}
