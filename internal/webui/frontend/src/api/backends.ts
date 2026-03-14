/**
 * API client for backend registry endpoint with module-level caching.
 * Interfaces with GET /api/backends endpoint.
 */

import { get, ApiError } from "./client";

// ============= Types =============

/**
 * Registered backend with metadata and health status.
 */
export interface BackendInfo {
  name: string;
  display_name: string;
  provider: string;
  status: string; // "available" or "unavailable"
  brand_color: string;
  description?: string;
  version?: string;
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

let backendsCache: BackendInfo[] | null = null;
let fetchPromise: Promise<BackendInfo[]> | null = null;
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
 * Fetch registered backends. Returns cached data if available.
 * Deduplicates concurrent in-flight requests.
 */
export async function fetchBackends(): Promise<BackendInfo[]> {
  if (backendsCache !== null) {
    return backendsCache;
  }
  if (fetchPromise !== null) {
    return fetchPromise;
  }
  const gen = cacheGeneration;
  fetchPromise = get<ApiResult<BackendInfo[]>>("/api/backends").then(
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
export async function refreshBackends(): Promise<BackendInfo[]> {
  cacheGeneration++;
  backendsCache = null;
  fetchPromise = null;
  return fetchBackends();
}
