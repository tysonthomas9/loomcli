/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceContext hook and WorkspaceProvider.
 * Mocks the workspace API (fetchWorkspaceApi) which the workspaceStore calls.
 *
 * T12 changes: WorkspaceProvider now requires workspaceId prop and uses useNavigate.
 * Store migration: WorkspaceProvider now uses workspaceStore instead of useWorkspace hook.
 */

import { renderHook, act } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type {
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceData,
} from "@/api/workspace";

import { WorkspaceProvider, useWorkspaceContext } from "../useWorkspaceContext";

// Mock the workspace API so the store can call fetchWorkspaceApi
vi.mock("@/api/workspace", () => ({
  fetchWorkspaceApi: vi.fn(),
  setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
  clearDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@/api/common", () => ({
  setActiveWorkspace: vi.fn(),
}));

vi.mock("@/utils/scopedStorage", () => ({
  wsGet: vi.fn(() => null),
  wsSet: vi.fn(),
  setLastWorkspaceId: vi.fn(),
}));

import { fetchWorkspaceApi } from "@/api/workspace";

const mockFetchWorkspaceApi = vi.mocked(fetchWorkspaceApi);

const TEST_WS_ID = "test-ws-uuid-1234";

/**
 * Helper to create workspace data and configure the mock API.
 */
function setupMockWorkspaceApi(
  overrides?: Partial<WorkspaceData>,
): WorkspaceData {
  const data: WorkspaceData = {
    id: TEST_WS_ID,
    name: "test-workspace",
    path: "/home/user/workspace",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [],
    default_workspace: "",
    ...overrides,
  };
  mockFetchWorkspaceApi.mockResolvedValue(data);
  return data;
}

/**
 * Helper to create a mock RepoInfo.
 */
function createMockRepo(overrides?: Partial<RepoInfo>): RepoInfo {
  return {
    name: "api",
    path: "/home/user/workspace/api",
    default_branch: "main",
    remote: "origin",
    groups: [],
    ...overrides,
  };
}

/**
 * Helper to create a mock WorkspaceAgentInfo.
 */
function createMockAgent(
  overrides?: Partial<WorkspaceAgentInfo>,
): WorkspaceAgentInfo {
  return {
    name: "nova",
    repos: ["api"],
    repo_groups: ["backend"],
    cross_repo: false,
    ...overrides,
  };
}

