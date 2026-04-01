/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspace hook.
 * Follows useAgents test pattern: mock API, fake timers, test polling and error handling.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchWorkspaceApi } from "@/api/workspace";
import type { WorkspaceData } from "@/api/workspace";

import { useWorkspace } from "../useWorkspace";

// Mock the workspace API functions
vi.mock("@/api/workspace", () => ({
  fetchWorkspaceApi: vi.fn(),
}));
const mockFetchWorkspaceApi = vi.mocked(fetchWorkspaceApi);

/**
 * Helper to create a mock WorkspaceData.
 */
function createMockWorkspace(
  overrides?: Partial<WorkspaceData>,
): WorkspaceData {
  return {
    name: "test-workspace",
    path: "/home/user/workspace",
    repos: [
      {
        name: "api",
        path: "/home/user/workspace/api",
        default_branch: "main",
        remote: "origin",
        source_repo_id: "repo-1",
        groups: ["backend"],
      },
      {
        name: "frontend",
        path: "/home/user/workspace/frontend",
        default_branch: "main",
        remote: "origin",
        source_repo_id: "repo-2",
        groups: ["frontend"],
      },
    ],
    groups: ["backend", "frontend"],
    agents: [
      {
        name: "nova",
        repos: ["api"],
        repo_groups: ["backend"],
        cross_repo: false,
      },
      {
        name: "falcon",
        repos: ["api", "frontend"],
        repo_groups: ["backend", "frontend"],
        cross_repo: true,
      },
    ],
    workspaces: [
      {
        name: "test-workspace",
        path: "/home/user/workspace",
        active: true,
        repo_count: 2,
      },
    ],
    ...overrides,
  };
}

