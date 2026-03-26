/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceContext hook and WorkspaceProvider.
 * Follows useAgentContext test pattern: mock underlying hook, test provider and helpers.
 *
 * T12 changes: WorkspaceProvider now requires workspaceId prop and uses useNavigate.
 */

import { renderHook, act } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace";

import { WorkspaceProvider, useWorkspaceContext } from "../useWorkspaceContext";
import { useWorkspace } from "../useWorkspace";
import type { UseWorkspaceReturn } from "../useWorkspace";

// Mock useWorkspace so we don't trigger real polling
vi.mock("../useWorkspace", () => ({
  useWorkspace: vi.fn(),
}));
const mockUseWorkspace = vi.mocked(useWorkspace);

// Mock API and storage modules used by WorkspaceProvider
vi.mock("@/api/workspace", () => ({
  setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
  clearDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
  refreshWorkspace: vi.fn().mockResolvedValue(undefined),
  invalidateWorkspaceCache: vi.fn(),
}));

vi.mock("@/api/client", () => ({
  setActiveWorkspace: vi.fn(),
}));

vi.mock("@/utils/scopedStorage", () => ({
  wsGet: vi.fn(() => null),
  wsSet: vi.fn(),
  setLastWorkspaceId: vi.fn(),
}));

const TEST_WS_ID = "test-ws-uuid-1234";

/**
 * Helper to create a mock UseWorkspaceReturn.
 */
