/**
 * Shared error classes used across the frontend.
 *
 * These live in src/types/ so that components can render error UIs without
 * crossing the frontend layer DAG back into the api layer. The api layer
 * re-exports them from its own files for its own throws.
 */

/**
 * Generic API error raised by the api client layer.
 * Wraps an HTTP status code, status text, and optional response body.
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown,
  ) {
    super(`API Error: ${status} ${statusText}`);
    this.name = "ApiError";
  }
}

/**
 * Error raised by app-config bootstrap (GET /api/config). Indicates the
 * server is unreachable or returned an invalid auth configuration.
 */
export class AppConfigError extends Error {
  constructor(
    message: string,
    public cause?: unknown,
  ) {
    super(message);
    this.name = "AppConfigError";
  }
}
