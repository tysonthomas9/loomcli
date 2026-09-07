import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  get,
  post,
  patch,
  del,
  ApiError,
  getAuthState,
  onAuthStateChange,
  getAuthToken,
  setAuthToken,
  setAuthState,
  wsUrl,
  onAuthTokenExpired,
  onWorkspaceUnavailable,
  API_BASE_URL,
  getApiOrigin,
  getWsBaseUrl,
  unwrapResponse,
  api,
} from "../client";

describe("API Client", () => {
  let originalFetch: typeof global.fetch;

  beforeEach(() => {
    originalFetch = global.fetch;
    vi.useFakeTimers();
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("GET requests", () => {
    it("returns parsed JSON on successful request", async () => {
      const mockData = { id: 1, name: "Test" };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(mockData),
      });

      const result = await get<typeof mockData>("/api/test");

      expect(result).toEqual(mockData);
      expect(global.fetch).toHaveBeenCalledWith(
        "/api/test",
        expect.objectContaining({
          method: "GET",
          headers: expect.objectContaining({
            Accept: "application/json",
          }),
          body: null,
        }),
      );
    });

    it("injects a W3C traceparent header on every request", async () => {
      // Every browser-initiated request must carry a traceparent so
      // server-side spans connect to a stable browser-side trace ID.
      // See docs/observability/tracing-contract.md §5.
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const init = mockFn.mock.calls[0][1] as RequestInit;
      const headers = init.headers as Record<string, string>;
      const tp = headers["traceparent"] ?? headers["Traceparent"];
      expect(tp).toBeDefined();
      // W3C format: 00-<32 hex>-<16 hex>-01
      expect(tp).toMatch(/^00-[0-9a-f]{32}-[0-9a-f]{16}-01$/);

      // Two independent calls must produce two independent trace IDs.
      await get("/api/test2");
      const init2 = mockFn.mock.calls[1][1] as RequestInit;
      const headers2 = init2.headers as Record<string, string>;
      const tp2 = headers2["traceparent"] ?? headers2["Traceparent"];
      expect(tp2).not.toBe(tp);
    });

    it("does not include Content-Type header for requests without body", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).not.toHaveProperty("Content-Type");
    });
  });

  describe("POST requests", () => {
    it("sends body and returns response", async () => {
      const requestBody = { name: "New Item" };
      const responseData = { id: 1, name: "New Item" };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: () => Promise.resolve(responseData),
      });

      const result = await post<typeof responseData>("/api/items", requestBody);

      expect(result).toEqual(responseData);
      expect(global.fetch).toHaveBeenCalledWith(
        "/api/items",
        expect.objectContaining({
          method: "POST",
          body: JSON.stringify(requestBody),
        }),
      );
    });

    it("sets Content-Type header for requests with body", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 201,
        json: () => Promise.resolve({}),
      });

      await post("/api/items", { data: "test" });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toHaveProperty(
        "Content-Type",
        "application/json",
      );
    });
  });

  describe("PATCH requests", () => {
    it("sends partial body and returns response", async () => {
      const partialUpdate = { name: "Updated Name" };
      const responseData = { id: 1, name: "Updated Name", age: 30 };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(responseData),
      });

      const result = await patch<typeof responseData>(
        "/api/items/1",
        partialUpdate,
      );

      expect(result).toEqual(responseData);
      expect(global.fetch).toHaveBeenCalledWith(
        "/api/items/1",
        expect.objectContaining({
          method: "PATCH",
          body: JSON.stringify(partialUpdate),
        }),
      );
    });
  });

  describe("DELETE requests", () => {
    it("works correctly", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 204,
        json: () => Promise.reject(new Error("No content")),
      });

      const result = await del("/api/items/1");

      expect(result).toBeUndefined();
      expect(global.fetch).toHaveBeenCalledWith(
        "/api/items/1",
        expect.objectContaining({
          method: "DELETE",
          body: null,
        }),
      );
    });
  });

  describe("Error handling", () => {
    it("unwrapResponse throws ApiError for a missing response envelope", () => {
      expect(() =>
        unwrapResponse(
          undefined,
          new Response(null, { status: 200, statusText: "OK" }),
        ),
      ).toThrow(ApiError);

      try {
        unwrapResponse(
          undefined,
          new Response(null, { status: 200, statusText: "OK" }),
        );
        throw new Error("Should have thrown");
      } catch (error) {
        expect(error).toMatchObject({
          status: 200,
          statusText: "OK",
          body: "missing response envelope",
        });
      }
    });

    it("unwrapResponse preserves HTTP status when an envelope reports failure", () => {
      expect(() =>
        unwrapResponse(
          { success: false, error: "conflicting write" },
          new Response(null, { status: 409, statusText: "Conflict" }),
        ),
      ).toThrow(ApiError);

      try {
        unwrapResponse(
          { success: false, error: "conflicting write" },
          new Response(null, { status: 409, statusText: "Conflict" }),
        );
        throw new Error("Should have thrown");
      } catch (error) {
        expect(error).toMatchObject({
          status: 409,
          statusText: "Conflict",
          body: "conflicting write",
        });
      }
    });

    it("throws ApiError with status 404 for not found", async () => {
      const errorBody = { error: "Not found" };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 404,
        statusText: "Not Found",
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get("/api/items/999")).rejects.toThrow(ApiError);
      await expect(get("/api/items/999")).rejects.toMatchObject({
        status: 404,
        statusText: "Not Found",
        body: errorBody,
      });
    });

    it("throws ApiError with status 500 for server error", async () => {
      const errorBody = { error: "Internal server error" };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get("/api/broken")).rejects.toThrow(ApiError);
      await expect(get("/api/broken")).rejects.toMatchObject({
        status: 500,
        statusText: "Internal Server Error",
        body: errorBody,
      });
    });

    it("throws ApiError with status 0 for network error", async () => {
      global.fetch = vi
        .fn()
        .mockRejectedValue(new TypeError("Failed to fetch"));

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      await expect(get("/api/test")).rejects.toMatchObject({
        status: 0,
        statusText: "Network error",
      });
    });

    it("throws ApiError with status 0 for timeout", async () => {
      // Use real timers for this test since fake timers interact poorly with AbortController
      vi.useRealTimers();

      // Create an AbortError like the browser would
      const abortError = new DOMException(
        "The operation was aborted.",
        "AbortError",
      );

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          // Listen for abort signal
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(abortError);
            });
          }
        });
      });

      // Use a very short timeout for testing
      const requestPromise = get("/api/slow", { timeout: 10 });

      await expect(requestPromise).rejects.toThrow(ApiError);

      // Reset mock for the second assertion
      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(abortError);
            });
          }
        });
      });

      const requestPromise2 = get("/api/slow", { timeout: 10 });

      try {
        await requestPromise2;
        throw new Error("Should have thrown");
      } catch (e) {
        expect(e).toMatchObject({
          status: 0,
          statusText: "Request timeout",
        });
      }

      // Restore fake timers for other tests
      vi.useFakeTimers();
    });

    it("handles text error body when JSON parsing fails", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        statusText: "Bad Request",
        text: () => Promise.resolve("Plain text error"),
      });

      await expect(get("/api/bad")).rejects.toMatchObject({
        status: 400,
        body: "Plain text error",
      });
    });

    it("handles JSON error body", async () => {
      const errorBody = { error: "Bad request", details: "Invalid field" };
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 400,
        statusText: "Bad Request",
        text: () => Promise.resolve(JSON.stringify(errorBody)),
      });

      await expect(get("/api/bad")).rejects.toMatchObject({
        status: 400,
        body: errorBody,
      });
    });
  });

  describe("openapi-fetch middleware", () => {
    it("aborts requests after the default timeout when no caller signal is provided", async () => {
      const mockFetch = vi.fn((_request: Request) => {
        return new Promise<Response>(() => {
          // Keep the request pending so only the middleware timeout can abort it.
        });
      });

      const requestPromise = api.GET("/api/workspaces/active", {
        baseUrl: "http://localhost",
        fetch: mockFetch,
      });
      requestPromise.catch(() => undefined);

      await vi.waitFor(() => expect(mockFetch).toHaveBeenCalledTimes(1));
      const request = mockFetch.mock.calls[0]?.[0] as Request;
      expect(request.signal.aborted).toBe(false);

      vi.advanceTimersByTime(30_000);

      expect(request.signal.aborted).toBe(true);
    });
  });

  // The openapi-fetch middleware carries the same 503 guard as fetchApi and
  // must not be left behind when the guard changes. It is exercised here on
  // /terminal/token rather than /claims/hold only because the claim-hold routes
  // are not in the generated OpenAPI paths — the predicate is the same one.
  describe("openapi-fetch middleware outage exemptions", () => {
    const respond = (status: number, statusText: string) =>
      vi.fn(async (request: Request) =>
        new URL(request.url).pathname === "/api/client-errors"
          ? new Response(null, { status: 204 })
          : new Response("failed", { status, statusText }),
      );

    it("does not notify or report a 503 on an exempt path", async () => {
      const reports = vi.fn();
      global.fetch = vi.fn((input: RequestInfo | URL) => {
        reports(String(input));
        return Promise.resolve({ ok: true, status: 204 });
      }) as unknown as typeof global.fetch;
      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await api.GET("/api/workspaces/{ws}/terminal/token", {
          baseUrl: "http://localhost",
          params: { path: { ws: "PUPPET" } },
          fetch: respond(503, "Terminal Unavailable"),
        });

        expect(cb).not.toHaveBeenCalled();
        expect(reports).not.toHaveBeenCalled();
      } finally {
        unsub();
      }
    });

    it("still notifies and reports a 503 on a non-exempt path", async () => {
      const reports = vi.fn();
      global.fetch = vi.fn((input: RequestInfo | URL) => {
        reports(String(input));
        return Promise.resolve({ ok: true, status: 204 });
      }) as unknown as typeof global.fetch;
      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await api.GET("/api/workspaces/{ws}/issues", {
          baseUrl: "http://localhost",
          params: { path: { ws: "PUPPET" } },
          fetch: respond(503, "Issues Service Down"),
        });

        expect(cb).toHaveBeenCalledTimes(1);
        expect(reports).toHaveBeenCalledWith("/api/client-errors");
      } finally {
        unsub();
      }
    });

    it("still reports a 500 on an exempt path", async () => {
      const reports = vi.fn();
      global.fetch = vi.fn((input: RequestInfo | URL) => {
        reports(String(input));
        return Promise.resolve({ ok: true, status: 204 });
      }) as unknown as typeof global.fetch;

      await api.GET("/api/workspaces/{ws}/terminal/token", {
        baseUrl: "http://localhost",
        params: { path: { ws: "PUPPET" } },
        fetch: respond(500, "Terminal Boom"),
      });

      expect(reports).toHaveBeenCalledWith("/api/client-errors");
    });
  });

  describe("Combined signal behavior (AbortSignal.any)", () => {
    it("timeout works when caller provides their own signal", async () => {
      vi.useRealTimers();

      const abortError = new DOMException(
        "The operation was aborted.",
        "AbortError",
      );

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController = new AbortController();
      const requestPromise = get("/api/slow", {
        timeout: 10,
        signal: callerController.signal,
      });

      await expect(requestPromise).rejects.toThrow(ApiError);

      // Verify it was a timeout error, not a caller abort
      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController2 = new AbortController();
      const requestPromise2 = get("/api/slow", {
        timeout: 10,
        signal: callerController2.signal,
      });

      try {
        await requestPromise2;
        throw new Error("Should have thrown");
      } catch (e) {
        expect(e).toMatchObject({
          status: 0,
          statusText: "Request timeout",
        });
      }

      vi.useFakeTimers();
    });

    it("caller signal abort works when timeout is also configured", async () => {
      vi.useRealTimers();

      const abortError = new DOMException(
        "The operation was aborted.",
        "AbortError",
      );

      global.fetch = vi.fn().mockImplementation((_url, options) => {
        return new Promise((_, reject) => {
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(abortError);
            });
          }
        });
      });

      const callerController = new AbortController();
      const requestPromise = get("/api/slow", {
        timeout: 5000,
        signal: callerController.signal,
      });

      // Abort from caller before timeout fires
      callerController.abort();

      await expect(requestPromise).rejects.toThrow(DOMException);
      await expect(
        // Need a fresh request for the second assertion
        (async () => {
          global.fetch = vi.fn().mockImplementation((_url, options) => {
            return new Promise((_, reject) => {
              if (options?.signal) {
                options.signal.addEventListener("abort", () => {
                  reject(abortError);
                });
              }
            });
          });
          const ctrl = new AbortController();
          const p = get("/api/slow", { timeout: 5000, signal: ctrl.signal });
          ctrl.abort();
          return p;
        })(),
      ).rejects.not.toThrow(ApiError);

      vi.useFakeTimers();
    });

    it("passes combined signal to fetch when caller provides signal", async () => {
      const mockData = { id: 1 };
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve(mockData),
      });

      const callerController = new AbortController();
      await get("/api/test", { signal: callerController.signal });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { signal: AbortSignal };
      // The signal should NOT be the caller's signal directly (it should be a combined signal)
      expect(options.signal).not.toBe(callerController.signal);
      // But it should still be an AbortSignal
      expect(options.signal).toBeInstanceOf(AbortSignal);
    });
  });

  describe("Custom headers", () => {
    it("can merge custom headers with defaults", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test", {
        headers: {
          Authorization: "Bearer token123",
          "X-Custom-Header": "custom-value",
        },
      });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).toMatchObject({
        Accept: "application/json",
        Authorization: "Bearer token123",
        "X-Custom-Header": "custom-value",
      });
    });

    it("custom headers can override default Accept header", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test", {
        headers: {
          Accept: "text/plain",
        },
      });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      expect(call).toBeDefined();
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers.Accept).toBe("text/plain");
    });
  });

  describe("Auth", () => {
    beforeEach(() => {
      // Reset to known baseline before each auth test
      setAuthToken(null);
    });

    it("setAuthToken(token) sets token and transitions to 'authenticated'", () => {
      setAuthToken("jwt-123");

      expect(getAuthToken()).toBe("jwt-123");
      expect(getAuthState()).toBe("authenticated");
    });

    it("setAuthToken(null) clears token and transitions to 'none'", () => {
      setAuthToken("jwt-123");
      setAuthToken(null);

      expect(getAuthToken()).toBeNull();
      expect(getAuthState()).toBe("none");
    });

    it("setAuthState('failed') transitions state correctly", () => {
      setAuthState("failed");

      expect(getAuthState()).toBe("failed");
    });

    it("default auth state is 'none'", () => {
      // After resetting token to null, state should be 'none'
      expect(getAuthState()).toBe("none");
    });

    it("onAuthStateChange fires when setAuthToken is called", () => {
      const callback = vi.fn();
      const unsubscribe = onAuthStateChange(callback);

      try {
        setAuthToken("test-jwt");

        expect(callback).toHaveBeenCalledWith("authenticated");
        expect(callback).toHaveBeenCalledTimes(1);
      } finally {
        unsubscribe();
      }
    });

    it("fetchApi injects Bearer header when token is set via setAuthToken", async () => {
      setAuthToken("my-jwt");

      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ data: "test" }),
      });

      await get("/api/test");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers["Authorization"]).toBe("Bearer my-jwt");
    });

    it("fetchApi does NOT inject header when token is null", async () => {
      setAuthToken(null);

      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test");

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers).not.toHaveProperty("Authorization");
    });

    it("explicit Authorization header is not overridden by setAuthToken", async () => {
      setAuthToken("default-jwt");

      global.fetch = vi.fn().mockResolvedValue({
        ok: true,
        status: 200,
        json: () => Promise.resolve({}),
      });

      await get("/api/test", {
        headers: { Authorization: "Bearer custom" },
      });

      const mockFn = global.fetch as ReturnType<typeof vi.fn>;
      const call = mockFn.mock.calls[0];
      const options = call?.[1] as { headers: Record<string, string> };
      expect(options.headers["Authorization"]).toBe("Bearer custom");
    });

    it("401 response notifies auth-token-expired listeners", async () => {
      setAuthToken("my-jwt");

      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        text: () => Promise.resolve("unauthorized"),
      });

      const cb = vi.fn();
      const unsub = onAuthTokenExpired(cb);

      try {
        await expect(get("/api/test")).rejects.toThrow(ApiError);
        expect(cb).toHaveBeenCalledTimes(1);
      } finally {
        unsub();
      }
    });

    it("401 response does NOT notify when token is null", async () => {
      setAuthToken(null);

      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        text: () => Promise.resolve("unauthorized"),
      });

      const cb = vi.fn();
      const unsub = onAuthTokenExpired(cb);

      try {
        await expect(get("/api/test")).rejects.toThrow(ApiError);
        expect(cb).not.toHaveBeenCalled();
      } finally {
        unsub();
      }
    });

    it("401 response clears authToken and transitions state to 'none'", async () => {
      setAuthToken("my-jwt");

      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        text: () => Promise.resolve("unauthorized"),
      });

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      expect(getAuthToken()).toBeNull();
      expect(getAuthState()).toBe("none");
    });

    it("401 does not retry — only 1 fetch call", async () => {
      setAuthToken("my-jwt");

      const mockFetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        text: () => Promise.resolve("unauthorized"),
      });
      global.fetch = mockFetch;

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      expect(mockFetch).toHaveBeenCalledTimes(1);
    });

    it("onAuthStateChange unsubscribe prevents further callbacks", () => {
      const callback = vi.fn();
      const unsubscribe = onAuthStateChange(callback);
      unsubscribe();

      setAuthToken("test-jwt");

      expect(callback).not.toHaveBeenCalled();
    });

    it("onAuthStateChange fires on state transitions", () => {
      const callback = vi.fn();
      const unsubscribe = onAuthStateChange(callback);

      try {
        setAuthToken("listener-token");
        expect(callback).toHaveBeenCalledWith("authenticated");

        setAuthToken(null);
        expect(callback).toHaveBeenCalledWith("none");

        expect(callback).toHaveBeenCalledTimes(2);
      } finally {
        unsubscribe();
      }
    });

    it("onWorkspaceUnavailable unsubscribe prevents further callbacks", async () => {
      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);
      unsub();

      setAuthToken(null);
      global.fetch = vi
        .fn()
        .mockRejectedValue(new TypeError("Failed to fetch"));

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      expect(cb).not.toHaveBeenCalled();
    });

    it("onAuthTokenExpired unsubscribe prevents further callbacks", async () => {
      setAuthToken("my-jwt");

      const cb = vi.fn();
      const unsub = onAuthTokenExpired(cb);
      unsub();

      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 401,
        statusText: "Unauthorized",
        text: () => Promise.resolve("unauthorized"),
      });

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      expect(cb).not.toHaveBeenCalled();
    });

    it("503 response notifies workspace service-unavailable listeners", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 503,
        statusText: "Service Unavailable",
        text: () => Promise.resolve("unavailable"),
      });

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/test")).rejects.toThrow(ApiError);
        expect(cb).toHaveBeenCalledTimes(1);
      } finally {
        unsub();
      }
    });

    it("429 is a throttle, not an outage: no offline badge, no error report", async () => {
      const reportSpy = vi.spyOn(
        await import("../errorReporter"),
        "reportError",
      );
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 429,
        statusText: "Too Many Requests",
        headers: new Headers({ "Retry-After": "12" }),
        text: () =>
          Promise.resolve(JSON.stringify({ error: "rate limit exceeded" })),
      });

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/test")).rejects.toMatchObject({
          status: 429,
          retryAfterMs: 12000,
        });
        expect(cb).not.toHaveBeenCalled();
        expect(reportSpy).not.toHaveBeenCalled();
      } finally {
        unsub();
      }
    });

    it("500 still reports a client error", async () => {
      const reportSpy = vi.spyOn(
        await import("../errorReporter"),
        "reportError",
      );
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 500,
        statusText: "Internal Server Error",
        headers: new Headers(),
        text: () => Promise.resolve("boom"),
      });

      await expect(get("/api/test")).rejects.toThrow(ApiError);
      expect(reportSpy).toHaveBeenCalled();
    });

    it("a 429 without Retry-After leaves retryAfterMs undefined", async () => {
      global.fetch = vi.fn().mockResolvedValue({
        ok: false,
        status: 429,
        statusText: "Too Many Requests",
        headers: new Headers(),
        text: () =>
          Promise.resolve(JSON.stringify({ error: "rate limit exceeded" })),
      });

      await expect(get("/api/test")).rejects.toMatchObject({
        status: 429,
        retryAfterMs: undefined,
      });
    });

    // errorReporter opens a circuit breaker after three consecutive failed
    // reports and the suite's fake timers never advance Date.now() to close it
    // again, so the report endpoint must answer ok here. It also dedups on
    // `${type}:${message}` for 5 s, which is why each case below uses its own
    // statusText.
    const mockFetchExcept = (status: number, statusText: string) =>
      vi.fn((url: string) =>
        url === "/api/client-errors"
          ? Promise.resolve({ ok: true, status: 204 })
          : Promise.resolve({
              ok: false,
              status,
              statusText,
              text: () => Promise.resolve("failed"),
            }),
      ) as unknown as typeof global.fetch & ReturnType<typeof vi.fn>;

    // PUPPET-529: /api/workspaces/{ws}/claims/hold answers 503 wherever the
    // server can reach no agent supervisor, and useClaimHold treats that as
    // "no hold" by design — so the transport must stay quiet about it. Before
    // the fix each 10 s poll fired a workspace-unavailable notification and a
    // POST /api/client-errors; both assertions below used to be the opposite.
    it("503 from /claims/hold neither notifies workspace-unavailable nor files a client-error report", async () => {
      const fetchMock = mockFetchExcept(503, "Supervisor Unavailable");
      global.fetch = fetchMock;

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/workspaces/PUPPET/claims/hold")).rejects.toThrow(
          ApiError,
        );

        expect(cb).not.toHaveBeenCalled();
        expect(
          fetchMock.mock.calls.some((call) => call[0] === "/api/client-errors"),
        ).toBe(false);
      } finally {
        unsub();
      }
    });

    // The release path carries its actor/force as a query string, so the
    // exemption must match on a substring rather than the whole path. The 503
    // is still thrown — useClaimHold.release puts it on screen; what is
    // suppressed is only the fleet-wide "workspace offline" signal and the
    // server-side report for a failure the UI already showed.
    it("503 from a /claims/hold release with a query string is exempt too", async () => {
      const fetchMock = mockFetchExcept(
        503,
        "Supervisor Unavailable (release)",
      );
      global.fetch = fetchMock;

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(
          del("/api/workspaces/PUPPET/claims/hold?actor=a&force=true"),
        ).rejects.toThrow(ApiError);

        expect(cb).not.toHaveBeenCalled();
        expect(
          fetchMock.mock.calls.some((call) => call[0] === "/api/client-errors"),
        ).toBe(false);
      } finally {
        unsub();
      }
    });

    // The exemption is 503-only: any other 5xx on an exempt path is a genuine
    // bug and must still be reported.
    it("500 from /claims/hold is still reported", async () => {
      const fetchMock = mockFetchExcept(500, "Claims Hold Boom");
      global.fetch = fetchMock;

      await expect(get("/api/workspaces/PUPPET/claims/hold")).rejects.toThrow(
        ApiError,
      );

      expect(
        fetchMock.mock.calls.some((call) => call[0] === "/api/client-errors"),
      ).toBe(true);
    });

    // The genuine-outage regression: a 503 anywhere else still means the
    // workspace service is down and must still raise both signals.
    it("503 from a non-exempt workspace path still notifies and reports", async () => {
      const fetchMock = mockFetchExcept(503, "Workspace Service Down");
      global.fetch = fetchMock;

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/workspaces/PUPPET/issues")).rejects.toThrow(
          ApiError,
        );

        expect(cb).toHaveBeenCalledTimes(1);
        expect(
          fetchMock.mock.calls.some((call) => call[0] === "/api/client-errors"),
        ).toBe(true);
      } finally {
        unsub();
      }
    });

    // A dead server is a real outage on every path, exempt or not — this is
    // what keeps the offline badge honest.
    it("network rejection on /claims/hold still notifies workspace-unavailable", async () => {
      global.fetch = vi
        .fn()
        .mockRejectedValue(new TypeError("Failed to fetch"));

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/workspaces/PUPPET/claims/hold")).rejects.toThrow(
          ApiError,
        );
        expect(cb).toHaveBeenCalledTimes(1);
      } finally {
        unsub();
      }
    });

    it("network error notifies workspace service-unavailable listeners", async () => {
      global.fetch = vi
        .fn()
        .mockRejectedValue(new TypeError("Failed to fetch"));

      const cb = vi.fn();
      const unsub = onWorkspaceUnavailable(cb);

      try {
        await expect(get("/api/test")).rejects.toThrow(ApiError);
        expect(cb).toHaveBeenCalledTimes(1);
      } finally {
        unsub();
      }
    });
  });

  describe("wsUrl", () => {
    it("builds workspace-scoped API path", () => {
      expect(wsUrl("my-ws", "/issues")).toBe("/api/workspaces/my-ws/issues");
    });

    it("encodes workspace ID with special characters", () => {
      expect(wsUrl("ws with spaces", "/issues")).toBe(
        "/api/workspaces/ws%20with%20spaces/issues",
      );
    });

    it("handles slashes in workspace ID", () => {
      expect(wsUrl("ws/id", "/issues")).toBe("/api/workspaces/ws%2Fid/issues");
    });
  });

  describe("API_BASE_URL helpers", () => {
    // Save/restore window.location across tests that mutate it.
    let savedLocation: Location | undefined;
    const hasWindow = typeof window !== "undefined";

    beforeEach(() => {
      if (hasWindow) {
        savedLocation = window.location;
      }
    });

    afterEach(() => {
      if (hasWindow && savedLocation) {
        Object.defineProperty(window, "location", {
          value: savedLocation,
          writable: true,
          configurable: true,
        });
      }
    });

    it("exposes API_BASE_URL as a string (empty by default in test env)", () => {
      expect(typeof API_BASE_URL).toBe("string");
      // No VITE_API_BASE_URL is set for Vitest, so this should be "".
      expect(API_BASE_URL).toBe("");
    });

    it("getApiOrigin falls back when API_BASE_URL is empty", () => {
      // API_BASE_URL is empty → either window.location.origin (browser/jsdom)
      // or the "http://localhost" fallback (Node env).
      const origin = getApiOrigin();
      if (hasWindow && window.location) {
        expect(origin).toBe(window.location.origin);
      } else {
        expect(origin).toBe("http://localhost");
      }
    });

    it("getWsBaseUrl converts http → ws", () => {
      if (!hasWindow) {
        // Node env: fallback is http://localhost → ws://localhost
        expect(getWsBaseUrl()).toBe("ws://localhost");
        return;
      }
      Object.defineProperty(window, "location", {
        value: { origin: "http://example.com:8080" },
        writable: true,
        configurable: true,
      });
      expect(getWsBaseUrl()).toBe("ws://example.com:8080");
    });

    it("getWsBaseUrl converts https → wss", () => {
      if (!hasWindow) {
        // Can't exercise https path without a window mock; skip.
        return;
      }
      Object.defineProperty(window, "location", {
        value: { origin: "https://secure.example.com" },
        writable: true,
        configurable: true,
      });
      expect(getWsBaseUrl()).toBe("wss://secure.example.com");
    });

    it("getWsBaseUrl preserves port and path after http→ws conversion", () => {
      if (!hasWindow) return;
      Object.defineProperty(window, "location", {
        value: { origin: "https://host.example.com:1234" },
        writable: true,
        configurable: true,
      });
      expect(getWsBaseUrl()).toBe("wss://host.example.com:1234");
    });
  });
});
