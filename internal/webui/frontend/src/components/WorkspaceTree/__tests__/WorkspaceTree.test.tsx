/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree component (redesigned sidebar).
 * Covers repo list rendering, collapse/expand, localStorage persistence,
 * loading/error/empty states, collapsed badge, and the + New Workspace button.
 *
 * Agent interaction tests (onAgentClick, agentTasks) live in AgentSection tests.
 * Repo-grouped layout tests were removed in loomcli-8uy0o (sidebar redesign).
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";
import type { RepoInfo } from "@/api/workspace";
import { WorkspaceTree } from "../WorkspaceTree";

// Default mock return values
const defaultReposReturn = {
  workspace: null as null | {
    name: string;
    id: string;
    workspaces: Array<{ name: string; id: string }>;
  },
  repos: [] as RepoInfo[],
  isLoading: false,
  error: null as string | null,
  refetch: vi.fn(),
  connectionState: "connected" as
    | "loading"
    | "connected"
    | "error_never_connected"
    | "error_lost_connection",
  retryCountdown: null as number | null,
  retryNow: vi.fn(),
  hasEverConnected: false,
};

const defaultAgentContext = {
  agents: [] as Array<{
    name: string;
    branch: string;
    status: string;
    ahead: number;
    behind: number;
    repo?: string;
  }>,
  isLoading: false,
  isConnected: true,
  lastUpdated: new Date(),
  getAgentByName: () => undefined,
};

// Mutable overrides – tests can replace before rendering
let reposOverride: Partial<typeof defaultReposReturn> = {};
let agentOverride: Partial<typeof defaultAgentContext> = {};
let activeWorkspaceNameOverride: string | null = null;

const TEST_WS_ID = "test-ws-uuid-1234";

// Mock zustand's useStore — apply selector to the merged agent context
vi.mock("zustand", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useStore: (_store: unknown, selector: (s: any) => unknown) =>
    selector({ ...defaultAgentContext, ...agentOverride }),
}));

vi.mock("@/hooks", () => ({
  useDebouncedCallback: (fn: (...args: unknown[]) => unknown) => fn,
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentStoreInstance: () => ({}), // dummy — useStore mock ignores store arg
  useWorkspaceContext: () => ({
    workspaceId: TEST_WS_ID,
    activeWorkspaceName: activeWorkspaceNameOverride,
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
    agents: [],
    ...defaultReposReturn,
    ...reposOverride,
  }),
  useWorkspaceTree: () => ({
    epics: [],
    orphanTasks: [],
    closedEpicCount: 0,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useToast: () => ({ showToast: vi.fn() }),
  useIssueDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useAgentDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
  LAYER_TERMINAL_SEARCH: 5,
  LAYER_WORKSPACE_SWITCHER: 42,
  useFocusTrap: vi.fn(),
  useFocusReturn: vi.fn(),
}));

vi.mock("@/components/WorkspaceSwitcher", () => ({
  WorkspaceSwitcher: () => null,
}));

vi.mock("@/hooks/useWorkspaceRepos", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceTree: () => ({
      epics: [],
      orphanTasks: [],
      closedEpicCount: 0,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    }),
  };
});

