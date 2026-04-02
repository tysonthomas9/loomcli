/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for appConfig.ts — auth mode discovery via GET /api/config.
 *
 * Uses vi.resetModules() + dynamic import in beforeEach to get a fresh
 * module with empty cache for each test (same pattern as editors.test.ts).
 * Mocks global.fetch directly (same pattern as client.test.ts).
 */

import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

let fetchAppConfig: typeof import("../appConfig").fetchAppConfig;
let AppConfigError: typeof import("../appConfig").AppConfigError;

function jsonResponse(
  body: unknown,
  { status = 200, contentType = "application/json" } = {},
) {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: status === 200 ? "OK" : "Error",
    headers: new Headers({ "Content-Type": contentType }),
    json: () => Promise.resolve(body),
  };
}

describe("appConfig", () => {
  let originalFetch: typeof global.fetch;

  beforeEach(async () => {
    originalFetch = global.fetch;
    vi.resetModules();

    const mod = await import("../appConfig");
    fetchAppConfig = mod.fetchAppConfig;
    AppConfigError = mod.AppConfigError;
  });

  afterEach(() => {
    global.fetch = originalFetch;
    vi.restoreAllMocks();
  });

  it("returns {mode:'open'} for open response", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse({ mode: "open" }));

    const config = await fetchAppConfig();

    expect(config).toEqual({ mode: "open" });
  });

  it("returns {mode:'oidc', auth_url} for oidc response", async () => {
    global.fetch = vi.fn().mockResolvedValue(
      jsonResponse({
        mode: "oidc",
        auth_url: "https://auth.example.com",
      }),
    );

    const config = await fetchAppConfig();

    expect(config).toEqual({
      mode: "oidc",
      auth_url: "https://auth.example.com",
    });
  });

  it("caches result on success (fetch called once)", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse({ mode: "open" }));

    await fetchAppConfig();
    await fetchAppConfig();

    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  it("resets cache on failure (allows retry)", async () => {
    const mockFetch = vi.fn();
    global.fetch = mockFetch;

    mockFetch.mockRejectedValueOnce(new TypeError("Network error"));
    await expect(fetchAppConfig()).rejects.toThrow(AppConfigError);

    mockFetch.mockResolvedValueOnce(jsonResponse({ mode: "open" }));
    const config = await fetchAppConfig();

    expect(config).toEqual({ mode: "open" });
    expect(mockFetch).toHaveBeenCalledTimes(2);
  });

  it("deduplicates concurrent calls", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse({ mode: "open" }));

    const [a, b] = await Promise.all([fetchAppConfig(), fetchAppConfig()]);

    expect(a).toEqual({ mode: "open" });
    expect(b).toEqual({ mode: "open" });
    expect(global.fetch).toHaveBeenCalledTimes(1);
  });

  it("throws AppConfigError on network error", async () => {
    global.fetch = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));

    await expect(fetchAppConfig()).rejects.toThrow(AppConfigError);
    await expect(
      // Need fresh module for second assertion
      (async () => {
        vi.resetModules();
        const mod = await import("../appConfig");
        global.fetch = vi
          .fn()
          .mockRejectedValue(new TypeError("Failed to fetch"));
        return mod.fetchAppConfig();
      })(),
    ).rejects.toThrow("Unable to reach server");
  });

  it("throws AppConfigError on timeout", async () => {
    vi.useFakeTimers();

    // fetch that never resolves — listens for abort signal
    global.fetch = vi
      .fn()
      .mockImplementation((_url: string, options: { signal?: AbortSignal }) => {
        return new Promise((_resolve, reject) => {
          if (options?.signal) {
            options.signal.addEventListener("abort", () => {
              reject(
                new DOMException("The operation was aborted.", "AbortError"),
              );
            });
          }
        });
      });

    vi.resetModules();
    const mod = await import("../appConfig");
    const promise = mod.fetchAppConfig();

    // Register the assertion before advancing timers to avoid unhandled rejection
    const assertion = expect(promise).rejects.toThrow(
      "Config request timed out",
    );

    // Advance past the 5-second timeout
    await vi.advanceTimersByTimeAsync(5001);

    await assertion;

    vi.useRealTimers();
  });

  it("throws AppConfigError on non-JSON response", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(
        jsonResponse("<html>SPA</html>", { contentType: "text/html" }),
      );

    await expect(fetchAppConfig()).rejects.toThrow(AppConfigError);
    await expect(
      (async () => {
        vi.resetModules();
        const mod = await import("../appConfig");
        global.fetch = vi
          .fn()
          .mockResolvedValue(
            jsonResponse("<html>SPA</html>", { contentType: "text/html" }),
          );
        return mod.fetchAppConfig();
      })(),
    ).rejects.toThrow("Server returned non-JSON response");
  });

  it("throws AppConfigError on HTTP error status", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ error: "fail" }, { status: 500 }));

    await expect(fetchAppConfig()).rejects.toThrow(AppConfigError);
    await expect(
      (async () => {
        vi.resetModules();
        const mod = await import("../appConfig");
        global.fetch = vi
          .fn()
          .mockResolvedValue(jsonResponse({ error: "fail" }, { status: 500 }));
        return mod.fetchAppConfig();
      })(),
    ).rejects.toThrow("Server returned 500");
  });

  it("throws AppConfigError on unknown mode value", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse({ mode: "magic" }));

    await expect(fetchAppConfig()).rejects.toThrow(AppConfigError);
    await expect(
      (async () => {
        vi.resetModules();
        const mod = await import("../appConfig");
        global.fetch = vi
          .fn()
          .mockResolvedValue(jsonResponse({ mode: "magic" }));
        return mod.fetchAppConfig();
      })(),
    ).rejects.toThrow("Unknown auth mode: magic");
  });

  it("falls back to window.location.origin when oidc mode omits auth_url (same-origin proxy)", async () => {
    global.fetch = vi.fn().mockResolvedValue(jsonResponse({ mode: "oidc" }));

    const config = await fetchAppConfig();

    expect(config).toEqual({
      mode: "oidc",
      auth_url: window.location.origin,
    });
  });

  it("strips unexpected fields from open response", async () => {
    global.fetch = vi
      .fn()
      .mockResolvedValue(jsonResponse({ mode: "open", extra: "field" }));

    const config = await fetchAppConfig();

    expect(config).toEqual({ mode: "open" });
    expect(config).not.toHaveProperty("extra");
  });
});
