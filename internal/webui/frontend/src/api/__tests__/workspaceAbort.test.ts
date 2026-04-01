/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for fetchWorkspaceApi (workspace.ts).
 *
 * Tests that fetchWorkspaceApi always makes a fresh network request
 * (no module-level caching). The caching/deduplication responsibility
 * has moved to the useWorkspace hook.
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

import type { WorkspaceData } from "../workspace";

// Mock the API client module
vi.mock("../client", () => ({
  get: vi.fn(),
  post: vi.fn(),
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
    id: "ws-1",
    name: "test-ws",
    path: "/tmp/test",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: "ws-1",
        name: "alpha",
        path: "/tmp/alpha",
        active: true,
        repo_count: 1,
        is_default: false,
      },
    ],
    default_workspace: "alpha",
    ...overrides,
  };
}

describe("fetchWorkspaceApi", () => {
  let fetchWorkspaceApi: typeof import("../workspace").fetchWorkspaceApi;
  let mockGet: ReturnType<typeof vi.fn>;

  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const clientMod = await import("../client");
    mockGet = vi.mocked(clientMod.get);

    const workspaceMod = await import("../workspace");
    fetchWorkspaceApi = workspaceMod.fetchWorkspaceApi;
  });

  it("always makes a network request (no caching)", async () => {
    const wsData1 = createMockWorkspaceData({ name: "first" });
    const wsData2 = createMockWorkspaceData({ name: "second" });
    mockGet.mockResolvedValueOnce({ success: true, data: wsData1 });
    mockGet.mockResolvedValueOnce({ success: true, data: wsData2 });

    const first = await fetchWorkspaceApi();
    expect(first.name).toBe("first");
    expect(mockGet).toHaveBeenCalledTimes(1);

    // Second call also hits the network (no cache)
    const second = await fetchWorkspaceApi();
    expect(second.name).toBe("second");
    expect(mockGet).toHaveBeenCalledTimes(2);
  });

  it("calls the active workspace endpoint when no workspaceId given", async () => {
    const wsData = createMockWorkspaceData();
    mockGet.mockResolvedValueOnce({ success: true, data: wsData });

    await fetchWorkspaceApi();

    expect(mockGet).toHaveBeenCalledWith("/api/workspaces/active");
  });

  it("calls the specific workspace endpoint when workspaceId given", async () => {
    const wsData = createMockWorkspaceData();
    mockGet.mockResolvedValueOnce({ success: true, data: wsData });

    await fetchWorkspaceApi("ws-123");

    expect(mockGet).toHaveBeenCalledWith("/api/workspaces/ws-123");
  });

  it("throws ApiError when response is unsuccessful", async () => {
    mockGet.mockResolvedValueOnce({ success: false, error: "Not found" });

    await expect(fetchWorkspaceApi()).rejects.toThrow("Not found");
  });

  it("encodes special characters in workspaceId", async () => {
    const wsData = createMockWorkspaceData();
    mockGet.mockResolvedValueOnce({ success: true, data: wsData });

    await fetchWorkspaceApi("ws with spaces");

    expect(mockGet).toHaveBeenCalledWith("/api/workspaces/ws%20with%20spaces");
  });
});
