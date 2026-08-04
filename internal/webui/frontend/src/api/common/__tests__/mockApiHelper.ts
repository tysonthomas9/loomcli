/**
 * Shared mock helpers for API tests.
 * Creates a mock `api` object that delegates to mock `get`/`post`/etc functions
 * so existing test assertions continue to work.
 */
import { vi } from "vitest";

/**
 * Create a mock api object (openapi-fetch client) whose methods return
 * { data, error, response } shaped responses.
 *
 * Usage in vi.mock:
 *   api: createMockApi()
 */
export function createMockApi() {
  return {
    GET: vi.fn().mockResolvedValue({
      data: undefined,
      error: undefined,
      response: undefined,
    }),
    POST: vi.fn().mockResolvedValue({
      data: undefined,
      error: undefined,
      response: undefined,
    }),
    PATCH: vi.fn().mockResolvedValue({
      data: undefined,
      error: undefined,
      response: undefined,
    }),
    PUT: vi.fn().mockResolvedValue({
      data: undefined,
      error: undefined,
      response: undefined,
    }),
    DELETE: vi.fn().mockResolvedValue({
      data: undefined,
      error: undefined,
      response: undefined,
    }),
    use: vi.fn(),
  };
}

/** Helper to create a successful openapi-fetch response */
export function okResponse<T>(data: T, status = 200) {
  return {
    data,
    error: undefined,
    response: new Response(null, { status }),
  };
}

/** Helper to create an error openapi-fetch response */
export function errResponse(
  body: unknown,
  status = 400,
  statusText = "Bad Request",
) {
  return {
    data: undefined,
    error: body,
    response: new Response(null, { status, statusText }),
  };
}
