/**
 * API functions for backend configuration.
 * Interfaces with GET/PATCH /api/config/backend endpoints.
 */

import { get, patch, ApiError } from './client';

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

// ============= API Functions =============

/**
 * Get the current backend configuration.
 */
export async function getBackendConfig(): Promise<BackendConfigData> {
  const response = await get<ApiResult<BackendConfigData>>('/api/config/backend');
  return unwrap(response);
}

/**
 * Update the project default backend.
 */
export async function updateBackendConfig(backend: string): Promise<BackendConfigData> {
  const response = await patch<ApiResult<BackendConfigData>>('/api/config/backend', { backend });
  return unwrap(response);
}
