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
 *
 * The Go server returns validation/domain errors as `{ "error": "<message>",
 * "kind": "<category>" }`. When that shape is present we surface the server's
 * message as `Error.message` so callers that do `setError(err.message)` see
 * "agent name must be lowercase…" instead of "API Error: 400 Bad Request".
 */
export class ApiError extends Error {
  constructor(
    public status: number,
    public statusText: string,
    public body?: unknown,
    /**
     * Milliseconds the server asked us to wait, parsed from `Retry-After`.
     * Present only on 429/503 responses that carried the header.
     */
    public retryAfterMs?: number,
  ) {
    super(deriveMessage(status, statusText, body));
    this.name = "ApiError";
  }
}

function deriveMessage(
  status: number,
  statusText: string,
  body: unknown,
): string {
  if (body && typeof body === "object" && "error" in body) {
    const value = (body as { error?: unknown }).error;
    if (typeof value === "string" && value.length > 0) {
      return value;
    }
  }
  return `API Error: ${status} ${statusText}`;
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
