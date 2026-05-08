/**
 * API client for backend health endpoint.
 * Stateless — no module-level caches. Caching belongs in backendsStore.
 * Uses raw fetch because the spec response is untyped Record<string, never>.
 */

import { get, ApiError } from "@/api/common";

// ============= Types =============

/**
 * One curated install or login command for a backend. Mirrors
 * misc.SetupAction in internal/webui/handlers/misc/backend_setup_metadata.go.
 */
export interface BackendSetupAction {
  id: string;
  label: string;
  command: string;
  interactive: boolean;
}

/** Env-var hint advertised by a backend. */
export interface BackendEnvVarHint {
  name: string;
  restart_required: boolean;
}

/**
 * Backend health data returned by the API.
 *
 * The base health booleans (name, display_name, available, installed,
 * api_key_set, version, message) are populated by every server. The
 * setup-related fields (description, authenticated, ready,
 * install_actions, login_actions, env_vars) are added server-side from
 * the curated registry; they are optional so older clients/responses
 * still type-check.
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
  description?: string;
  authenticated?: boolean;
  ready?: boolean;
  install_actions?: BackendSetupAction[];
  login_actions?: BackendSetupAction[];
  env_vars?: BackendEnvVarHint[];
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