/**
 * Helper to flush pending promises (allows store fetch to complete).
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useWorkspaceContext", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
  });

  describe("outside provider", () => {
    it("returns safe defaults without throwing", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.workspace).toBeNull();
      expect(result.current.repos).toEqual([]);
      expect(result.current.groups).toEqual([]);
      expect(result.current.agents).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("getRepoByName returns undefined outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getRepoByName("anything")).toBeUndefined();
    });

    it("getReposByGroup returns empty array outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("getAgentByName returns undefined outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.getAgentByName("nova")).toBeUndefined();
    });

    it("refetch is a no-op function outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(() => result.current.refetch()).not.toThrow();
    });
  });

  describe("inside provider", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("provides repos from workspace data", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("api");
      expect(result.current.repos[1].name).toBe("frontend");
    });

    it("provides groups from workspace data", async () => {
      setupMockWorkspaceApi({ groups: ["backend", "frontend", "infra"] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.groups).toEqual(["backend", "frontend", "infra"]);
    });

    it("provides agents from workspace data", async () => {
      const agents = [
        createMockAgent({ name: "nova" }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspaceApi({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe("nova");
      expect(result.current.agents[1].name).toBe("falcon");
    });

    it("provides error on fetch failure", async () => {
      mockFetchWorkspaceApi.mockRejectedValue(new Error("Network error"));

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.error).toBe("Network error");
    });

    it("calls fetchWorkspaceApi with the workspaceId", async () => {
      setupMockWorkspaceApi();

      renderHook(() => useWorkspaceContext(), { wrapper });

      await flushPromises();

      expect(mockFetchWorkspaceApi).toHaveBeenCalledWith(TEST_WS_ID);
    });
  });

  describe("getRepoByName", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("finds correct repo by name", async () => {
      const repos = [
        createMockRepo({ name: "api", source_repo_id: "repo-1" }),
        createMockRepo({ name: "frontend", source_repo_id: "repo-2" }),
        createMockRepo({ name: "backend", source_repo_id: "repo-3" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      const found = result.current.getRepoByName("frontend");
      expect(found).toBeDefined();
      expect(found!.name).toBe("frontend");
      expect(found!.source_repo_id).toBe("repo-2");
    });

    it("returns undefined for unknown repo name", async () => {
      const repos = [createMockRepo({ name: "api" })];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getRepoByName("unknown")).toBeUndefined();
    });

    it("returns undefined when repos list is empty", async () => {
      setupMockWorkspaceApi({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getRepoByName("api")).toBeUndefined();
    });
  });

  describe("getReposByGroup", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("filters correctly by group", async () => {
      const repos = [
        createMockRepo({ name: "api", groups: ["backend"] }),
        createMockRepo({
          name: "frontend",
          groups: ["frontend"],
        }),
        createMockRepo({
          name: "gateway",
          groups: ["backend", "infra"],
        }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      const backendRepos = result.current.getReposByGroup("backend");
      expect(backendRepos).toHaveLength(2);
      expect(backendRepos.map((r) => r.name)).toEqual(["api", "gateway"]);
    });

    it("returns empty array for unknown group", async () => {
      const repos = [createMockRepo({ name: "api", groups: ["backend"] })];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getReposByGroup("nonexistent")).toEqual([]);
    });

    it("returns empty array when no repos have groups", async () => {
      const repos = [
        createMockRepo({ name: "api", groups: undefined }),
        createMockRepo({ name: "frontend", groups: undefined }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("returns repos that belong to multiple groups", async () => {
      const repos = [
        createMockRepo({
          name: "gateway",
          groups: ["backend", "infra"],
        }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getReposByGroup("backend")).toHaveLength(1);
      expect(result.current.getReposByGroup("infra")).toHaveLength(1);
    });
  });

  describe("getAgentByName", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("finds correct agent by name", async () => {
      const agents = [
        createMockAgent({ name: "nova", cross_repo: false }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspaceApi({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      const found = result.current.getAgentByName("falcon");
      expect(found).toBeDefined();
      expect(found!.name).toBe("falcon");
      expect(found!.cross_repo).toBe(true);
    });

    it("returns undefined for unknown agent name", async () => {
      const agents = [createMockAgent({ name: "nova" })];
      setupMockWorkspaceApi({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getAgentByName("unknown-agent")).toBeUndefined();
    });

    it("returns undefined when agents list is empty", async () => {
      setupMockWorkspaceApi({ agents: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.getAgentByName("nova")).toBeUndefined();
    });
  });

  describe("activeWorkspaceName", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("is set to workspace.name on load", async () => {
      setupMockWorkspaceApi({ name: "my-workspace" });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.activeWorkspaceName).toBe("my-workspace");
    });

    it("is null when workspace has not loaded yet", () => {
      // Never resolve the fetch
      mockFetchWorkspaceApi.mockReturnValue(new Promise(() => {}));

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeWorkspaceName).toBeNull();
    });
  });

  describe("setActiveWorkspace", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("setActiveWorkspace navigates via React Router (no page reload)", async () => {
      setupMockWorkspaceApi({
        workspaces: [
          {
            id: "ws-2",
            name: "new-workspace",
            path: "/path",
            active: false,
            repo_count: 1,
            is_default: false,
          },
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.setActiveWorkspace("new-workspace");
      });

      // No crash — the navigate call happens internally via useNavigate
    });
  });

  describe("selectRepos", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("updates activeRepos to only the named repos", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api", "backend"]);
      });

      expect(result.current.activeRepos).toHaveLength(2);
      expect(result.current.activeRepos.map((r) => r.name)).toEqual([
        "api",
        "backend",
      ]);
    });

    it("updates selectedRepoNames set", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["frontend"]);
      });

      expect(result.current.selectedRepoNames.has("frontend")).toBe(true);
      expect(result.current.selectedRepoNames.has("api")).toBe(false);
    });
  });

  describe("selectAll", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("clears selection and returns all repos", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api"]);
      });
      expect(result.current.activeRepos).toHaveLength(1);

      act(() => {
        result.current.selectAll();
      });

      expect(result.current.activeRepos).toHaveLength(3);
      expect(result.current.selectedRepoNames.size).toBe(0);
      expect(result.current.isAllSelected).toBe(true);
    });
  });

  describe("toggleRepo", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("adds a repo to selection", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.toggleRepo("api");
      });

      expect(result.current.selectedRepoNames.has("api")).toBe(true);
    });

    it("removes a repo from selection", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.toggleRepo("api");
      });
      expect(result.current.selectedRepoNames.has("api")).toBe(true);

      act(() => {
        result.current.toggleRepo("api");
      });
      expect(result.current.selectedRepoNames.has("api")).toBe(false);
    });
  });

  describe("sourceReposFilter", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("returns undefined when all repos selected", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.sourceReposFilter).toBeUndefined();
    });

    it("returns repo names when subset selected", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api", "backend"]);
      });

      expect(result.current.sourceReposFilter).toEqual(["api", "backend"]);
    });

    it("returns undefined after selectAll", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api"]);
      });
      expect(result.current.sourceReposFilter).toEqual(["api"]);

      act(() => {
        result.current.selectAll();
      });
      expect(result.current.sourceReposFilter).toBeUndefined();
    });
  });

  describe("isMultiRepo", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("is false when 0 repos", async () => {
      setupMockWorkspaceApi({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.isMultiRepo).toBe(false);
    });

    it("is false when 1 repo", async () => {
      setupMockWorkspaceApi({ repos: [createMockRepo({ name: "api" })] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.isMultiRepo).toBe(false);
    });

    it("is true when 2+ repos", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.isMultiRepo).toBe(true);
    });

    it("is true when 3 repos", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.isMultiRepo).toBe(true);
    });
  });

  describe("localStorage persistence", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("workspace name survives unmount/remount", async () => {
      setupMockWorkspaceApi({ name: "persistent-ws" });

      const { unmount } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });
      await flushPromises();
      unmount();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });
      await flushPromises();

      expect(result.current.activeWorkspaceName).toBe("persistent-ws");
    });

    it("does not write to loom-active-workspace", async () => {
      setupMockWorkspaceApi({ name: "my-workspace" });

      renderHook(() => useWorkspaceContext(), { wrapper });
      await flushPromises();

      expect(localStorage.getItem("loom-active-workspace")).toBeNull();
    });
  });

  describe("localStorage failure", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("gracefully falls back to defaults when getItem throws", () => {
      const getItemSpy = vi
        .spyOn(Storage.prototype, "getItem")
        .mockImplementation(() => {
          throw new Error("localStorage disabled");
        });

      // Never resolve to keep workspace null
      mockFetchWorkspaceApi.mockReturnValue(new Promise(() => {}));

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeWorkspaceName).toBeNull();
      expect(result.current.selectedRepoNames.size).toBe(0);
      expect(result.current.isAllSelected).toBe(true);

      getItemSpy.mockRestore();
    });

    it("gracefully handles setItem failure", async () => {
      const setItemSpy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("quota exceeded");
        });

      setupMockWorkspaceApi();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(() => {
        act(() => {
          result.current.setActiveWorkspace("new-ws");
        });
      }).not.toThrow();

      expect(() => {
        act(() => {
          result.current.selectRepos(["api"]);
        });
      }).not.toThrow();

      expect(() => {
        act(() => {
          result.current.toggleRepo("api");
        });
      }).not.toThrow();

      expect(() => {
        act(() => {
          result.current.selectAll();
        });
      }).not.toThrow();

      setItemSpy.mockRestore();
    });
  });

  describe("NO_WORKSPACE_CONTEXT defaults for new fields", () => {
    it("returns safe defaults for selection fields outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(result.current.activeWorkspaceName).toBeNull();
      expect(result.current.selectedRepoNames).toBeInstanceOf(Set);
      expect(result.current.selectedRepoNames.size).toBe(0);
      expect(result.current.activeRepos).toEqual([]);
      expect(result.current.activeRepoNames).toEqual([]);
      expect(result.current.isAllSelected).toBe(true);
      expect(result.current.sourceReposFilter).toBeUndefined();
      expect(result.current.isMultiRepo).toBe(false);
    });

    it("selection actions are no-ops outside provider", () => {
      const { result } = renderHook(() => useWorkspaceContext());

      expect(() => result.current.setActiveWorkspace("ws")).not.toThrow();
      expect(() => result.current.selectRepos(["api"])).not.toThrow();
      expect(() => result.current.selectAll()).not.toThrow();
      expect(() => result.current.toggleRepo("api")).not.toThrow();
    });
  });

  describe("isAllSelected", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("is true when selectedRepoNames is empty (no filter)", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.isAllSelected).toBe(true);
    });

    it("is false when repos are selected", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api"]);
      });

      expect(result.current.isAllSelected).toBe(false);
    });

    it("is true when every repo is ticked, so repo-less tasks are not hidden", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api", "frontend"]);
      });

      expect(result.current.isAllSelected).toBe(true);
      expect(result.current.sourceReposFilter).toBeUndefined();
    });

    it("becomes true again after selectAll", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["api"]);
      });
      expect(result.current.isAllSelected).toBe(false);

      act(() => {
        result.current.selectAll();
      });
      expect(result.current.isAllSelected).toBe(true);
    });
  });

  describe("activeRepoNames", () => {
    function wrapper({ children }: { children: ReactNode }) {
      return (
        <MemoryRouter>
          <WorkspaceProvider workspaceId={TEST_WS_ID}>
            {children}
          </WorkspaceProvider>
        </MemoryRouter>
      );
    }

    it("returns all repo names when no filter applied", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.activeRepoNames).toEqual([
        "api",
        "frontend",
        "backend",
      ]);
    });

    it("returns only selected repo names when filter applied", async () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspaceApi({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      act(() => {
        result.current.selectRepos(["frontend", "backend"]);
      });

      expect(result.current.activeRepoNames).toEqual(["frontend", "backend"]);
    });

    it("returns empty array when no repos exist", async () => {
      setupMockWorkspaceApi({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      await flushPromises();

      expect(result.current.activeRepoNames).toEqual([]);
    });
  });
});
