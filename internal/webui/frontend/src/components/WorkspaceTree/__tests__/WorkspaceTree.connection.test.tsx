/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for WorkspaceTree connection status indicator feature.
 * Covers daemon prompt rendering, retry button, collapsed badge
 * disconnected state, and existing behavior when props are omitted.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import "@testing-library/jest-dom";
import { WorkspaceTree } from "../WorkspaceTree";

// ── Hook mocks ───────────────────────────────────────────────────────────────

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

let reposOverride: Partial<typeof defaultReposReturn> = {};
let agentOverride: Partial<typeof defaultAgentContext> = {};

// Mock zustand's useStore — apply selector to the merged agent context
vi.mock("zustand", () => ({
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  useStore: (_store: unknown, selector: (s: any) => unknown) =>
    selector({ ...defaultAgentContext, ...agentOverride }),
}));

vi.mock("@/hooks", () => ({
  useDebouncedCallback: (fn: (...args: unknown[]) => unknown) => fn,
  useWorkspaceRepos: () => ({ ...defaultReposReturn, ...reposOverride }),
  useAgentStoreInstance: () => ({}),
  useWorkspaceContext: () => ({
    workspaceId: "test-ws-uuid",
    activeWorkspaceName: null,
    defaultWorkspaceName: null,
    setDefaultWorkspace: vi.fn(),
    agents: [],
    workspace: reposOverride.workspace ?? null,
    repos: reposOverride.repos ?? [],
    isLoading: reposOverride.isLoading ?? defaultReposReturn.isLoading,
    error: reposOverride.error ?? defaultReposReturn.error,
    refetch: reposOverride.refetch ?? defaultReposReturn.refetch,
    connectionState:
      reposOverride.connectionState ?? defaultReposReturn.connectionState,
    retryCountdown:
      reposOverride.retryCountdown ?? defaultReposReturn.retryCountdown,
    retryNow: reposOverride.retryNow ?? defaultReposReturn.retryNow,
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

// ── Helpers ──────────────────────────────────────────────────────────────────

const oneRepo = [
  {
    name: "alpha",
    path: "/repos/alpha",
    default_branch: "main",
    remote: "origin",
  },
];

describe("WorkspaceTree connection status", () => {
  beforeEach(() => {
    localStorage.clear();
    reposOverride = {};
    agentOverride = {};
  });

  describe("daemon prompt", () => {
    it("is NOT shown when connectionLost is false", () => {
      reposOverride = { repos: oneRepo };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          connectionLost={false}
          connectionState="connected"
        />,
      );

      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(screen.queryByText("Connection lost")).not.toBeInTheDocument();
    });

    it("is NOT shown when connectionLost is undefined", () => {
      reposOverride = { repos: oneRepo };

      render(<WorkspaceTree defaultCollapsed={false} />);

      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(screen.queryByText("Connection lost")).not.toBeInTheDocument();
    });

    it("is shown when connectionLost=true with Retry button", () => {
      reposOverride = { repos: oneRepo };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          connectionLost={true}
          connectionState="disconnected"
          onRetryConnection={() => {}}
        />,
      );

      const alert = screen.getByRole("alert");
      expect(alert).toBeInTheDocument();
      expect(screen.getByText("Connection lost")).toBeInTheDocument();
      expect(
        screen.getByText("Check that the workspace service is running."),
      ).toBeInTheDocument();

      // Retry button inside the daemon prompt
      const retryButton = screen.getByRole("button", { name: /retry/i });
      expect(retryButton).toBeInTheDocument();
    });

    it("Retry button calls onRetryConnection callback", () => {
      const handleRetry = vi.fn();
      reposOverride = { repos: oneRepo };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          connectionLost={true}
          connectionState="disconnected"
          onRetryConnection={handleRetry}
        />,
      );

      const retryButton = screen.getByRole("button", { name: /retry/i });
      fireEvent.click(retryButton);

      expect(handleRetry).toHaveBeenCalledTimes(1);
    });

    it("does not render Retry button when onRetryConnection is not provided", () => {
      reposOverride = { repos: oneRepo };

      render(
        <WorkspaceTree
          defaultCollapsed={false}
          connectionLost={true}
          connectionState="disconnected"
        />,
      );

      // The alert should exist but no retry button inside it
      expect(screen.getByRole("alert")).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: /retry/i }),
      ).not.toBeInTheDocument();
    });
  });

  describe("collapsed badge disconnected state", () => {
    it('shows "!" badge with data-disconnected="true" when collapsed and disconnected', () => {
      reposOverride = { repos: oneRepo };

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

    it('shows "!" badge when collapsed and reconnecting', () => {
      reposOverride = { repos: oneRepo };

      const { container } = render(
        <WorkspaceTree
          defaultCollapsed={true}
          connectionState="reconnecting"
          disconnectedSince={Date.now() - 3000}
        />,
      );

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).toBeInTheDocument();
      expect(badge!.textContent).toBe("!");
      expect(badge!.getAttribute("data-disconnected")).toBe("true");
    });

    it("does not show collapsed badge when collapsed and connected (even with agents)", () => {
      reposOverride = { repos: oneRepo };
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
        ],
      };

      const { container } = render(
        <WorkspaceTree defaultCollapsed={true} connectionState="connected" />,
      );

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).not.toBeInTheDocument();
    });
  });

  describe("existing behavior", () => {
    it("renders normally when no connection props are provided", () => {
      reposOverride = { repos: oneRepo };
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
        ],
      };

      render(<WorkspaceTree defaultCollapsed={false} />);

      // Repo renders (appears in both repo name and agent's repo badge)
      const alphaElements = screen.getAllByText("alpha");
      expect(alphaElements.length).toBeGreaterThanOrEqual(1);

      // No daemon prompt
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      expect(screen.queryByText("Connection lost")).not.toBeInTheDocument();
    });

    it("does not show collapsed badge when no agents and no connection props", () => {
      reposOverride = { repos: oneRepo };

      const { container } = render(<WorkspaceTree defaultCollapsed={true} />);

      const badge = container.querySelector('[class*="collapsedBadge"]');
      expect(badge).not.toBeInTheDocument();
    });
  });
});
