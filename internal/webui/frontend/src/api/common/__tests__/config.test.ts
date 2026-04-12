/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the backend config API functions (config.ts).
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import { getBackendConfig, updateBackendConfig } from "../config";
import type { BackendConfigData } from "../config";

// Mock the API client module
vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
  return {
    ...actual,
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

import { api } from "../client";

const mockApiGet = vi.mocked(api.GET);
const mockApiPatch = vi.mocked(api.PATCH);

function createMockConfigData(
  overrides?: Partial<BackendConfigData>,
): BackendConfigData {
  return {
    backend: "anthropic",
    source: "project",
    available: ["anthropic", "openai", "local"],
    agents: [],
    ...overrides,
  };
}

describe("getBackendConfig", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls GET /api/config/backend and unwraps response", async () => {
    const configData = createMockConfigData();
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: configData },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await getBackendConfig();

    expect(mockApiGet).toHaveBeenCalledTimes(1);
    expect(mockApiGet).toHaveBeenCalledWith("/api/config/backend");
    expect(result).toEqual(configData);
  });

  it("returns config with all fields populated", async () => {
    const configData = createMockConfigData({
      backend: "openai",
      source: "default",
      available: ["anthropic", "openai"],
      agents: [
        { worktree: "feature-a", role: "coder", backend: "anthropic" },
        { worktree: "feature-b", role: "reviewer", backend: "openai" },
      ],
    });
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: configData },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await getBackendConfig();

    expect(result.backend).toBe("openai");
    expect(result.source).toBe("default");
    expect(result.available).toEqual(["anthropic", "openai"]);
    expect(result.agents).toHaveLength(2);
    expect(result.agents[0].worktree).toBe("feature-a");
  });

  it("throws on failure response", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { success: false, error: "config not found" },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(getBackendConfig()).rejects.toThrow();
  });

  it("throws on network error from client", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { message: "Network error" },
      response: new Response(null, {
        status: 500,
        statusText: "Network error",
      }),
    } as never);

    await expect(getBackendConfig()).rejects.toThrow();
  });
});

describe("updateBackendConfig", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("calls PATCH /api/config/backend with correct body and unwraps response", async () => {
    const configData = createMockConfigData({
      backend: "openai",
      source: "project",
    });
    mockApiPatch.mockResolvedValueOnce({
      data: { success: true, data: configData },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await updateBackendConfig("openai");

    expect(mockApiPatch).toHaveBeenCalledTimes(1);
    expect(mockApiPatch).toHaveBeenCalledWith("/api/config/backend", {
      body: { backend: "openai" },
    });
    expect(result).toEqual(configData);
  });

  it("returns updated config data after successful patch", async () => {
    const configData = createMockConfigData({
      backend: "local",
      source: "project",
      available: ["anthropic", "openai", "local"],
    });
    mockApiPatch.mockResolvedValueOnce({
      data: { success: true, data: configData },
      error: undefined,
      response: new Response(),
    } as never);

    const result = await updateBackendConfig("local");

    expect(result.backend).toBe("local");
    expect(result.source).toBe("project");
  });

  it("throws on failure response", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: { success: false, error: "invalid backend" },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(updateBackendConfig("invalid")).rejects.toThrow();
  });

  it("throws on network error from client", async () => {
    mockApiPatch.mockResolvedValueOnce({
      data: undefined,
      error: { message: "Connection refused" },
      response: new Response(null, {
        status: 500,
        statusText: "Connection refused",
      }),
    } as never);

    await expect(updateBackendConfig("openai")).rejects.toThrow();
  });
});
