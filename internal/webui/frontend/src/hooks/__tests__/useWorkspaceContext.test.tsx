/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceContext hook and WorkspaceProvider.
 *
 * Refactored per docs/design/workspace-provider-refactor.md: WorkspaceProvider
 * now receives `workspace: WorkspaceData` as a prop (from the router loader in
 * production), and its `refetch` / `setDefaultWorkspace` use useRevalidator —
 * which requires the provider to be mounted inside a data router.
 *
 * Tests use createMemoryRouter so useRevalidator resolves cleanly. No loader
 * is needed: the provider takes the workspace directly as a prop, so tests
 * just pass whatever mock data they want.
 */

import { renderHook, act } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { useEffect, useState, type ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import type {
  RepoInfo,
  WorkspaceAgentInfo,
  WorkspaceData,
} from "@/api/workspace";

import { WorkspaceProvider, useWorkspaceContext } from "../useWorkspaceContext";

// Mock API and storage modules used by WorkspaceProvider
vi.mock("@/api/workspace", () => ({
  setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
  clearDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
  invalidateWorkspaceCache: vi.fn(),
}));

vi.mock("@/utils/scopedStorage", () => ({
  wsGet: vi.fn(() => null),
  wsSet: vi.fn(),
  setLastWorkspaceId: vi.fn(),
}));

const TEST_WS_ID = "test-ws-uuid-1234";

/** Build a complete WorkspaceData from partial overrides. */
function makeWorkspace(overrides?: Partial<WorkspaceData>): WorkspaceData {
  return {
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
}

/** Helper to create a mock RepoInfo. */
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

/** Helper to create a mock WorkspaceAgentInfo. */
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
 * Build a renderHook wrapper that mounts WorkspaceProvider inside a
 * createMemoryRouter so useRevalidator resolves. The provider takes the
 * workspace as a prop directly; no loader is needed for tests.
 */
function makeWrapper(workspace: WorkspaceData) {
  return function Wrapper({ children }: { children: ReactNode }) {
    const router = createMemoryRouter(
      [
        {
          path: "/",
          element: (
            <WorkspaceProvider workspace={workspace}>
              {children}
            </WorkspaceProvider>
          ),
        },
        // Catch-all so setActiveWorkspace navigation tests don't log 404s.
        // The provider fires navigate("/ws/<id>/") on switch; we don't care
        // what's rendered there, only that navigate() resolves successfully.
        {
          path: "*",
          element: null,
        },
      ],
      { initialEntries: ["/"] },
    );
    return <RouterProvider router={router} />;
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
      expect(() => result.current.refetch()).not.toThrow();
    });
  });

  describe("inside provider", () => {
    it("provides repos from workspace prop", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.repos).toHaveLength(2);
      expect(result.current.repos[0].name).toBe("api");
      expect(result.current.repos[1].name).toBe("frontend");
    });

    it("provides groups from workspace prop", () => {
      const workspace = makeWorkspace({
        groups: ["backend", "frontend", "infra"],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.groups).toEqual(["backend", "frontend", "infra"]);
    });

    it("provides agents from workspace prop", () => {
      const workspace = makeWorkspace({
        agents: [
          createMockAgent({ name: "nova" }),
          createMockAgent({ name: "falcon", cross_repo: true }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.agents).toHaveLength(2);
      expect(result.current.agents[0].name).toBe("nova");
      expect(result.current.agents[1].name).toBe("falcon");
    });

    it("isLoading is always false (data is always loaded by the time the provider renders)", () => {
      const workspace = makeWorkspace();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.isLoading).toBe(false);
    });

    it("error is always null (loader errors surface via errorElement)", () => {
      const workspace = makeWorkspace();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.error).toBeNull();
    });
  });

  describe("getRepoByName", () => {
    it("finds correct repo by name", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api", source_repo_id: "repo-1" }),
          createMockRepo({ name: "frontend", source_repo_id: "repo-2" }),
          createMockRepo({ name: "backend", source_repo_id: "repo-3" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      const found = result.current.getRepoByName("frontend");
      expect(found).toBeDefined();
      expect(found!.name).toBe("frontend");
      expect(found!.source_repo_id).toBe("repo-2");
    });

    it("returns undefined for unknown repo name", () => {
      const workspace = makeWorkspace({
        repos: [createMockRepo({ name: "api" })],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getRepoByName("unknown")).toBeUndefined();
    });

    it("returns undefined when repos list is empty", () => {
      const workspace = makeWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getRepoByName("api")).toBeUndefined();
    });
  });

  describe("getReposByGroup", () => {
    it("filters correctly by group", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api", groups: ["backend"] }),
          createMockRepo({ name: "frontend", groups: ["frontend"] }),
          createMockRepo({ name: "gateway", groups: ["backend", "infra"] }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      const backendRepos = result.current.getReposByGroup("backend");
      expect(backendRepos).toHaveLength(2);
      expect(backendRepos.map((r) => r.name)).toEqual(["api", "gateway"]);
    });

    it("returns empty array for unknown group", () => {
      const workspace = makeWorkspace({
        repos: [createMockRepo({ name: "api", groups: ["backend"] })],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getReposByGroup("nonexistent")).toEqual([]);
    });

    it("returns empty array when no repos have groups", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api", groups: undefined }),
          createMockRepo({ name: "frontend", groups: undefined }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getReposByGroup("backend")).toEqual([]);
    });

    it("returns repos that belong to multiple groups", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "gateway", groups: ["backend", "infra"] }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getReposByGroup("backend")).toHaveLength(1);
      expect(result.current.getReposByGroup("infra")).toHaveLength(1);
    });
  });

  describe("getAgentByName", () => {
    it("finds correct agent by name", () => {
      const workspace = makeWorkspace({
        agents: [
          createMockAgent({ name: "nova", cross_repo: false }),
          createMockAgent({ name: "falcon", cross_repo: true }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      const found = result.current.getAgentByName("falcon");
      expect(found).toBeDefined();
      expect(found!.name).toBe("falcon");
      expect(found!.cross_repo).toBe(true);
    });

    it("returns undefined for unknown agent name", () => {
      const workspace = makeWorkspace({
        agents: [createMockAgent({ name: "nova" })],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getAgentByName("unknown-agent")).toBeUndefined();
    });

    it("returns undefined when agents list is empty", () => {
      const workspace = makeWorkspace({ agents: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.getAgentByName("nova")).toBeUndefined();
    });
  });

  describe("activeWorkspaceName", () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it("is derived from workspace.name", () => {
      const workspace = makeWorkspace({ name: "my-workspace" });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.activeWorkspaceName).toBe("my-workspace");
    });
  });

  describe("setActiveWorkspace", () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it("navigates via React Router without throwing (no page reload)", () => {
      const workspace = makeWorkspace({
        workspaces: [
          {
            id: TEST_WS_ID,
            name: "test-workspace",
            path: "/path",
            active: true,
            repo_count: 0,
            is_default: false,
          },
          {
            id: "ws-2",
            name: "new-workspace",
            path: "/path",
            active: false,
            repo_count: 0,
            is_default: false,
          },
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.setActiveWorkspace("new-workspace");
      });

      // Navigation happens internally via useNavigate — no throw = pass
    });
  });

  describe("selectRepos", () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it("updates activeRepos to only the named repos", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
          createMockRepo({ name: "backend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
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
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.selectRepos(["frontend"]);
      });

      expect(result.current.selectedRepoNames.has("frontend")).toBe(true);
      expect(result.current.selectedRepoNames.has("api")).toBe(false);
    });
  });

  describe("selectAll", () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it("clears selection and returns all repos", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
          createMockRepo({ name: "backend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

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
    beforeEach(() => {
      localStorage.clear();
    });

    it("adds a repo to selection", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.toggleRepo("api");
      });

      expect(result.current.selectedRepoNames.has("api")).toBe(true);
    });

    it("removes a repo from selection", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

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
    beforeEach(() => {
      localStorage.clear();
    });

    it("returns undefined when all repos selected", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.sourceReposFilter).toBeUndefined();
    });

    it("returns repo names when subset selected", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
          createMockRepo({ name: "backend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.selectRepos(["api", "backend"]);
      });

      expect(result.current.sourceReposFilter).toEqual(["api", "backend"]);
    });

    it("returns undefined after selectAll", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
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
    it("is false when 0 repos", () => {
      const workspace = makeWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.isMultiRepo).toBe(false);
    });

    it("is true when 1 repo", () => {
      const workspace = makeWorkspace({
        repos: [createMockRepo({ name: "api" })],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.isMultiRepo).toBe(true);
    });

    it("is true when 2+ repos", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.isMultiRepo).toBe(true);
    });
  });

  describe("localStorage failure", () => {
    it("gracefully handles setItem failure", () => {
      const setItemSpy = vi
        .spyOn(Storage.prototype, "setItem")
        .mockImplementation(() => {
          throw new Error("quota exceeded");
        });

      const workspace = makeWorkspace();

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

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
    beforeEach(() => {
      localStorage.clear();
    });

    it("is true when selectedRepoNames is empty (no filter)", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.isAllSelected).toBe(true);
    });

    it("is false when repos are selected", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.selectRepos(["api"]);
      });

      expect(result.current.isAllSelected).toBe(false);
    });

    it("becomes true again after selectAll", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
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

  /**
   * Regression test for the core bug that motivated the refactor.
   *
   * The bug: `selectedRepoNames` used to persist across workspace switches
   * because WorkspaceProvider held it in useState derived from a workspaceId
   * prop via useEffect — effect-sync antipattern. The fix mounts
   * PerWorkspacePrefsProvider with key={workspace.id}, forcing a clean remount
   * that re-initializes selectedRepoNames from the new workspace's scoped
   * localStorage.
   *
   * This test uses a stateful wrapper that lets us swap the workspace prop
   * mid-test and verifies the inner provider actually resets.
   */
  describe("per-workspace prefs keyed remount", () => {
    beforeEach(() => {
      localStorage.clear();
      vi.clearAllMocks();
    });

    // Shared stateful wrapper factory. The router stays stable; the
    // workspace prop changes via an external signal. This lets us observe
    // what happens when key={workspaceId} changes on PerWorkspacePrefsProvider.
    function makeStatefulWrapper(initial: WorkspaceData) {
      const state: { workspace: WorkspaceData } = { workspace: initial };
      const listeners = new Set<() => void>();

      function swap(ws: WorkspaceData) {
        state.workspace = ws;
        listeners.forEach((fn) => fn());
      }

      function Wrapper({ children }: { children: ReactNode }) {
        const [, setTick] = useState(0);
        useEffect(() => {
          const fn = () => setTick((n) => n + 1);
          listeners.add(fn);
          return () => {
            listeners.delete(fn);
          };
        }, []);
        const router = createMemoryRouter(
          [
            {
              path: "/",
              element: (
                <WorkspaceProvider workspace={state.workspace}>
                  {children}
                </WorkspaceProvider>
              ),
            },
            { path: "*", element: null },
          ],
          { initialEntries: ["/"] },
        );
        return <RouterProvider router={router} />;
      }

      return { Wrapper, swap };
    }

    it("resets selectedRepoNames when workspace.id changes", async () => {
      const { wsGet } = await import("@/utils/scopedStorage");
      vi.mocked(wsGet).mockImplementation(() => null);

      const workspaceA = makeWorkspace({
        id: "ws-a",
        name: "workspace-a",
        repos: [createMockRepo({ name: "api" })],
      });
      const workspaceB = makeWorkspace({
        id: "ws-b",
        name: "workspace-b",
        repos: [createMockRepo({ name: "other" })],
      });

      const { Wrapper, swap } = makeStatefulWrapper(workspaceA);

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: Wrapper,
      });

      // Select "api" in workspace A
      act(() => {
        result.current.selectRepos(["api"]);
      });
      expect(result.current.selectedRepoNames.has("api")).toBe(true);
      expect(result.current.isAllSelected).toBe(false);

      // Switch to workspace B. Key changes on PerWorkspacePrefsProvider,
      // triggering unmount+remount. Workspace B's localStorage returns null
      // so the new init is an empty Set.
      act(() => {
        swap(workspaceB);
      });

      expect(result.current.selectedRepoNames.size).toBe(0);
      expect(result.current.isAllSelected).toBe(true);
    });

    it("restores stored selection from scoped localStorage on workspace switch", async () => {
      const { wsGet } = await import("@/utils/scopedStorage");
      vi.mocked(wsGet).mockImplementation((wsId: string) => {
        if (wsId === "ws-b") return JSON.stringify(["other"]);
        return null;
      });

      const workspaceA = makeWorkspace({
        id: "ws-a",
        repos: [createMockRepo({ name: "api" })],
      });
      const workspaceB = makeWorkspace({
        id: "ws-b",
        repos: [
          createMockRepo({ name: "other" }),
          createMockRepo({ name: "another" }),
        ],
      });

      const { Wrapper, swap } = makeStatefulWrapper(workspaceA);

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: Wrapper,
      });

      // Workspace A starts with nothing selected
      expect(result.current.selectedRepoNames.size).toBe(0);

      // Switch to workspace B → should restore ["other"] from localStorage
      act(() => {
        swap(workspaceB);
      });

      expect(result.current.selectedRepoNames.has("other")).toBe(true);
      expect(result.current.selectedRepoNames.has("another")).toBe(false);
    });
  });

  describe("activeRepoNames", () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it("returns all repo names when no filter applied", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
          createMockRepo({ name: "backend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.activeRepoNames).toEqual([
        "api",
        "frontend",
        "backend",
      ]);
    });

    it("returns only selected repo names when filter applied", () => {
      const workspace = makeWorkspace({
        repos: [
          createMockRepo({ name: "api" }),
          createMockRepo({ name: "frontend" }),
          createMockRepo({ name: "backend" }),
        ],
      });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      act(() => {
        result.current.selectRepos(["frontend", "backend"]);
      });

      expect(result.current.activeRepoNames).toEqual(["frontend", "backend"]);
    });

    it("returns empty array when no repos exist", () => {
      const workspace = makeWorkspace({ repos: [] });

      const { result } = renderHook(() => useWorkspaceContext(), {
        wrapper: makeWrapper(workspace),
      });

      expect(result.current.activeRepoNames).toEqual([]);
    });
  });
});
