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
import { WorkspaceTree } from "../WorkspaceTree";

// Default mock return values
const defaultReposReturn = {
  workspace: null,
  repos: [] as Array<{
    name: string;
    path: string;
    default_branch: string;
    remote: string;
  }>,
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

const TEST_WS_ID = "test-ws-uuid-1234";

vi.mock("@/hooks", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentContext: () => ({ ...defaultAgentContext, ...agentOverride }),
  useWorkspaceContext: () => ({
    workspaceId: TEST_WS_ID,
    activeWorkspaceName: null,
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
    agents: [],
  }),
  useToast: () => ({ showToast: vi.fn() }),
  useIssueDiffStat: () => ({
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

vi.mock("@/hooks/useWorkspaceTree", () => ({
  useWorkspaceTree: () => ({
    epics: [],
    orphanTasks: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

describe("WorkspaceTree", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("loom:last-workspace-id", TEST_WS_ID);
    reposOverride = {};
    agentOverride = {};
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
          },
          {
            name: "beta",
            path: "/repos/beta",
            default_branch: "main",
            remote: "origin",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
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
    it("shows total active agent count when collapsed", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
          {
            name: "beta",
            path: "/repos/beta",
            default_branch: "main",
            remote: "origin",
          },
        ],
      };
      agentOverride = {
        agents: [
          {
            name: "a1",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
          {
            name: "a2",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "beta",
          },
          {
            name: "a3",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "beta",
          },
        ],
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).toBeInTheDocument();
      expect(badge!.textContent).toBe("3");
    });

    it("does not show collapsed badge when no active agents", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).not.toBeInTheDocument();
    });
  });

  // "+ New Workspace" button moved to WorkspaceSwitcher dialog (loomcli-8uy0o)
});
