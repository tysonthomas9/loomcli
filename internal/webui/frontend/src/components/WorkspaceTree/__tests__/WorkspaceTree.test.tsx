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
  useToast: () => ({ showToast: vi.fn() }),
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
            repo: "alpha",
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

      render(<WorkspaceTree defaultCollapsed={false} />);

      // alpha should show count 2, beta should show count 1
      const radios = screen.getAllByRole("radio");
      const alphaButton = radios.find((r) => r.textContent?.includes("alpha"))!;
      expect(alphaButton.textContent).toContain("2");

      const betaButton = radios.find((r) => r.textContent?.includes("beta"))!;
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
        agents: [
          {
            name: "a1",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "/repos/alpha",
          },
        ],
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

  describe("per-repo AgentCard rendering", () => {
    it("renders AgentCards inside expanded repo groups with correct agent data", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 2,
            behind: 0,
            repo: "alpha",
          },
          {
            name: "nova",
            branch: "feat-2",
            status: "ready",
            ahead: 0,
            behind: 1,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Agent names should appear (rendered by AgentCard)
      expect(screen.getByText("falcon")).toBeInTheDocument();
      expect(screen.getByText("nova")).toBeInTheDocument();
    });

    it("does not render AgentCards when repo group is collapsed", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      // Pre-set repo collapsed state
      localStorage.setItem(
        "workspace-tree-repo-collapsed",
        JSON.stringify({ alpha: true }),
      );

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Repo name should show but agent should be hidden
      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.queryByText("falcon")).not.toBeInTheDocument();
    });

    it("renders AgentCards for multiple repos with correct grouping", () => {
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
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
          {
            name: "nova",
            branch: "feat-2",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "beta",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("falcon")).toBeInTheDocument();
      expect(screen.getByText("nova")).toBeInTheDocument();
    });

    it("does not render AgentCards section when repo has no agents", () => {
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
      agentOverride = { agents: [] };

      const { container } = render(<WorkspaceTree defaultCollapsed={false} />);

      // Repo name should show but no agent group div
      expect(screen.getByText("alpha")).toBeInTheDocument();
      const agentGroups = container.querySelectorAll(
        '[class*="repoGroupAgents"]',
      );
      expect(agentGroups.length).toBe(0);
    });
  });

  describe("per-repo collapse chevron", () => {
    it("clicking chevron toggles agent card visibility", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Initially expanded – agent visible
      expect(screen.getByText("falcon")).toBeInTheDocument();

      // Click collapse chevron
      const chevron = screen.getByRole("button", {
        name: /collapse agents/i,
      });
      fireEvent.click(chevron);

      // Agent should be hidden
      expect(screen.queryByText("falcon")).not.toBeInTheDocument();

      // Click expand chevron to show again
      const expandChevron = screen.getByRole("button", {
        name: /expand agents/i,
      });
      fireEvent.click(expandChevron);

      expect(screen.getByText("falcon")).toBeInTheDocument();
    });

    it("clicking chevron does NOT trigger onWorkspaceSelect", () => {
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
      agentOverride = {
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          onWorkspaceSelect={handleSelect}
        />,
      );

      const chevron = screen.getByRole("button", {
        name: /collapse agents/i,
      });
      fireEvent.click(chevron);

      expect(handleSelect).not.toHaveBeenCalled();
    });
  });

  describe("per-repo collapse localStorage persistence", () => {
    it("persists per-repo collapse state to localStorage", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Click chevron to collapse alpha
      const chevron = screen.getByRole("button", {
        name: /collapse agents/i,
      });
      fireEvent.click(chevron);

      const stored = JSON.parse(
        localStorage.getItem("workspace-tree-repo-collapsed") || "{}",
      );
      expect(stored.alpha).toBe(true);
    });

    it("reads initial per-repo collapse state from localStorage", () => {
      localStorage.setItem(
        "workspace-tree-repo-collapsed",
        JSON.stringify({ alpha: true }),
      );

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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Agent should be hidden because alpha is collapsed via localStorage
      expect(screen.queryByText("falcon")).not.toBeInTheDocument();
    });

    it("persists collapse state for multiple repos independently", () => {
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
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
          {
            name: "nova",
            branch: "feat-2",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "beta",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Collapse only alpha
      const chevrons = screen.getAllByRole("button", {
        name: /collapse agents/i,
      });
      fireEvent.click(chevrons[0]!);

      // alpha agent hidden, beta agent visible
      expect(screen.queryByText("falcon")).not.toBeInTheDocument();
      expect(screen.getByText("nova")).toBeInTheDocument();

      const stored = JSON.parse(
        localStorage.getItem("workspace-tree-repo-collapsed") || "{}",
      );
      expect(stored.alpha).toBe(true);
      expect(stored.beta).toBeUndefined();
    });
  });

  describe("add button", () => {
    it("renders + button when onAddClick is provided", () => {
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

      render(<WorkspaceTree defaultCollapsed={false} onAddClick={() => {}} />);

      expect(
        screen.getByRole("button", { name: /add workspace/i }),
      ).toBeInTheDocument();
    });

    it("does not render + button when onAddClick is not provided", () => {
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

      expect(
        screen.queryByRole("button", { name: /add workspace/i }),
      ).not.toBeInTheDocument();
    });

    it("calls onAddClick when + button is clicked", () => {
      const handleAdd = vi.fn();
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

      render(<WorkspaceTree defaultCollapsed={false} onAddClick={handleAdd} />);

      const addButton = screen.getByRole("button", {
        name: /add workspace/i,
      });
      fireEvent.click(addButton);

      expect(handleAdd).toHaveBeenCalledTimes(1);
    });

    it("clicking + button does not trigger tree collapse", () => {
      const handleAdd = vi.fn();
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

      render(<WorkspaceTree defaultCollapsed={false} onAddClick={handleAdd} />);

      const addButton = screen.getByRole("button", {
        name: /add workspace/i,
      });
      fireEvent.click(addButton);

      // Tree should still be expanded (repo still visible)
      expect(screen.getByText("alpha")).toBeInTheDocument();
    });
  });

  describe("unassigned agents group", () => {
    it("shows Unassigned group when agents have no matching repo", () => {
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
        agents: [
          {
            name: "orphan",
            branch: "feat-x",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "nonexistent",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("Unassigned")).toBeInTheDocument();
      expect(screen.getByText("orphan")).toBeInTheDocument();
    });

    it("shows Unassigned group for agents with no repo field", () => {
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
        agents: [
          { name: "lone", branch: "main", status: "idle", ahead: 0, behind: 0 },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("Unassigned")).toBeInTheDocument();
      expect(screen.getByText("lone")).toBeInTheDocument();
    });

    it("does not show Unassigned group when all agents match repos", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.queryByText("Unassigned")).not.toBeInTheDocument();
    });

    it("does not show Unassigned group when there are no agents", () => {
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
      agentOverride = { agents: [] };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.queryByText("Unassigned")).not.toBeInTheDocument();
    });

    it("shows agent count on Unassigned group", () => {
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
        agents: [
          {
            name: "orphan1",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "nonexistent",
          },
          {
            name: "orphan2",
            branch: "main",
            status: "idle",
            ahead: 0,
            behind: 0,
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      const unassignedHeader = screen.getByText("Unassigned").closest("div")!;
      expect(unassignedHeader.textContent).toContain("2");
    });

    it("collapse/expand works on Unassigned group", () => {
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
        agents: [
          {
            name: "orphan",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "nonexistent",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Agent should be visible
      expect(screen.getByText("orphan")).toBeInTheDocument();

      // Find the collapse chevron in the Unassigned section
      // There are multiple collapse buttons – the last one is for Unassigned
      const collapseButtons = screen.getAllByRole("button", {
        name: /collapse agents/i,
      });
      const unassignedChevron = collapseButtons[collapseButtons.length - 1]!;
      fireEvent.click(unassignedChevron);

      // Agent should be hidden
      expect(screen.queryByText("orphan")).not.toBeInTheDocument();
    });
  });

  describe("onAgentClick callback", () => {
    it("fires onAgentClick with correct agent name when AgentCard is clicked", () => {
      const handleAgentClick = vi.fn();
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
          {
            name: "nova",
            branch: "feat-2",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          onAgentClick={handleAgentClick}
        />,
      );

      // AgentCard with onClick renders role="button" with aria-label "Agent: falcon"
      const falconCard = screen.getByLabelText("Agent: falcon");
      fireEvent.click(falconCard);

      expect(handleAgentClick).toHaveBeenCalledTimes(1);
      expect(handleAgentClick).toHaveBeenCalledWith("falcon");
    });

    it("fires onAgentClick for unassigned agents too", () => {
      const handleAgentClick = vi.fn();
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
        agents: [
          {
            name: "orphan",
            branch: "main",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "nonexistent",
          },
        ],
      };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          onAgentClick={handleAgentClick}
        />,
      );

      const orphanCard = screen.getByLabelText("Agent: orphan");
      fireEvent.click(orphanCard);

      expect(handleAgentClick).toHaveBeenCalledTimes(1);
      expect(handleAgentClick).toHaveBeenCalledWith("orphan");
    });

    it("does not render clickable AgentCards when onAgentClick is not provided", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Agent name still visible, but no aria-label "Agent: falcon" (no onClick)
      expect(screen.getByText("falcon")).toBeInTheDocument();
      expect(screen.queryByLabelText("Agent: falcon")).not.toBeInTheDocument();
    });
  });

  describe("agentTasks prop", () => {
    it("passes taskTitle to AgentCards via agentTasks prop", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "working: bd-123 (5m)",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          agentTasks={{ falcon: { title: "Fix login bug" } }}
        />,
      );

      // AgentCard uses taskTitle as title attribute on status line
      expect(screen.getByTitle("Fix login bug")).toBeInTheDocument();
    });

    it("passes taskTitle to unassigned AgentCards too", () => {
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
        agents: [
          {
            name: "orphan",
            branch: "main",
            status: "working: bd-456 (2m)",
            ahead: 0,
            behind: 0,
            repo: "nonexistent",
          },
        ],
      };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          agentTasks={{ orphan: { title: "Refactor database" } }}
        />,
      );

      expect(screen.getByTitle("Refactor database")).toBeInTheDocument();
    });

    it("AgentCard shows status label as title when no agentTasks entry", () => {
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
        agents: [
          {
            name: "falcon",
            branch: "feat-1",
            status: "ready",
            ahead: 0,
            behind: 0,
            repo: "alpha",
          },
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} agentTasks={{}} />);

      // Status falls back to "Ready" title
      expect(screen.getByTitle("Ready")).toBeInTheDocument();
    });
  });
});