/**
 * Helper to flush pending promises when using fake timers.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useWorkspace", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    mockFetchWorkspaceApi.mockReset();
    mockFetchWorkspaceApi.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  describe("initial fetch", () => {
    it("returns repos/groups/agents on successful fetch", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.workspace).toEqual(workspace);
      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("api");
      expect(result.current.repos[1].name).toBe("frontend");
      expect(result.current.groups).toEqual(["backend", "frontend"]);
      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe("nova");
      expect(result.current.agents[1].name).toBe("falcon");
      expect(result.current.error).toBeNull();
    });

    it("sets isLoading during initial fetch", async () => {
      mockFetchWorkspaceApi.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      // Should be loading before fetch resolves
      expect(result.current.isLoading).toBe(true);
      expect(result.current.workspace).toBeNull();
      expect(result.current.repos).toEqual([]);
      expect(result.current.groups).toEqual([]);
      expect(result.current.agents).toEqual([]);
    });

    it("sets isLoading to false after fetch resolves", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
    });

    it("sets isLoading to false after fetch rejects", async () => {
      mockFetchWorkspaceApi.mockRejectedValueOnce(new Error("Network error"));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBe("Network error");
    });

    it("sets error message from Error instance", async () => {
      mockFetchWorkspaceApi.mockRejectedValueOnce(
        new Error("Failed to connect"),
      );

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.error).toBe("Failed to connect");
    });

    it("sets default error message for non-Error exceptions", async () => {
      mockFetchWorkspaceApi.mockRejectedValueOnce("some string error");

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.error).toBe("Failed to load workspace data");
    });
  });

  describe("stale data on error", () => {
    it("keeps stale data on subsequent fetch errors", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 5000 }));

      // Initial fetch succeeds
      await flushPromises();
      expect(result.current.workspace).toEqual(workspace);
      expect(result.current.repos).toHaveLength(2);

      // Next poll fails
      mockFetchWorkspaceApi.mockRejectedValueOnce(
        new Error("Server unavailable"),
      );

      await act(async () => {
        vi.advanceTimersByTime(5000);
      });
      await flushPromises();

      // Stale data should be preserved
      expect(result.current.workspace).toEqual(workspace);
      expect(result.current.repos).toHaveLength(2);
      expect(result.current.error).toBe("Server unavailable");
    });
  });

  describe("polling", () => {
    it("calls refetch on interval", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      renderHook(() => useWorkspace({ pollInterval: 10000 }));

      // Initial fetch
      await flushPromises();
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Subsequent polls call fetchWorkspaceApi again
      const updatedWorkspace = createMockWorkspace({
        name: "updated-workspace",
      });
      mockFetchWorkspaceApi.mockResolvedValueOnce(updatedWorkspace);

      await act(async () => {
        vi.advanceTimersByTime(10000);
      });
      await flushPromises();

      // 1 initial + 1 poll = 2 total
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);
    });

    it("does not poll when pollInterval is 0", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Advance time - should not trigger additional fetches
      await act(async () => {
        vi.advanceTimersByTime(120000);
      });
      await flushPromises();

      // Still only the initial call
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);
    });

    it("uses default poll interval of 60000ms", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      renderHook(() => useWorkspace());

      await flushPromises();
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      const updatedWorkspace = createMockWorkspace({
        name: "updated",
      });
      mockFetchWorkspaceApi.mockResolvedValueOnce(updatedWorkspace);

      // Advance 59 seconds - should not poll yet
      await act(async () => {
        vi.advanceTimersByTime(59000);
      });
      await flushPromises();

      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(1);

      // Advance 1 more second (total 60s) - should poll
      await act(async () => {
        vi.advanceTimersByTime(1000);
      });
      await flushPromises();

      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);
    });
  });

  describe("cleanup", () => {
    it("cleans up interval on unmount", async () => {
      const clearIntervalSpy = vi.spyOn(globalThis, "clearInterval");
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { unmount } = renderHook(() =>
        useWorkspace({ pollInterval: 10000 }),
      );

      await flushPromises();

      clearIntervalSpy.mockClear();

      unmount();

      expect(clearIntervalSpy).toHaveBeenCalled();

      clearIntervalSpy.mockRestore();
    });

    it("does not update state after unmount", async () => {
      let resolvePromise: (value: WorkspaceData) => void;
      const pendingPromise = new Promise<WorkspaceData>((resolve) => {
        resolvePromise = resolve;
      });
      mockFetchWorkspaceApi.mockReturnValueOnce(pendingPromise);

      const { result, unmount } = renderHook(() =>
        useWorkspace({ pollInterval: 0 }),
      );

      expect(result.current.isLoading).toBe(true);

      // Unmount before the promise resolves
      unmount();

      // Resolve the promise after unmount - should not throw or update state
      await act(async () => {
        resolvePromise!(createMockWorkspace());
      });
      await flushPromises();

      // No error means mountedRef prevented the state update
    });
  });

  describe("refetch", () => {
    it("refetch calls fetchWorkspaceApi (fresh network request)", async () => {
      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      const updatedWorkspace = createMockWorkspace({
        name: "refreshed-workspace",
      });
      mockFetchWorkspaceApi.mockResolvedValueOnce(updatedWorkspace);

      await act(async () => {
        result.current.refetch();
      });
      await flushPromises();

      // 1 initial + 1 refetch = 2 total
      expect(mockFetchWorkspaceApi).toHaveBeenCalledTimes(2);
      expect(result.current.workspace?.name).toBe("refreshed-workspace");
    });

    it("clears error on successful refetch", async () => {
      mockFetchWorkspaceApi.mockRejectedValueOnce(new Error("Initial error"));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();
      expect(result.current.error).toBe("Initial error");

      const workspace = createMockWorkspace();
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      await act(async () => {
        result.current.refetch();
      });
      await flushPromises();

      expect(result.current.error).toBeNull();
      expect(result.current.workspace).toEqual(workspace);
    });
  });

  describe("convenience aliases", () => {
    it("repos returns empty array when workspace is null", () => {
      mockFetchWorkspaceApi.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      expect(result.current.repos).toEqual([]);
    });

    it("groups returns empty array when workspace is null", () => {
      mockFetchWorkspaceApi.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      expect(result.current.groups).toEqual([]);
    });

    it("agents returns empty array when workspace is null", () => {
      mockFetchWorkspaceApi.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      expect(result.current.agents).toEqual([]);
    });

    it("groups returns empty array when workspace.groups is undefined", async () => {
      const workspace = createMockWorkspace({ groups: undefined });
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.groups).toEqual([]);
    });

    it("agents returns empty array when workspace.agents is undefined", async () => {
      const workspace = createMockWorkspace({ agents: undefined });
      mockFetchWorkspaceApi.mockResolvedValueOnce(workspace);

      const { result } = renderHook(() => useWorkspace({ pollInterval: 0 }));

      await flushPromises();

      expect(result.current.agents).toEqual([]);
    });
  });
});
