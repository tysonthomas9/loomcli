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

import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";
import {
  useWorkspaceTreeWidth,
  WORKSPACE_TREE_MIN_WIDTH,
  WORKSPACE_TREE_MAX_WIDTH,
} from "@/hooks/ui/useWorkspaceTreeWidth";
import { WorkspaceTree } from "../WorkspaceTree";

const { mockAddWorkspaceRepos } = vi.hoisted(() => ({
  mockAddWorkspaceRepos: vi.fn(),
}));

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
    activeWorkspaceName: null,
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
    agents: defaultAgentContext.agents,
    workspace: reposOverride.workspace ?? null,
    repos: reposOverride.repos ?? [],
    isLoading: reposOverride.isLoading ?? defaultReposReturn.isLoading,
    error: reposOverride.error ?? defaultReposReturn.error,
    refetch: reposOverride.refetch ?? defaultReposReturn.refetch,
  }),
  useWorkspaceTree: () => ({
    epics: [],
    orphanTasks: [],
    closedEpicCount: 0,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
  useWorkspaceTreeWidth,
  WORKSPACE_TREE_MIN_WIDTH,
  WORKSPACE_TREE_MAX_WIDTH,
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
  LAYER_WORKSPACE_SWITCHER: 42,
  useFocusTrap: vi.fn(),
  useFocusReturn: vi.fn(),
}));

vi.mock("@/hooks/api", () => ({
  addWorkspaceRepos: (...args: unknown[]) => mockAddWorkspaceRepos(...args),
}));

vi.mock("@/components/WorkspaceSwitcher", () => ({
  WorkspaceSwitcher: () => null,
}));

// Repository/sidebar tests do not exercise the terminal view. Keep its
// runtime graph out of this suite so collecting WorkspaceTree cannot pull in
// terminal sessions, panes, and CodeMirror.
vi.mock("../TerminalSection", () => ({
  TerminalSection: () => null,
}));

vi.mock("@/hooks/useWorkspaceRepos", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
}));

