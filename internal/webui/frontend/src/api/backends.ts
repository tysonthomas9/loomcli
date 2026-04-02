/**
 * API client for backend health endpoint.
 * Stateless — no module-level caches. Caching belongs in backendsStore.
 * Interfaces with GET /api/backends endpoint.
 */

import { get, ApiError } from "./client";

// ============= Types =============

/**
 * Backend health data returned by the API.
 */
export interface BackendHealthData {
  name: string;
  /** Human-readable name; may be empty string if backend has no custom display name. */
  display_name: string;
  available: boolean;
  installed: boolean;
  api_key_set: boolean;
  version?: string;
  message?: string;
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
 * Fetch registered backends with health status. Always hits the network.
 */
export async function fetchBackends(): Promise<BackendHealthData[]> {
  const response = await get<ApiResult<BackendHealthData[]>>("/api/backends");
  return unwrap(response);
}

/**
 * Re-fetch backends from the server. Alias for fetchBackends (no cache to invalidate).
 */
export async function refreshBackends(): Promise<BackendHealthData[]> {
  return fetchBackends();
}
