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
vi.mock("../client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../client")>();
  return {
    ...actual,
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    del: vi.fn(),
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
  let mockApiGet: ReturnType<typeof vi.fn>;

  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const clientMod = await import("../client");
    mockApiGet = vi.mocked(clientMod.api.GET);

    const workspaceMod = await import("../workspace");
    fetchWorkspaceApi = workspaceMod.fetchWorkspaceApi;
  });

  it("always makes a network request (no caching)", async () => {
    const wsData1 = createMockWorkspaceData({ name: "first" });
    const wsData2 = createMockWorkspaceData({ name: "second" });
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: wsData1 },
      error: undefined,
      response: new Response(),
    } as never);
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: wsData2 },
      error: undefined,
      response: new Response(),
    } as never);

    const first = await fetchWorkspaceApi();
    expect(first.name).toBe("first");
    expect(mockApiGet).toHaveBeenCalledTimes(1);

    // Second call also hits the network (no cache)
    const second = await fetchWorkspaceApi();
    expect(second.name).toBe("second");
    expect(mockApiGet).toHaveBeenCalledTimes(2);
  });

  it("calls the active workspace endpoint when no workspaceId given", async () => {
    const wsData = createMockWorkspaceData();
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: wsData },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchWorkspaceApi();

    expect(mockApiGet).toHaveBeenCalledWith("/api/workspaces/active");
  });

  it("calls the specific workspace endpoint when workspaceId given", async () => {
    const wsData = createMockWorkspaceData();
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: wsData },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchWorkspaceApi("ws-123");

    expect(mockApiGet).toHaveBeenCalledWith("/api/workspaces/{ws}", {
      params: { path: { ws: "ws-123" } },
    });
  });

  it("throws ApiError when response has error", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: undefined,
      error: { error: "Not found" },
      response: new Response(null, { status: 404, statusText: "Not Found" }),
    } as never);

    await expect(fetchWorkspaceApi()).rejects.toThrow();
  });

  it("passes workspaceId as path param", async () => {
    const wsData = createMockWorkspaceData();
    mockApiGet.mockResolvedValueOnce({
      data: { success: true, data: wsData },
      error: undefined,
      response: new Response(),
    } as never);

    await fetchWorkspaceApi("ws with spaces");

    expect(mockApiGet).toHaveBeenCalledWith("/api/workspaces/{ws}", {
      params: { path: { ws: "ws with spaces" } },
    });
  });
});