describe("WorkspaceTree", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("loom:last-workspace-id", TEST_WS_ID);
    reposOverride = {};
    agentOverride = {};
    activeWorkspaceNameOverride = null;
  });

  describe("repo list rendering", () => {
    it("renders repo names in Repos section when expanded", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
          {
            name: "beta",
            path: "/repos/beta",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
    });

    it("renders a repo with is_linked_worktree: true (sibling-collision workspace)", () => {
      // Regression guard for the "No repos in workspace" bug: if the
      // sidebar ever re-introduces a .filter(!r.is_linked_worktree)
      // predicate, a workspace whose only repo is a linked worktree would
      // render the empty state instead of its repo. That must not happen.
      reposOverride = {
        repos: [
          {
            name: "bravo",
            path: "/root/.loom/workspaces/bravo/bravo",
            default_branch: "main",
            remote: "origin",
            groups: [],
            is_linked_worktree: true,
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("bravo")).toBeInTheDocument();
      expect(
        screen.queryByText("No repos in workspace"),
      ).not.toBeInTheDocument();
    });
  });

  describe("collapse toggle", () => {
    it("hides content when collapsed", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={true} />);

      // Repo name should not be visible when collapsed
      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
    });

    it("shows content when expanded via toggle", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={true} />);

      // Click expand
      const expandButton = screen.getByRole("button", {
        name: /expand workspace tree/i,
      });
      fireEvent.click(expandButton);

      expect(screen.getByText("alpha")).toBeInTheDocument();
    });

    it("collapses content when toggle is clicked while expanded", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();

      const collapseButton = screen.getByRole("button", {
        name: /collapse workspace tree/i,
      });
      fireEvent.click(collapseButton);

      expect(screen.queryByText("alpha")).not.toBeInTheDocument();
    });
  });

  describe("localStorage persistence", () => {
    it("persists collapsed state to localStorage", () => {
      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(localStorage.getItem(`loom:${TEST_WS_ID}:tree-collapsed`)).toBe(
        "false",
      );

      const collapseButton = screen.getByRole("button", {
        name: /collapse workspace tree/i,
      });
      fireEvent.click(collapseButton);

      expect(localStorage.getItem(`loom:${TEST_WS_ID}:tree-collapsed`)).toBe(
        "true",
      );
    });

    it("reads initial state from localStorage", () => {
      localStorage.setItem(`loom:${TEST_WS_ID}:tree-collapsed`, "false");

      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      // defaultCollapsed=true but localStorage says false, so it should be expanded
      render(<WorkspaceTree defaultCollapsed={true} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty message when no repos", () => {
      reposOverride = { repos: [], isLoading: false, error: null };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("No repos in workspace")).toBeInTheDocument();
    });
  });

  describe("loading state", () => {
    it("shows skeleton rows when loading with no repos", () => {
      reposOverride = { repos: [], isLoading: true };

      const { container } = render(<WorkspaceTree defaultCollapsed={false} />);

      const skeletons = container.querySelectorAll('[class*="skeletonRow"]');
      expect(skeletons.length).toBe(3);
    });
  });

  describe("error state", () => {
    it("shows error display and retry button for error_never_connected", () => {
      const mockRetryNow = vi.fn();
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Network error",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: mockRetryNow,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByTestId("error-display")).toBeInTheDocument();

      const retryButton = screen.getByRole("button", { name: /retry/i });
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(mockRetryNow).toHaveBeenCalledTimes(1);
    });
  });

  describe("collapsed badge", () => {
    it("shows disconnected badge when collapsed and disconnected", () => {
      const { container } = render(
        <WorkspaceTree
          defaultCollapsed={true}
          connectionState="disconnected"
          disconnectedSince={Date.now() - 5000}
        />,
      );

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).toBeInTheDocument();
      expect(badge!.textContent).toBe("!");
      expect(badge!.getAttribute("data-disconnected")).toBe("true");
    });

    it("does not show collapsed badge when connected and no disconnected state", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };

      const { container } = render(
        <WorkspaceTree defaultCollapsed={true} connectionState="connected" />,
      );

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).not.toBeInTheDocument();
    });
  });

  // "+ New Workspace" button moved to WorkspaceSwitcher dialog (loomcli-8uy0o)

  describe("other workspaces section", () => {
    it("renders non-active workspaces below the repos list", () => {
      activeWorkspaceNameOverride = "alpha";
      reposOverride = {
        workspace: {
          name: "alpha",
          id: TEST_WS_ID,
          workspaces: [
            { name: "alpha", id: TEST_WS_ID },
            { name: "beta", id: "beta-id" },
          ],
        },
      };

      render(
        <WorkspaceTree defaultCollapsed={false} onWorkspaceSwitch={vi.fn()} />,
      );

      // "beta" is the only non-active workspace; it should render
      expect(screen.getByText("beta")).toBeInTheDocument();
    });

    it("does not render the Workspaces section when only the active workspace exists", () => {
      activeWorkspaceNameOverride = "solo";
      reposOverride = {
        workspace: {
          name: "solo",
          id: TEST_WS_ID,
          workspaces: [{ name: "solo", id: TEST_WS_ID }],
        },
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // The OtherWorkspacesSection header is "Workspaces" — must be absent
      expect(screen.queryByText("Workspaces")).not.toBeInTheDocument();
    });
  });

  describe("queue stats bar", () => {
    it("shows Failed (not Blocked) and reflects the failed count", () => {
      render(
        <WorkspaceTree
          defaultCollapsed={false}
          workQueueCounts={{
            backlog: 0,
            open: 1,
            blocked: 0,
            inProgress: 0,
            needsReview: 0,
            done: 5,
            failed: 3,
          }}
        />,
      );

      expect(screen.getByText("Failed")).toBeInTheDocument();
      expect(screen.getByText("3")).toBeInTheDocument();
      expect(screen.queryByText("Blocked")).not.toBeInTheDocument();
    });
  });

  describe("status bar removal", () => {
    it("does not render the working/reviewing/idle status bar copy", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
            groups: [],
          },
        ],
      };
      agentOverride = {
        agents: [
          {
            name: "a1",
            branch: "main",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // SidebarStatusBar copy ("N working", "N reviewing", "N idle") must be gone
      expect(screen.queryByText(/\d+\s*working/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/\d+\s*reviewing/i)).not.toBeInTheDocument();
      expect(screen.queryByText(/\d+\s*idle/i)).not.toBeInTheDocument();
    });
  });
});
