/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the reorderWorkspaces API function (workspace.ts).
 *
 * Because workspace.ts uses a module-level cache, we use
 * vi.resetModules() + dynamic import in beforeEach to get a fresh module
 * instance for each test.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import type { WorkspaceData } from "../workspace";

// Mock the API client module
vi.mock("../client", () => ({
  get: vi.fn(),
  patch: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  ApiError: class extends Error {
    status: number;
    statusText: string;
    constructor(status: number, statusText: string) {
      super(`API Error: ${status} ${statusText}`);
      this.name = "ApiError";
      this.status = status;
      this.statusText = statusText;
    }
  },
}));

function createMockWorkspaceData(
  overrides?: Partial<WorkspaceData>,
): WorkspaceData {
  return {
    name: "test-ws",
    path: "/tmp/test",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      { name: "alpha", path: "/tmp/alpha", active: true, repo_count: 1 },
      { name: "beta", path: "/tmp/beta", active: false, repo_count: 2 },
    ],
    workspace_order: ["beta", "alpha"],
    ...overrides,
  };
}

let reorderWorkspaces: typeof import("../workspace").reorderWorkspaces;
let mockPut: ReturnType<typeof vi.fn>;

describe("reorderWorkspaces", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    // Re-import after resetModules to get a fresh module with empty cache
    const clientMod = await import("../client");
    mockPut = vi.mocked(clientMod.put);

    const workspaceMod = await import("../workspace");
    reorderWorkspaces = workspaceMod.reorderWorkspaces;
  });

  it("calls PUT /api/workspaces/order with correct payload", async () => {
    const wsData = createMockWorkspaceData();
    mockPut.mockResolvedValueOnce({ success: true, data: wsData });

    await reorderWorkspaces(["beta", "alpha"]);

    expect(mockPut).toHaveBeenCalledTimes(1);
    expect(mockPut).toHaveBeenCalledWith("/api/workspaces/order", {
      order: ["beta", "alpha"],
    });
  });

  it("returns unwrapped workspace data on success", async () => {
    const wsData = createMockWorkspaceData({
      workspace_order: ["gamma", "alpha", "beta"],
    });
    mockPut.mockResolvedValueOnce({ success: true, data: wsData });

    const result = await reorderWorkspaces(["gamma", "alpha", "beta"]);

    expect(result).toEqual(wsData);
    expect(result.workspace_order).toEqual(["gamma", "alpha", "beta"]);
    expect(result.name).toBe("test-ws");
  });

  it("throws on error response from API", async () => {
    mockPut.mockResolvedValueOnce({
      success: false,
      error: "no config found",
    });

    await expect(reorderWorkspaces(["alpha"])).rejects.toThrow(
      "no config found",
    );
  });

  it("throws on network error from client", async () => {
    mockPut.mockRejectedValueOnce(new Error("Network error"));

    await expect(reorderWorkspaces(["alpha"])).rejects.toThrow("Network error");
  });

  it("sends empty array to clear custom ordering", async () => {
    const wsData = createMockWorkspaceData({ workspace_order: undefined });
    mockPut.mockResolvedValueOnce({ success: true, data: wsData });

    const result = await reorderWorkspaces([]);

    expect(mockPut).toHaveBeenCalledWith("/api/workspaces/order", {
      order: [],
    });
    expect(result).toEqual(wsData);
  });
});
