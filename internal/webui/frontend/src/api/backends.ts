/**
 * API client for backend health endpoint with module-level caching.
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

// ============= Module-level Cache =============

let backendsCache: BackendHealthData[] | null = null;
let fetchPromise: Promise<BackendHealthData[]> | null = null;
let cacheGeneration = 0;

// ============= Helpers =============

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * Fetch registered backends with health status. Returns cached data if available.
 * Deduplicates concurrent in-flight requests.
 */
export async function fetchBackends(): Promise<BackendHealthData[]> {
  if (backendsCache !== null) {
    return backendsCache;
  }
  if (fetchPromise !== null) {
    return fetchPromise;
  }
  const gen = cacheGeneration;
  fetchPromise = get<ApiResult<BackendHealthData[]>>("/api/backends").then(
    (response) => {
      if (gen !== cacheGeneration) {
        return fetchBackends();
      }
      backendsCache = unwrap(response);
      fetchPromise = null;
      return backendsCache;
    },
    (err) => {
      if (gen === cacheGeneration) {
        fetchPromise = null;
      }
      throw err;
    },
  );
  return fetchPromise;
}

/**
 * Invalidate the cache and re-fetch backends from the server.
 */
export async function refreshBackends(): Promise<BackendHealthData[]> {
  cacheGeneration++;
  backendsCache = null;
  fetchPromise = null;
  return fetchBackends();
}
