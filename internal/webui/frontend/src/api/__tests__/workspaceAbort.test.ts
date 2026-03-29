/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for workspace cache invalidation (workspace.ts).
 *
 * Tests that invalidateWorkspaceCache() correctly clears the module-level
 * cache, increments the generation counter, and forces subsequent
 * fetchWorkspace() calls to make fresh network requests.
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

let fetchWorkspace: typeof import("../workspace").fetchWorkspace;
let invalidateWorkspaceCache: typeof import("../workspace").invalidateWorkspaceCache;
let getCachedWorkspace: typeof import("../workspace").getCachedWorkspace;
let mockGet: ReturnType<typeof vi.fn>;

describe("invalidateWorkspaceCache", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    // Re-import after resetModules to get a fresh module with empty cache
    const clientMod = await import("../client");
    mockGet = vi.mocked(clientMod.get);

    const workspaceMod = await import("../workspace");
    fetchWorkspace = workspaceMod.fetchWorkspace;
    invalidateWorkspaceCache = workspaceMod.invalidateWorkspaceCache;
    getCachedWorkspace = workspaceMod.getCachedWorkspace;
  });

  it("clears cache so getCachedWorkspace returns null", async () => {
    const wsData = createMockWorkspaceData();
    mockGet.mockResolvedValueOnce({ success: true, data: wsData });

    // Populate the cache
    await fetchWorkspace();
    expect(getCachedWorkspace()).toEqual(wsData);

    // Invalidate
    invalidateWorkspaceCache();
    expect(getCachedWorkspace()).toBeNull();
  });

  it("forces fetchWorkspace to make a new network request after invalidation", async () => {
    const wsData1 = createMockWorkspaceData({ name: "first" });
    const wsData2 = createMockWorkspaceData({ name: "second" });
    mockGet.mockResolvedValueOnce({ success: true, data: wsData1 });
    mockGet.mockResolvedValueOnce({ success: true, data: wsData2 });

    // First fetch populates cache
    const first = await fetchWorkspace();
    expect(first.name).toBe("first");
    expect(mockGet).toHaveBeenCalledTimes(1);

    // Second fetch returns cached (no new network call)
    const cached = await fetchWorkspace();
    expect(cached.name).toBe("first");
    expect(mockGet).toHaveBeenCalledTimes(1);

    // Invalidate cache
    invalidateWorkspaceCache();

    // Next fetch must make a new network request
    const fresh = await fetchWorkspace();
    expect(fresh.name).toBe("second");
    expect(mockGet).toHaveBeenCalledTimes(2);
  });

  it("increments generation so stale in-flight responses are discarded", async () => {
    const wsData1 = createMockWorkspaceData({ name: "stale" });
    const wsData2 = createMockWorkspaceData({ name: "fresh" });

    // Set up a slow first response and a fast second response
    mockGet.mockImplementationOnce(
      () =>
        new Promise((resolve) =>
          setTimeout(() => resolve({ success: true, data: wsData1 }), 50),
        ),
    );
    mockGet.mockResolvedValueOnce({ success: true, data: wsData2 });

    // Start the first fetch (will be in-flight)
    const promise1 = fetchWorkspace();

    // Invalidate before the first fetch completes
    invalidateWorkspaceCache();

    // The first fetch should eventually resolve, but the stale data
    // should be discarded due to generation mismatch. It will recursively
    // call fetchWorkspace() which picks up the second mock.
    const result = await promise1;
    expect(result.name).toBe("fresh");
    expect(mockGet).toHaveBeenCalledTimes(2);
  });

  it("can be called multiple times without error", () => {
    invalidateWorkspaceCache();
    invalidateWorkspaceCache();
    invalidateWorkspaceCache();

    // Cache should still be null
    expect(getCachedWorkspace()).toBeNull();
  });

  it("can be called before any fetch has been made", () => {
    // Should not throw
    invalidateWorkspaceCache();
    expect(getCachedWorkspace()).toBeNull();
  });
});
