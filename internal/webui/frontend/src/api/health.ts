/**
 * API functions for daemon health checking.
 * Interfaces with GET /api/health endpoint.
 */

import { get } from "./client";

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
  return get<HealthResponse>("/api/health", { timeout: 5000 });
}
