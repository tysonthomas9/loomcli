/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree component.
 * Covers repo list rendering, agent counts, collapse/expand,
 * localStorage persistence, loading/error/empty states, and callbacks.
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
};

const defaultAgentContext = {
  agents: [] as Array<{ id: string; repo?: string; status?: string }>,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 0,
    need_review: 0,
    backlog: 0,
  },
  taskLists: {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  },
  agentTasks: {},
  sync: {
    db_synced: true,
    db_last_sync: "",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 0,
    closed: 0,
    total: 0,
    completion: 0,
    remaining: 0,
    in_progress: 0,
    review: 0,
    blocked: 0,
  },
  isLoading: false,
  isConnected: true,
  lastUpdated: new Date(),
};

// Mutable overrides – tests can replace before rendering
let reposOverride: Partial<typeof defaultReposReturn> = {};
let agentOverride: Partial<typeof defaultAgentContext> = {};

vi.mock("@/hooks", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentContext: () => ({ ...defaultAgentContext, ...agentOverride }),
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
}));

describe("WorkspaceTree", () => {
  beforeEach(() => {
    localStorage.clear();
    reposOverride = {};
    agentOverride = {};
  });

  describe("repo list rendering", () => {
    it("renders repo names when expanded", () => {
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

  describe("agent counts per repo", () => {
    it("shows agent count badge for repos with active agents", () => {
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
          { id: "a1", repo: "alpha" },
          { id: "a2", repo: "alpha" },
          { id: "a3", repo: "beta" },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // alpha should show count 2, beta should show count 1
      const alphaButton = screen.getByText("alpha").closest("button")!;
      expect(alphaButton.textContent).toContain("2");

      const betaButton = screen.getByText("beta").closest("button")!;
      expect(betaButton.textContent).toContain("1");
    });

    it("matches agents by repo path as well as name", () => {
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
      agentOverride = {
        agents: [{ id: "a1", repo: "/repos/alpha" }],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      const alphaButton = screen.getByText("alpha").closest("button")!;
      expect(alphaButton.textContent).toContain("1");
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

      expect(localStorage.getItem("workspace-tree-collapsed")).toBe("false");

      const collapseButton = screen.getByRole("button", {
        name: /collapse workspace tree/i,
      });
      fireEvent.click(collapseButton);

      expect(localStorage.getItem("workspace-tree-collapsed")).toBe("true");
    });

    it("reads initial state from localStorage", () => {
      localStorage.setItem("workspace-tree-collapsed", "false");

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
    it("shows error message and retry button", () => {
      const mockRefetch = vi.fn();
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Network error",
        refetch: mockRefetch,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("Network error")).toBeInTheDocument();

      const retryButton = screen.getByRole("button", { name: /retry/i });
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(mockRefetch).toHaveBeenCalledTimes(1);
    });
  });

  describe("workspace select callback", () => {
    it("fires onWorkspaceSelect with repo name when a repo is clicked", () => {
      const handleSelect = vi.fn();
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

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          onWorkspaceSelect={handleSelect}
        />,
      );

      const repoButton = screen.getByText("alpha").closest("button")!;
      fireEvent.click(repoButton);

      expect(handleSelect).toHaveBeenCalledTimes(1);
      expect(handleSelect).toHaveBeenCalledWith("alpha");
    });

    it("fires onWorkspaceSelect with null when All Workspaces is clicked", () => {
      const handleSelect = vi.fn();
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

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          onWorkspaceSelect={handleSelect}
        />,
      );

      const allButton = screen.getByText("All Workspaces").closest("button")!;
      fireEvent.click(allButton);

      expect(handleSelect).toHaveBeenCalledTimes(1);
      expect(handleSelect).toHaveBeenCalledWith(null);
    });
  });

  describe("All Workspaces entry", () => {
    it("renders All Workspaces option above repo list", () => {
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

      expect(screen.getByText("All Workspaces")).toBeInTheDocument();
    });

    it("shows All Workspaces radio as checked when activeRepoName is null", () => {
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

      render(<WorkspaceTree defaultCollapsed={false} activeRepoName={null} />);

      const allButton = screen.getByText("All Workspaces").closest("button")!;
      expect(allButton).toHaveAttribute("aria-checked", "true");

      const alphaButton = screen.getByText("alpha").closest("button")!;
      expect(alphaButton).toHaveAttribute("aria-checked", "false");
    });

    it("shows repo radio as checked when activeRepoName matches", () => {
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

      render(<WorkspaceTree defaultCollapsed={false} activeRepoName="alpha" />);

      const allButton = screen.getByText("All Workspaces").closest("button")!;
      expect(allButton).toHaveAttribute("aria-checked", "false");

      const alphaButton = screen.getByText("alpha").closest("button")!;
      expect(alphaButton).toHaveAttribute("aria-checked", "true");

      const betaButton = screen.getByText("beta").closest("button")!;
      expect(betaButton).toHaveAttribute("aria-checked", "false");
    });

    it("shows total agent count on All Workspaces entry", () => {
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
          { id: "a1", repo: "alpha" },
          { id: "a2", repo: "beta" },
          { id: "a3", repo: "beta" },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      const allButton = screen.getByText("All Workspaces").closest("button")!;
      expect(allButton.textContent).toContain("3");
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
          { id: "a1", repo: "alpha" },
          { id: "a2", repo: "beta" },
          { id: "a3", repo: "beta" },
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
});