vi.mock("@/hooks/workspace", () => ({
  useAutomations: () => ({ bindings: [] }),
  useWorkspaceTree: () => ({
    epics: [],
    orphanTasks: [],
    closedEpicCount: 0,
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
    mockAddWorkspaceRepos.mockReset();
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

    it("adds a repository through the Add Repo dialog", async () => {
      const refetch = vi.fn();
      mockAddWorkspaceRepos.mockResolvedValue({});
      reposOverride = { repos: [], refetch };

      render(<WorkspaceTree defaultCollapsed={false} />);

      fireEvent.click(screen.getByRole("button", { name: "+ Add Repo" }));
      fireEvent.change(screen.getByLabelText("Repository URL"), {
        target: { value: "/repos/api" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));

      await waitFor(() => {
        expect(mockAddWorkspaceRepos).toHaveBeenCalledWith(TEST_WS_ID, {
          repos: ["/repos/api"],
          branch: "main",
        });
      });
      expect(refetch).toHaveBeenCalled();
    });

    it("prefills the sample repo in the dialog for one-click empty setup", async () => {
      const refetch = vi.fn();
      mockAddWorkspaceRepos.mockResolvedValue({});
      reposOverride = { repos: [], refetch };

      render(<WorkspaceTree defaultCollapsed={false} />);

      fireEvent.click(screen.getByRole("button", { name: "+ Add Repo" }));
      expect(screen.getByLabelText("Repository URL")).toHaveValue(
        "https://github.com/octocat/Hello-World",
      );
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));

      await waitFor(() => {
        expect(mockAddWorkspaceRepos).toHaveBeenCalledWith(TEST_WS_ID, {
          clone_urls: ["https://github.com/octocat/Hello-World"],
          branch: "main",
        });
      });
      expect(refetch).toHaveBeenCalled();
    });

    it("opens the dialog empty once repos exist (no sample prefill)", async () => {
      reposOverride = {
        repos: [
          {
            name: "hello-world",
            path: "/repos/hello-world",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("hello-world")).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "+ Add Repo" }));
      expect(screen.getByLabelText("Repository URL")).toHaveValue("");
    });

    it("clones a remote repository URL via the dialog", async () => {
      const refetch = vi.fn();
      mockAddWorkspaceRepos.mockResolvedValue({});
      reposOverride = { repos: [], refetch };

      render(<WorkspaceTree defaultCollapsed={false} />);

      fireEvent.click(screen.getByRole("button", { name: "+ Add Repo" }));
      fireEvent.change(screen.getByLabelText("Repository URL"), {
        target: { value: "https://github.com/octocat/Hello-World" },
      });
      fireEvent.click(screen.getByRole("button", { name: "Add Repository" }));

      await waitFor(() => {
        expect(mockAddWorkspaceRepos).toHaveBeenCalledWith(TEST_WS_ID, {
          clone_urls: ["https://github.com/octocat/Hello-World"],
          branch: "main",
        });
      });
      expect(refetch).toHaveBeenCalled();
    });
  });

  describe("agent creation entrypoint", () => {
    it("shows the add-agent button even when no agents exist", () => {
      const onAddClick = vi.fn();

      render(
        <WorkspaceTree defaultCollapsed={false} onAddClick={onAddClick} />,
      );

      fireEvent.click(screen.getByRole("button", { name: "+ Add agent" }));

      expect(onAddClick).toHaveBeenCalledOnce();
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
    it("offers the Add Repo dialog in the repos section when no repos", () => {
      reposOverride = { repos: [], isLoading: false, error: null };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("heading", { name: "Repos" }),
      ).toBeInTheDocument();
      fireEvent.click(screen.getByRole("button", { name: "+ Add Repo" }));
      expect(screen.getByLabelText("Repository URL")).toBeInTheDocument();
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
        refetch: mockRetryNow,
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

      const badge = container.querySelector(
        '[class*="collapsedConnectionBadge"]',
      );
      expect(badge).toBeInTheDocument();
      expect(badge!.textContent).toBe("!");
      expect(badge!.getAttribute("data-disconnected")).toBe("true");
    });

    it("shows collapsed agent rail when connected", () => {
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
        <WorkspaceTree defaultCollapsed={true} connectionState="connected" />,
      );

      expect(screen.getByTestId("collapsed-agent-rail")).toBeInTheDocument();
      expect(screen.getByTestId("collapsed-repo-rail")).toBeInTheDocument();
      expect(screen.getByLabelText("alpha")).toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Add repo" }),
      ).toBeInTheDocument();
    });
  });

  describe("sidebar layout", () => {
    it("renders sections in linear scroll order with repos after agents", () => {
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

      const { container } = render(
        <WorkspaceTree defaultCollapsed={false} onAddClick={() => {}} />,
      );

      const content = container.querySelector('[class*="content"]');
      const addAgentButton = screen.getByRole("button", {
        name: "+ Add agent",
      });
      const reposHeading = screen.getByRole("heading", { name: "Repos" });

      expect(content).toBeInTheDocument();
      expect(
        container.querySelector('[class*="mainScroll"]'),
      ).not.toBeInTheDocument();
      expect(
        container.querySelector('[class*="reposDock"]'),
      ).not.toBeInTheDocument();
      expect(content!.contains(addAgentButton)).toBe(true);
      expect(content!.contains(reposHeading)).toBe(true);
      expect(
        addAgentButton.compareDocumentPosition(reposHeading) &
          Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    });
  });

  describe("sidebar resize", () => {
    it("shows resize handle when expanded", () => {
      render(<WorkspaceTree defaultCollapsed={false} />);
      expect(
        screen.getByTestId("workspace-tree-resize-handle"),
      ).toBeInTheDocument();
    });

    it("hides resize handle when collapsed", () => {
      render(<WorkspaceTree defaultCollapsed={true} />);
      expect(
        screen.queryByTestId("workspace-tree-resize-handle"),
      ).not.toBeInTheDocument();
    });

    it("widens sidebar via keyboard resize and persists width", async () => {
      render(<WorkspaceTree defaultCollapsed={false} />);
      const handle = screen.getByTestId("workspace-tree-resize-handle");
      const aside = handle.closest("aside");

      fireEvent.keyDown(handle, { key: "ArrowRight" });

      expect(aside).toHaveStyle({ "--workspace-tree-sidebar-width": "226px" });
      // Persistence is debounced (~200ms) to avoid per-pointermove writes.
      await waitFor(() => {
        expect(
          localStorage.getItem(`loom:${TEST_WS_ID}:workspace-tree-width`),
        ).toBe("226");
      });
    });
  });

  // "+ New Workspace" button moved to WorkspaceSwitcher dialog (loomcli-8uy0o)
});
