/**
 * API functions for backend configuration.
 * Uses openapi-fetch generated client.
 */

import { api, ApiError, apiErrorFromResponse } from "./client";

// ============= Types =============

/**
 * Per-agent backend override entry.
 */
export interface AgentBackendOverride {
  worktree: string;
  role: string;
  backend: string;
}

/**
 * Backend configuration data returned by the API.
 */
export interface BackendConfigData {
  backend: string;
  source: string;
  available: string[];
  agents: AgentBackendOverride[];
}

/**
 * Request body for PATCH /api/config/backend.
 */
export interface BackendConfigPatchRequest {
  backend: string;
}

// ============= Cache =============

const CONFIG_CACHE_KEY = "cortex:config:backend";

/**
 * Get cached backend config from localStorage.
 * Returns null if no cache exists or data is corrupted.
 */
export function getCachedBackendConfig(): BackendConfigData | null {
  try {
    const raw = localStorage.getItem(CONFIG_CACHE_KEY);
    if (!raw) return null;
    const parsed: unknown = JSON.parse(raw);
    // Runtime shape check to guard against stale/corrupted cache
    if (
      typeof parsed !== "object" ||
      parsed === null ||
      typeof (parsed as Record<string, unknown>).backend !== "string" ||
      !Array.isArray((parsed as Record<string, unknown>).available) ||
      !Array.isArray((parsed as Record<string, unknown>).agents)
    ) {
      return null;
    }
    return parsed as BackendConfigData;
  } catch {
    return null;
  }
}

/** Cache backend config in localStorage. */
function cacheBackendConfig(data: BackendConfigData): void {
  try {
    localStorage.setItem(CONFIG_CACHE_KEY, JSON.stringify(data));
  } catch {
    // localStorage may be unavailable (private browsing, quota exceeded) — ignore
  }
}

// ============= API Functions =============

/**
 * Get the current backend configuration.
 * Caches the response in localStorage for offline access.
 */
export async function getBackendConfig(): Promise<BackendConfigData> {
  const { data, error, response } = await api.GET("/api/config/backend");
  if (error) throw apiErrorFromResponse(error, response);
  // BackendConfigResponse has {success, data?, error?} shape
  const envelope = data as {
    success?: boolean;
    data?: BackendConfigData;
    error?: string;
  };
  if (!envelope.success || !envelope.data) {
    throw new ApiError(0, envelope.error ?? "Unknown error");
  }
  cacheBackendConfig(envelope.data);
  return envelope.data;
}

/**
 * Update the project default backend.
 */
export async function updateBackendConfig(
  backend: string,
): Promise<BackendConfigData> {
  const { data, error, response } = await api.PATCH("/api/config/backend", {
    body: { backend },
  });
  if (error) throw apiErrorFromResponse(error, response);
  const envelope = data as {
    success?: boolean;
    data?: BackendConfigData;
    error?: string;
  };
  if (!envelope.success || !envelope.data) {
    throw new ApiError(0, envelope.error ?? "Unknown error");
  }
  return envelope.data;
}
