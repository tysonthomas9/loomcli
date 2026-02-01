/**
 * API-specific types for request/response shapes.
 */

/**
 * Generic API response wrapper.
 * Used for successful responses with data.
 */
export interface ApiResponse<T> {
  data: T;
  success: true;
}

/**
 * API error response structure.
 */
export interface ApiError {
  error: string;
  code?: string;
  details?: Record<string, unknown>;
  success: false;
}

/**
 * Union type for API responses.
 */
export type ApiResult<T> = ApiResponse<T> | ApiError;

/**
 * Type guard to check if result is successful.
 */
export function isApiSuccess<T>(result: ApiResult<T>): result is ApiResponse<T> {
  return result.success === true;
}

/**
 * Type guard to check if result is an error.
 */
export function isApiError<T>(result: ApiResult<T>): result is ApiError {
  return result.success === false;
}

/**
 * Paginated response wrapper for future use.
 */
export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  page: number;
  page_size: number;
  has_more: boolean;
}
