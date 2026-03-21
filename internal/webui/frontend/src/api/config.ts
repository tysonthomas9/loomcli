/**
 * API functions for backend configuration.
 * Interfaces with GET/PATCH /api/config/backend endpoints.
 */

import { get, patch, ApiError } from "./client";

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

// ============= Response Types =============

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

// ============= Helpers =============

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
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
  const response = await get<ApiResult<BackendConfigData>>(
    "/api/config/backend",
  );
  const data = unwrap(response);
  cacheBackendConfig(data);
  return data;
}

/**
 * Update the project default backend.
 */
export async function updateBackendConfig(
  backend: string,
): Promise<BackendConfigData> {
  const response = await patch<ApiResult<BackendConfigData>>(
    "/api/config/backend",
    { backend },
  );
  return unwrap(response);
}