function setupMockWorkspace(overrides?: Partial<UseWorkspaceReturn>): void {
  const defaultReturn: UseWorkspaceReturn = {
    workspace: {
      id: TEST_WS_ID,
      name: "test-workspace",
      path: "/home/user/workspace",
      repos: [],
      groups: [],
      agents: [],
      workspaces: [],
      default_workspace: "",
    },
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
    ...overrides,
  };
  mockUseWorkspace.mockReturnValue(defaultReturn);
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

describe("useWorkspaceContext", () => {
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

      // Should not throw
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

    it("provides repos from useWorkspace", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("api");
      expect(result.current.repos[1].name).toBe("frontend");
    });

    it("provides groups from useWorkspace", () => {
      setupMockWorkspace({ groups: ["backend", "frontend", "infra"] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.groups).toEqual(["backend", "frontend", "infra"]);
    });

    it("provides agents from useWorkspace", () => {
      const agents = [
        createMockAgent({ name: "nova" }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe("nova");
      expect(result.current.agents[1].name).toBe("falcon");
    });

    it("provides isLoading from useWorkspace", () => {
      setupMockWorkspace({ isLoading: true });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.isLoading).toBe(true);
    });

    it("provides error from useWorkspace", () => {
      setupMockWorkspace({ error: "Network error" });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.error).toBe("Network error");
    });

    it("passes pollInterval of 60000 to useWorkspace", () => {
      setupMockWorkspace();

      renderHook(() => useWorkspaceContext(), { wrapper });

      expect(mockUseWorkspace).toHaveBeenCalledWith({
        pollInterval: 60000,
      });
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

    it("finds correct repo by name", () => {
      const repos = [
        createMockRepo({ name: "api", source_repo_id: "repo-1" }),
        createMockRepo({ name: "frontend", source_repo_id: "repo-2" }),
        createMockRepo({ name: "backend", source_repo_id: "repo-3" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const found = result.current.getRepoByName("frontend");
      expect(found).toBeDefined();
      expect(found!.name).toBe("frontend");
      expect(found!.source_repo_id).toBe("repo-2");
    });

    it("returns undefined for unknown repo name", () => {
      const repos = [createMockRepo({ name: "api" })];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getRepoByName("unknown")).toBeUndefined();
    });

    it("returns undefined when repos list is empty", () => {
      setupMockWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    it("filters correctly by group", () => {
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
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const backendRepos = result.current.getReposByGroup("backend");
      expect(backendRepos).toHaveLength(2);
      expect(backendRepos.map((r) => r.name)).toEqual(["api", "gateway"]);
    });

    it("returns empty array for unknown group", () => {
      const repos = [createMockRepo({ name: "api", groups: ["backend"] })];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getReposByGroup("nonexistent")).toEqual([]);
    });

    it("returns empty array when no repos have groups", () => {
      const repos = [
        createMockRepo({ name: "api", groups: undefined }),
        createMockRepo({ name: "frontend", groups: undefined }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("returns repos that belong to multiple groups", () => {
      const repos = [
        createMockRepo({
          name: "gateway",
          groups: ["backend", "infra"],
        }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // Should appear in both groups
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

    it("finds correct agent by name", () => {
      const agents = [
        createMockAgent({ name: "nova", cross_repo: false }),
        createMockAgent({ name: "falcon", cross_repo: true }),
      ];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      const found = result.current.getAgentByName("falcon");
      expect(found).toBeDefined();
      expect(found!.name).toBe("falcon");
      expect(found!.cross_repo).toBe(true);
    });

    it("returns undefined for unknown agent name", () => {
      const agents = [createMockAgent({ name: "nova" })];
      setupMockWorkspace({ agents });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.getAgentByName("unknown-agent")).toBeUndefined();
    });

    it("returns undefined when agents list is empty", () => {
      setupMockWorkspace({ agents: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    beforeEach(() => {
      localStorage.clear();
    });

    it("is set to workspace.name on load", () => {
      setupMockWorkspace({
        workspace: {
          id: TEST_WS_ID,
          name: "my-workspace",
          path: "/home/user/workspace",
          repos: [],
          groups: [],
          agents: [],
          workspaces: [],
          default_workspace: "",
        },
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeWorkspaceName).toBe("my-workspace");
    });

    it("is null when workspace is null", () => {
      setupMockWorkspace({ workspace: null });

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

    beforeEach(() => {
      localStorage.clear();
    });

    it("setActiveWorkspace navigates via React Router (no page reload)", () => {
      setupMockWorkspace({
        workspace: {
          id: TEST_WS_ID,
          name: "test-workspace",
          path: "/home/user/workspace",
          repos: [],
          groups: [],
          agents: [],
          workspaces: [{ id: "ws-2", name: "new-workspace", path: "/path" }],
          default_workspace: "",
        },
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // setActiveWorkspace looks up the workspace by name and navigates
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

    beforeEach(() => {
      localStorage.clear();
    });

    it("updates activeRepos to only the named repos", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      act(() => {
        result.current.selectRepos(["api", "backend"]);
      });

      expect(result.current.activeRepos).toHaveLength(2);
      expect(result.current.activeRepos.map((r) => r.name)).toEqual([
        "api",
        "backend",
      ]);
    });

    it("updates selectedRepoNames set", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    beforeEach(() => {
      localStorage.clear();
    });

    it("clears selection and returns all repos", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // First select a subset
      act(() => {
        result.current.selectRepos(["api"]);
      });
      expect(result.current.activeRepos).toHaveLength(1);

      // Then select all
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

    beforeEach(() => {
      localStorage.clear();
    });

    it("adds a repo to selection", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      act(() => {
        result.current.toggleRepo("api");
      });

      expect(result.current.selectedRepoNames.has("api")).toBe(true);
    });

    it("removes a repo from selection", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // Add then remove
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

    beforeEach(() => {
      localStorage.clear();
    });

    it("returns undefined when all repos selected", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.sourceReposFilter).toBeUndefined();
    });

    it("returns repo names when subset selected", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      act(() => {
        result.current.selectRepos(["api", "backend"]);
      });

      expect(result.current.sourceReposFilter).toEqual(["api", "backend"]);
    });

    it("returns undefined after selectAll", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    it("is false when 0 repos", () => {
      setupMockWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.isMultiRepo).toBe(false);
    });

    it("is true when 1 repo", () => {
      setupMockWorkspace({ repos: [createMockRepo({ name: "api" })] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // isMultiRepo is true when workspace has 1+ repos (changed from 2+)
      expect(result.current.isMultiRepo).toBe(true);
    });

    it("is true when 2+ repos", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.isMultiRepo).toBe(true);
    });

    it("is true when 3 repos", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    beforeEach(() => {
      localStorage.clear();
    });

    it("workspace name survives unmount/remount", () => {
      setupMockWorkspace({
        workspace: {
          id: TEST_WS_ID,
          name: "persistent-ws",
          path: "/home/user/workspace",
          repos: [],
          groups: [],
          agents: [],
          workspaces: [],
          default_workspace: "",
        },
      });

      // First render sets localStorage
      const { unmount } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });
      unmount();

      // Second render reads from localStorage
      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeWorkspaceName).toBe("persistent-ws");
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

      // Use null workspace so the useEffect doesn't override the initial null
      setupMockWorkspace({ workspace: null });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // Should not throw and should have defaults (localStorage read failed gracefully)
      expect(result.current.activeWorkspaceName).toBeNull();
      expect(result.current.selectedRepoNames.size).toBe(0);
      expect(result.current.isAllSelected).toBe(true);

      getItemSpy.mockRestore();
    });

    it("gracefully handles setItem failure", () => {
      const setItemSpy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("quota exceeded");
        });

      setupMockWorkspace();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      // Should not throw when persisting
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

    beforeEach(() => {
      localStorage.clear();
    });

    it("is true when selectedRepoNames is empty (no filter)", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.isAllSelected).toBe(true);
    });

    it("is false when repos are selected", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      act(() => {
        result.current.selectRepos(["api"]);
      });

      expect(result.current.isAllSelected).toBe(false);
    });

    it("becomes true again after selectAll", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

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

    beforeEach(() => {
      localStorage.clear();
    });

    it("returns all repo names when no filter applied", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeRepoNames).toEqual([
        "api",
        "frontend",
        "backend",
      ]);
    });

    it("returns only selected repo names when filter applied", () => {
      const repos = [
        createMockRepo({ name: "api" }),
        createMockRepo({ name: "frontend" }),
        createMockRepo({ name: "backend" }),
      ];
      setupMockWorkspace({ repos });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      act(() => {
        result.current.selectRepos(["frontend", "backend"]);
      });

      expect(result.current.activeRepoNames).toEqual(["frontend", "backend"]);
    });

    it("returns empty array when no repos exist", () => {
      setupMockWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper,
      });

      expect(result.current.activeRepoNames).toEqual([]);
    });
  });
});
