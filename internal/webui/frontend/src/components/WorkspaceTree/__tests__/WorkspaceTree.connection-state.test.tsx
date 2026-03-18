/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree connection state handling.
 * Covers ErrorDisplay rendering for error_never_connected,
 * error badge in collapsed state, stale banner + dimmed repo list
 * for error_lost_connection, and retry button label with countdown.
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

// Mutable overrides - tests can replace before rendering
let reposOverride: Partial<typeof defaultReposReturn> = {};
let agentOverride: Partial<typeof defaultAgentContext> = {};

vi.mock("@/hooks", () => ({
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentContext: () => ({ ...defaultAgentContext, ...agentOverride }),
  useWorkspaceContext: () => ({
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
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

describe("WorkspaceTree connection state", () => {
  beforeEach(() => {
    localStorage.clear();
    reposOverride = {};
    agentOverride = {};
  });

  describe("error_never_connected state", () => {
    it("renders ErrorDisplay with connection-error variant", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // ErrorDisplay renders with data-testid="error-display"
      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(screen.getByTestId("error-display")).toHaveAttribute(
        "data-variant",
        "connection-error",
      );
    });

    it("renders ErrorDisplay with correct title and description", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("heading", { name: "Workspace unavailable" }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/Could not connect to workspace/),
      ).toBeInTheDocument();
    });

    it("renders retry button that calls retryNow", () => {
      const mockRetryNow = vi.fn();
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: mockRetryNow,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      const retryButton = screen.getByTestId("retry-button");
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(mockRetryNow).toHaveBeenCalledTimes(1);
    });

    it("shows 'Retry now' label when retryCountdown is null", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry now" }),
      ).toBeInTheDocument();
    });

    it("shows countdown in retry label when retryCountdown is non-null", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: 8,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry in 8s" }),
      ).toBeInTheDocument();
    });

    it("shows 'Retrying...' label when isLoading is true", () => {
      reposOverride = {
        repos: [],
        isLoading: true,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retrying..." }),
      ).toBeInTheDocument();
    });
  });

  describe("error badge in collapsed state", () => {
    it("renders error badge when collapsed and connectionState is error_never_connected", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const errorBadge = container.querySelector('[class*="errorBadge"]');
      expect(errorBadge).toBeInTheDocument();
      expect(errorBadge!.textContent).toBe("!");
      expect(errorBadge).toHaveAttribute("title", "Workspace connection error");
    });

    it("renders error badge when collapsed and connectionState is error_lost_connection", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Connection lost",
        connectionState: "error_lost_connection",
        retryCountdown: 10,
        retryNow: vi.fn(),
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const errorBadge = container.querySelector('[class*="errorBadge"]');
      expect(errorBadge).toBeInTheDocument();
      expect(errorBadge!.textContent).toBe("!");
    });

    it("does not render error badge when connectionState is connected", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: null,
        connectionState: "connected",
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const errorBadge = container.querySelector('[class*="errorBadge"]');
      expect(errorBadge).not.toBeInTheDocument();
    });

    it("does not render error badge when connectionState is loading", () => {
      reposOverride = {
        repos: [],
        isLoading: true,
        error: null,
        connectionState: "loading",
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const errorBadge = container.querySelector('[class*="errorBadge"]');
      expect(errorBadge).not.toBeInTheDocument();
    });

    it("does not render error badge when expanded (not collapsed)", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={false} />);

      const errorBadge = container.querySelector('[class*="errorBadge"]');
      expect(errorBadge).not.toBeInTheDocument();
    });
  });

  describe("error_lost_connection state", () => {
    it("renders stale banner with 'Connection lost' text", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Server down",
        connectionState: "error_lost_connection",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByText("Connection lost — showing last known state"),
      ).toBeInTheDocument();
    });

    it("renders retry button in stale banner that calls retryNow", () => {
      const mockRetryNow = vi.fn();
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Server down",
        connectionState: "error_lost_connection",
        retryCountdown: null,
        retryNow: mockRetryNow,
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      const retryButton = screen.getByRole("button", { name: "Retry now" });
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(mockRetryNow).toHaveBeenCalledTimes(1);
    });

    it("shows countdown in stale banner retry label when retryCountdown is non-null", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Server down",
        connectionState: "error_lost_connection",
        retryCountdown: 15,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry in 15s" }),
      ).toBeInTheDocument();
    });

    it("renders repo list with staleOverlay class applied", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Server down",
        connectionState: "error_lost_connection",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={false} />);

      const repoList = container.querySelector('[class*="staleOverlay"]');
      expect(repoList).toBeInTheDocument();
    });

    it("still shows repo names in dimmed repo list", () => {
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
        isLoading: false,
        error: "Server down",
        connectionState: "error_lost_connection",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.getByText("alpha")).toBeInTheDocument();
      expect(screen.getByText("beta")).toBeInTheDocument();
    });

    it("does not render staleOverlay when connected", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: null,
        connectionState: "connected",
      };

      const { container } = render(<WorkspaceTree defaultCollapsed={false} />);

      const staleOverlay = container.querySelector('[class*="staleOverlay"]');
      expect(staleOverlay).not.toBeInTheDocument();
    });

    it("does not render stale banner when connected", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: null,
        connectionState: "connected",
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.queryByText("Connection lost — showing last known state"),
      ).not.toBeInTheDocument();
    });
  });

  describe("retry button label variants", () => {
    it("shows 'Retry now' when retryCountdown is null in error_never_connected", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry now" }),
      ).toBeInTheDocument();
    });

    it("shows 'Retry in Ns' when retryCountdown is non-null in error_never_connected", () => {
      reposOverride = {
        repos: [],
        isLoading: false,
        error: "Connection refused",
        connectionState: "error_never_connected",
        retryCountdown: 3,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry in 3s" }),
      ).toBeInTheDocument();
    });

    it("shows 'Retry now' when retryCountdown is null in error_lost_connection", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Connection lost",
        connectionState: "error_lost_connection",
        retryCountdown: null,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry now" }),
      ).toBeInTheDocument();
    });

    it("shows 'Retry in Ns' when retryCountdown is non-null in error_lost_connection", () => {
      reposOverride = {
        repos: [
          {
            name: "alpha",
            path: "/repos/alpha",
            default_branch: "main",
            remote: "origin",
          },
        ],
        isLoading: false,
        error: "Connection lost",
        connectionState: "error_lost_connection",
        retryCountdown: 42,
        retryNow: vi.fn(),
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(
        screen.getByRole("button", { name: "Retry in 42s" }),
      ).toBeInTheDocument();
    });
  });
});
