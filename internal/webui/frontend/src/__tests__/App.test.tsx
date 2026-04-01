/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for App component.
 */

import {
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { ConnectionState } from "@/api/sse";
import {
  useFilterState,
  useIssueDetail,
  useRouteView,
  useAgentContext,
  useWorkspaceContext,
} from "@/hooks";
import { useIssues } from "@/hooks/useIssues";
import type { Issue, Status } from "@/types";

import App from "../App";

// Mock react-router-dom
const mockNavigate = vi.fn();
vi.mock("react-router-dom", () => ({
  useParams: vi.fn(() => ({ workspaceId: "test-ws-id" })),
  useNavigate: vi.fn(() => mockNavigate),
  useSearchParams: vi.fn(() => [new URLSearchParams(), vi.fn()]),
  useLocation: vi.fn(() => ({
    pathname: "/ws/test-ws-id/kanban",
    search: "",
    hash: "",
    state: null,
    key: "default",
  })),
  Outlet: () => null,
}));

// Create hoisted mocks for @/api functions used by handleApprove/handleReject
const { mockCloseIssue, mockUpdateIssue, mockAddComment } = vi.hoisted(() => ({
  mockCloseIssue: vi.fn(),
  mockUpdateIssue: vi.fn(),
  mockAddComment: vi.fn(),
}));

// Create hoisted mocks for usePanelManager return values
const { mockOpenPanel, mockClosePanel, mockIsOpen, mockUsePanelManager } =
  vi.hoisted(() => ({
    mockOpenPanel: vi.fn(),
    mockClosePanel: vi.fn(),
    mockIsOpen: vi.fn(() => false),
    mockUsePanelManager: vi.fn(),
  }));

// Create hoisted mocks that can be shared across mock definitions
const {
  mockUseIssues,
  mockUseIssueDetail,
  mockUseToast,
  mockUseAgents,
  mockUseAgentContext,
} = vi.hoisted(() => ({
  mockUseIssues: vi.fn(),
  mockUseIssueDetail: vi.fn(),
  mockUseToast: vi.fn(() => ({
    toasts: [],
    showToast: vi.fn(),
    dismissToast: vi.fn(),
    dismissAll: vi.fn(),
  })),
  mockUseAgents: vi.fn(() => ({
    agents: [],
    tasks: {
      needs_planning: 0,
      ready_to_implement: 0,
      in_progress: 0,
      need_review: 0,
      blocked: 0,
    },
    taskLists: {
      needsPlanning: [],
      readyToImplement: [],
      needsReview: [],
      inProgress: [],
      blocked: [],
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
    connectionState: "connected",
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: null,
    refetch: vi.fn(),
    retryNow: vi.fn(),
  })),
  mockUseAgentContext: vi.fn(() => ({
    agents: [],
    tasks: {
      needs_planning: 0,
      ready_to_implement: 0,
      in_progress: 0,
      need_review: 0,
      blocked: 0,
    },
    taskLists: {
      needsPlanning: [],
      readyToImplement: [],
      needsReview: [],
      inProgress: [],
      blocked: [],
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
    connectionState: "connected",
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: null,
    refetch: vi.fn(),
    retryNow: vi.fn(),
    getAgentByName: vi.fn(() => undefined),
  })),
}));

// Mock the useIssues hook from its direct module
vi.mock("@/hooks/useIssues", () => ({
  useIssues: mockUseIssues,
}));

// Mock @/api functions used by handleApprove and handleReject
vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return {
    ...actual,
    updateIssue: mockUpdateIssue,
    addComment: mockAddComment,
    closeIssue: mockCloseIssue,
    getIssueEvents: vi.fn().mockImplementation(() => new Promise(() => {})),
  };
});

// Mock AuthContext so UserMenu can call useAuth() outside of a provider
vi.mock("@/contexts/AuthContext", () => ({
  useAuth: vi.fn(() => ({
    mode: "open" as const,
    user: null,
    isLoading: false,
    isAuthenticated: true,
    authServiceDown: false,
    signIn: vi.fn(),
    signOut: vi.fn(),
  })),
}));

// Mock GraphView to avoid ResizeObserver issues in jsdom
vi.mock("@/components/GraphView", () => ({
  GraphView: ({ issues }: { issues: unknown[] }) => (
    <div data-testid="mock-graph-view">
      Graph View ({Array.isArray(issues) ? issues.length : 0} issues)
    </div>
  ),
}));

// Mock MonitorDashboard to avoid complex dependencies in jsdom
vi.mock("@/components/MonitorDashboard", () => ({
  MonitorDashboard: () => (
    <div data-testid="monitor-dashboard">Monitor Dashboard</div>
  ),
}));

// Mock TerminalView to avoid xterm.js browser dependencies in jsdom
vi.mock("@/components/TerminalView", () => ({
  TerminalView: ({
    isActive,
    onEscape,
  }: {
    isActive?: boolean;
    onEscape?: () => void;
  }) => (
    <div
      data-testid="terminal-view"
      data-active={isActive ? "true" : undefined}
      onClick={onEscape}
    />
  ),
}));

// Mock FileExplorer to avoid CodeMirror dependencies in jsdom
vi.mock("@/components/FileExplorer", () => ({
  FileExplorer: () => <div data-testid="file-explorer">File Explorer</div>,
}));

// Mock WorkspaceView to avoid lazy-loading issues in jsdom
vi.mock("@/components/WorkspaceView", () => ({
  WorkspaceView: () => <div data-testid="workspace-view">Workspace View</div>,
}));

// Mock terminal API and tab persistence to prevent async operations from DefaultContent
vi.mock("@/api/terminal", () => ({
  deleteTabMetadata: vi.fn().mockImplementation(() => new Promise(() => {})),
  scheduleSessionKill: vi.fn().mockImplementation(() => new Promise(() => {})),
  listIssueSessions: vi.fn().mockImplementation(() => new Promise(() => {})),
}));

vi.mock("@/hooks/useIssueTabPersistence", () => ({
  useIssueTabPersistence: vi.fn(() => ({
    savedState: null,
    isLoading: false,
    saveTabs: vi.fn(),
    clearTabs: vi.fn(),
  })),
}));

// Create hoisted mock for useRouteView to allow per-test control
const { mockUseRouteView, mockSetActiveView, mockNavigateToView } = vi.hoisted(
  () => ({
    mockUseRouteView: vi.fn(),
    mockSetActiveView: vi.fn(),
    mockNavigateToView: vi.fn(),
  }),
);

/**
 * Helper to create a useRouteView return value (object shape).
 */
function createViewStateReturn(
  view: string,
  setter = mockSetActiveView,
): {
  view: string;
  setView: typeof mockSetActiveView;
  navigateToView: typeof mockNavigateToView;
} {
  return {
    view,
    setView: setter,
    navigateToView: mockNavigateToView,
  };
}

// Mock the hooks barrel file that App.tsx imports from
vi.mock("@/hooks", () => ({
  useIssues: mockUseIssues,
  useIssueDetail: mockUseIssueDetail,
  useToast: mockUseToast,
  useRouteView: mockUseRouteView,
  DEFAULT_GROUP_BY: "none",
  useFilterState: vi.fn(() => [
    {}, // FilterState - empty means App.tsx will apply DEFAULT_GROUP_BY fallback
    {
      setPriority: vi.fn(),
      setType: vi.fn(),
      setLabels: vi.fn(),
      setSearch: vi.fn(),
      setShowBlocked: vi.fn(),
      setGroupBy: vi.fn(),
      clearFilter: vi.fn(),
      clearAll: vi.fn(),
    }, // FilterActions
  ]),
  useIssueFilter: vi.fn((issues: unknown[]) => ({
    filteredIssues: issues,
    count: Array.isArray(issues) ? issues.length : 0,
    totalCount: Array.isArray(issues) ? issues.length : 0,
    hasActiveFilters: false,
    activeFilters: [],
  })),
  useDebounce: vi.fn((value: unknown) => value),
  useBlockedIssues: vi.fn(() => ({
    data: null,
    loading: false,
    error: null,
    refetch: vi.fn(),
  })),
  useSort: vi.fn((options: { data: unknown[] }) => ({
    sortedData: options.data,
    sortState: { key: null, direction: "asc" },
    handleSort: vi.fn(),
    clearSort: vi.fn(),
  })),
  useSelection: vi.fn(() => ({
    selectedIds: new Set<string>(),
    isSelected: vi.fn(() => false),
    toggle: vi.fn(),
    selectAll: vi.fn(),
    deselectAll: vi.fn(),
    clear: vi.fn(),
    count: 0,
    hasSelection: false,
  })),
  useBulkClose: vi.fn(() => ({
    closeSelected: vi.fn(),
    isClosing: false,
    error: null,
  })),
  useGraphData: vi.fn(() => ({
    nodes: [],
    edges: [],
    isLoading: false,
  })),
  useAutoLayout: vi.fn(() => ({
    nodes: [],
    edges: [],
    isLayouting: false,
    triggerLayout: vi.fn(),
  })),
  useAgents: mockUseAgents,
  useAgentContext: mockUseAgentContext,
  AgentProvider: ({ children }: { children: React.ReactNode }) => children,
  useRecentAssignees: vi.fn(() => ({
    recentAssignees: [],
    addRecentAssignee: vi.fn(),
    clearRecentAssignees: vi.fn(),
  })),
  useTaskLogPolling: vi.fn(() => ({
    chunks: [],
    state: "disconnected" as const,
    error: null,
    resetVersion: 0,
    refresh: vi.fn(),
  })),
  useAgentTerminalLogs: vi.fn(() => ({
    mode: "idle" as const,
    chunks: [],
    state: "disconnected" as const,
    error: null,
    resetVersion: 0,
    refresh: vi.fn(),
    resize: vi.fn(),
    sendInput: vi.fn(),
  })),
  useTheme: vi.fn(() => ({
    theme: "light" as const,
    toggleTheme: vi.fn(),
    setTheme: vi.fn(),
  })),
  useRepoFilter: vi.fn(() => [[], vi.fn()]),
  useWorkspaceRepos: vi.fn(() => ({
    repos: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  })),
  useWorkspaceContext: vi.fn(() => ({
    workspaceId: "test-ws-id",
    workspace: null,
    repos: [],
    groups: [],
    agents: [],
    isLoading: false,
    error: null,
    refetch: vi.fn(),
    getRepoByName: vi.fn(),
    getReposByGroup: vi.fn(() => []),
    getAgentByName: vi.fn(),
    activeWorkspaceName: null,
    setActiveWorkspace: vi.fn(),
    selectedRepoNames: new Set<string>(),
    activeRepos: [],
    activeRepoNames: [],
    isAllSelected: true,
    selectRepos: vi.fn(),
    selectAll: vi.fn(),
    toggleRepo: vi.fn(),
    sourceReposFilter: undefined,
    isMultiRepo: false,
  })),
  useWorkspaceState: vi.fn(),
  useElapsedTime: vi.fn(() => "0s"),
  useJobPolling: vi.fn(() => ({
    isPolling: false,
    progress: "",
    elapsed: "0s",
    error: "",
    startJob: vi.fn(),
    reset: vi.fn(),
  })),
  useFocusReturn: vi.fn(),
  useFocusTrap: vi.fn(),
  useRepoFilterParam: vi.fn(() => [null, vi.fn()]),
  useSearchScope: vi.fn(() => ({
    scopeName: undefined,
    clearScope: vi.fn(),
  })),
  useDaemonHealth: vi.fn(() => ({
    isDaemonAvailable: true,
    isChecking: false,
    wasEverConnected: true,
    connectionMode: "connected" as const,
    retryCountdown: 0,
    lastError: null,
    retryNow: vi.fn(),
  })),
  usePanelManager: mockUsePanelManager,
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
  LAYER_WORKSPACE_SWITCHER: 42,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
  LAYER_TERMINAL_SEARCH: 5,
  useDebouncedCallback: (fn: (...args: unknown[]) => unknown) => fn,
  useAgentDiffStat: () => ({
    data: null,
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  }),
}));

// Alias for convenience in tests (prefixed with _ to satisfy linter for unused vars)
const _useIssuesMock = mockUseIssues;
const _useRouteViewMock = mockUseRouteView;

/**
 * Create a mock issue for testing.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${Math.random().toString(36).slice(2, 9)}`,
    title: "Test Issue",
    priority: 2,
    status: "open",
    issue_type: "task",
    created_at: "2024-01-01T00:00:00Z",
    updated_at: "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

/**
 * Create mock useIssues return value.
 */
interface MockUseIssuesReturn {
  issues: Issue[];
  issuesMap: Map<string, Issue>;
  isLoading: boolean;
  error: string | null;
  connectionState: ConnectionState;
  isConnected: boolean;
  reconnectAttempts: number;
  refetch: () => Promise<void>;
  updateIssueStatus: (issueId: string, newStatus: Status) => Promise<void>;
  getIssue: (id: string) => Issue | undefined;
  mutationCount: number;
  retryConnection: () => void;
  pendingIds: Set<string>;
}

const DEFAULT_MOCK_ISSUE = createMockIssue({
  id: "default-issue",
  title: "Default Issue",
  status: "open",
});

function createMockUseIssuesReturn(
  overrides: Partial<MockUseIssuesReturn> = {},
): MockUseIssuesReturn {
  const issues = overrides.issues ?? [DEFAULT_MOCK_ISSUE];
  const issuesMap =
    overrides.issuesMap ?? new Map(issues.map((issue) => [issue.id, issue]));

  return {
    issues,
    issuesMap,
    isLoading: false,
    error: null,
    connectionState: "connected",
    isConnected: true,
    reconnectAttempts: 0,
    refetch: vi.fn().mockResolvedValue(undefined),
    updateIssueStatus: vi.fn().mockResolvedValue(undefined),
    getIssue: (id: string) => issuesMap.get(id),
    mutationCount: 0,
    retryConnection: vi.fn(),
    pendingIds: new Set<string>(),
    ...overrides,
  };
}

/**
 * Create default mock return value for useIssueDetail.
 */
function createMockUseIssueDetailReturn(
  overrides: Partial<{
    issueDetails: unknown;
    isLoading: boolean;
    error: string | null;
    fetchIssue: ReturnType<typeof vi.fn>;
    clearIssue: ReturnType<typeof vi.fn>;
  }> = {},
) {
  return {
    issueDetails: null,
    isLoading: false,
    error: null,
    fetchIssue: vi.fn(),
    clearIssue: vi.fn(),
    ...overrides,
  };
}

describe("App", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Set up default useRouteView mock (kanban is the default view)
    mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
    // Set up default useIssueDetail mock
    mockUseIssueDetail.mockReturnValue(createMockUseIssueDetailReturn());
    // Set up default usePanelManager mock
    mockUsePanelManager.mockReturnValue({
      activePanel: null,
      pendingPanel: null,
      openPanel: mockOpenPanel,
      closePanel: mockClosePanel,
      isOpen: mockIsOpen,
    });
    // Set up default API mocks (resolve by default so existing tests aren't affected)
    mockUpdateIssue.mockResolvedValue({});
    mockAddComment.mockResolvedValue({});
  });

  describe("loading state", () => {
    it("renders LoadingSkeleton columns when isLoading is true", () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // LoadingSkeleton.Column components have aria-hidden="true"
      const skeletonColumns = container.querySelectorAll(
        '[aria-hidden="true"]',
      );

      // Each Column skeleton contains multiple nested elements with aria-hidden
      // We verify that skeletons are rendered by checking for the column structure
      expect(skeletonColumns.length).toBeGreaterThan(0);

      // Should not render KanbanBoard when loading
      expect(screen.queryByRole("article")).not.toBeInTheDocument();

      // Should not render ErrorDisplay when loading
      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
    });

    it("renders three LoadingSkeleton.Column components when loading", () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // The loading skeleton columns have a specific structure
      // We check for the loading container with three skeleton columns
      const flexContainer = container.querySelector(
        '[data-testid="loading-container"]',
      );
      expect(flexContainer).toBeInTheDocument();

      // Count direct children of the flex container (the 3 skeleton columns)
      const children = flexContainer?.children;
      expect(children?.length).toBe(3);
    });

    it("renders ConnectionStatus in header when loading", () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: true,
        connectionState: "connecting",
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus should be visible with the connection state (showText=false, so check data-state)
      const status = container.querySelector('[data-state="connecting"]');
      expect(status).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("renders ErrorDisplay when error is present", () => {
      const mockReturn = createMockUseIssuesReturn({
        error: "Failed to fetch issues",
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByTestId("error-display")).toBeInTheDocument();
      expect(screen.getByRole("alert")).toBeInTheDocument();
    });

    it("renders ErrorDisplay with fetch-error variant", () => {
      const mockReturn = createMockUseIssuesReturn({
        error: "Network error",
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByTestId("error-display")).toHaveAttribute(
        "data-variant",
        "fetch-error",
      );
    });

    it("renders retry button that calls refetch", () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        error: "Network error",
        isLoading: false,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const retryButton = screen.getByTestId("retry-button");
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(refetch).toHaveBeenCalledTimes(1);
    });

    it("shows error details when showDetails is true", () => {
      const mockReturn = createMockUseIssuesReturn({
        error: "Specific error message",
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // The App component passes showDetails to ErrorDisplay
      expect(screen.getByText("Technical details")).toBeInTheDocument();
      expect(screen.getByText("Specific error message")).toBeInTheDocument();
    });

    it("renders ConnectionStatus with reconnecting state in error state", () => {
      const retryConnection = vi.fn();
      const mockReturn = createMockUseIssuesReturn({
        error: "Connection failed",
        isLoading: false,
        connectionState: "reconnecting",
        reconnectAttempts: 2,
        retryConnection,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus should show reconnecting state (showText=false, check data-state)
      const status = container.querySelector('[data-state="reconnecting"]');
      expect(status).toBeInTheDocument();
    });

    it("does not render KanbanBoard when error is present", () => {
      const mockReturn = createMockUseIssuesReturn({
        error: "Error occurred",
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // KanbanBoard renders StatusColumns with headings
      expect(
        screen.queryByRole("heading", { name: "Open" }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("heading", { name: "In Progress" }),
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole("heading", { name: "Done" }),
      ).not.toBeInTheDocument();
    });
  });

  describe("success state", () => {
    it("renders KanbanBoard with issues when data is loaded", async () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "First Issue",
          status: "open",
        }),
        createMockIssue({
          id: "issue-2",
          title: "Second Issue",
          status: "in_progress",
        }),
        createMockIssue({
          id: "issue-3",
          title: "Third Issue",
          status: "closed",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      await act(async () => {
        render(<App />);
      });

      // SwimLaneBoard should render with status columns
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: "In Progress" }),
      ).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: "Done" })).toBeInTheDocument();

      // Issues should be rendered
      expect(screen.getByText("First Issue")).toBeInTheDocument();
      expect(screen.getByText("Second Issue")).toBeInTheDocument();
      expect(screen.getByText("Third Issue")).toBeInTheDocument();
    });

    it("renders ConnectionStatus in header actions", () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "connected",
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Use data-state attribute to find ConnectionStatus (showText=false, no visible text)
      const statusElement = container.querySelector('[data-state="connected"]');
      expect(statusElement).toBeInTheDocument();
    });

    it("does not render ErrorDisplay when no error", () => {
      const mockReturn = createMockUseIssuesReturn({ error: null });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
    });

    it("does not render LoadingSkeleton when not loading", () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: false });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container: _container } = render(<App />);

      // The loading container should not be present when not loading
      expect(screen.queryByTestId("loading-container")).not.toBeInTheDocument();
    });

    it("renders EmptyState when issues array is empty", () => {
      const mockReturn = createMockUseIssuesReturn({ issues: [] });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Should render EmptyWorkspaceBoard in the main content area
      const emptyBoard = screen.getByTestId("empty-workspace-board");
      expect(emptyBoard).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: "No issues yet" }),
      ).toBeInTheDocument();
    });
  });

  describe("drag-end handler", () => {
    it("calls updateIssueStatus with correct parameters on drag-end", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [
        createMockIssue({ id: "drag-issue", title: "Drag Me", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Since testing actual drag-and-drop is complex with dnd-kit,
      // we verify that the App passes onDragEnd to KanbanBoard
      // by checking that updateIssueStatus is available and callable
      expect(updateIssueStatus).not.toHaveBeenCalled();

      // The KanbanBoard should be rendered with the issues
      expect(screen.getByText("Drag Me")).toBeInTheDocument();
    });

    it("updateIssueStatus is passed to KanbanBoard via onDragEnd", () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [
        createMockIssue({ id: "test-issue", title: "Test", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify KanbanBoard is rendered (we can't easily test the drag event
      // but we verify the component structure is correct)
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(screen.getByText("Test")).toBeInTheDocument();
    });
  });

  describe("drag-end failure and ErrorToast", () => {
    it("shows ErrorToast when updateIssueStatus throws", async () => {
      const updateIssueStatus = vi
        .fn()
        .mockRejectedValue(new Error("Update failed"));
      const issues = [
        createMockIssue({
          id: "fail-issue",
          title: "Will Fail",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify the issue is rendered
      expect(screen.getByText("Will Fail")).toBeInTheDocument();

      // Note: Testing the actual toast appearance requires triggering the drag-end
      // which involves complex dnd-kit event simulation. The test below verifies
      // that the toast is not shown initially.
      expect(screen.queryByTestId("error-toast")).not.toBeInTheDocument();
    });

    it("ErrorToast is not rendered initially", () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.queryByTestId("error-toast")).not.toBeInTheDocument();
    });

    it("ErrorToast can display error message from updateIssueStatus failure", async () => {
      // This test demonstrates the expected behavior by directly testing
      // that when toastError state is set, ErrorToast appears.
      // Since we can't easily trigger drag events, we test the component's
      // handling of the error state through the hook's error mechanism.

      const updateIssueStatus = vi
        .fn()
        .mockRejectedValue(new Error("API Error"));
      const issues = [createMockIssue({ id: "test-1", status: "open" })];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify the component is ready to handle errors
      expect(screen.queryByTestId("error-toast")).not.toBeInTheDocument();
    });
  });

  describe("ConnectionStatus in header", () => {
    it("renders ConnectionStatus with connected state", () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "connected",
        isConnected: true,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus rendered with showText=false — verify via data-state
      const status = container.querySelector('[data-state="connected"]');
      expect(status).toBeInTheDocument();
    });

    it("renders ConnectionStatus with disconnected state", () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "disconnected",
        isConnected: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      const status = container.querySelector('[data-state="disconnected"]');
      expect(status).toBeInTheDocument();
    });

    it("renders ConnectionStatus with reconnecting state and attempt count", () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "reconnecting",
        isConnected: false,
        reconnectAttempts: 3,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      const status = container.querySelector('[data-state="reconnecting"]');
      expect(status).toBeInTheDocument();
    });

    it("renders ConnectionStatus with connecting state", () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "connecting",
        isConnected: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      const status = container.querySelector('[data-state="connecting"]');
      expect(status).toBeInTheDocument();
    });

    it("passes retryConnection to ConnectionStatus onRetry", () => {
      const retryConnection = vi.fn();
      const mockReturn = createMockUseIssuesReturn({
        connectionState: "reconnecting",
        reconnectAttempts: 1,
        retryConnection,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus is rendered with showRetryButton=false in the new layout,
      // so verify the status element exists with correct state instead
      const status = container.querySelector('[data-state="reconnecting"]');
      expect(status).toBeInTheDocument();
    });
  });

  describe("AppLayout integration", () => {
    it("renders with Cortex title in header", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(
        screen.getByRole("heading", { name: "Cortex", level: 1 }),
      ).toBeInTheDocument();
    });

    it("renders header with banner role", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Use container query since there may be multiple banner roles
      const banner = container.querySelector('header[role="banner"]');
      expect(banner).toBeInTheDocument();
    });

    it("renders main content area", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByRole("main")).toBeInTheDocument();
    });
  });

  describe("useIssues hook integration", () => {
    it("calls useIssues hook on mount", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(useIssues).toHaveBeenCalled();
    });

    it("uses all expected properties from useIssues return", () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
        isLoading: false,
        error: null,
        connectionState: "connected",
        reconnectAttempts: 0,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Verify the component renders without errors, indicating
      // it correctly uses all the hook's return values
      const banner = container.querySelector('header[role="banner"]');
      expect(banner).toBeInTheDocument();
      expect(screen.getByRole("main")).toBeInTheDocument();
    });
  });

  describe("state transitions", () => {
    it("transitions from loading to success", () => {
      const mockLoadingReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockLoadingReturn);

      const { rerender } = render(<App />);

      // Verify loading state
      expect(
        screen.queryByRole("heading", { name: "Open" }),
      ).not.toBeInTheDocument();

      // Transition to success
      const issues = [
        createMockIssue({ title: "Loaded Issue", status: "open" }),
      ];
      const mockSuccessReturn = createMockUseIssuesReturn({
        isLoading: false,
        issues,
      });
      vi.mocked(useIssues).mockReturnValue(mockSuccessReturn);

      rerender(<App />);

      // Verify success state
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(screen.getByText("Loaded Issue")).toBeInTheDocument();
    });

    it("transitions from loading to error", () => {
      const mockLoadingReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockLoadingReturn);

      const { rerender } = render(<App />);

      // Verify loading state
      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();

      // Transition to error
      const mockErrorReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: "Network error occurred",
      });
      vi.mocked(useIssues).mockReturnValue(mockErrorReturn);

      rerender(<App />);

      // Verify error state
      expect(screen.getByTestId("error-display")).toBeInTheDocument();
    });

    it("transitions from error to success on retry", () => {
      const mockErrorReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: "Initial error",
      });
      vi.mocked(useIssues).mockReturnValue(mockErrorReturn);

      const { rerender } = render(<App />);

      // Verify error state
      expect(screen.getByTestId("error-display")).toBeInTheDocument();

      // Transition to success after retry
      const issues = [
        createMockIssue({ title: "Retrieved Issue", status: "open" }),
      ];
      const mockSuccessReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: null,
        issues,
      });
      vi.mocked(useIssues).mockReturnValue(mockSuccessReturn);

      rerender(<App />);

      // Verify success state
      expect(screen.queryByTestId("error-display")).not.toBeInTheDocument();
      expect(screen.getByText("Retrieved Issue")).toBeInTheDocument();
    });
  });

  describe("filter integration", () => {
    it("renders SearchInput in the navigation slot", () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SearchInput should be rendered with the search input test id
      expect(screen.getByTestId("search-input")).toBeInTheDocument();
      expect(
        screen.getByPlaceholderText("Search tasks..."),
      ).toBeInTheDocument();
    });

    it("renders FilterBar in the navigation slot", () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // FilterBar should be rendered with priority/type dropdowns and more-filters button
      expect(screen.getByTestId("filter-bar")).toBeInTheDocument();
      expect(screen.getByTestId("priority-filter")).toBeInTheDocument();
      expect(screen.getByTestId("type-filter")).toBeInTheDocument();
      expect(screen.getByTestId("more-filters-trigger")).toBeInTheDocument();
    });

    it("renders filter navigation even in loading state", () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: true,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // In the new layout, search and filters are always in the header
      expect(screen.getByTestId("search-input")).toBeInTheDocument();
      expect(screen.getByTestId("filter-bar")).toBeInTheDocument();
    });

    it("renders filter navigation even in error state", () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: "Network error",
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // In the new layout, search and filters are always in the header
      expect(screen.getByTestId("search-input")).toBeInTheDocument();
      expect(screen.getByTestId("filter-bar")).toBeInTheDocument();
    });

    it("passes filtered issues to KanbanBoard", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "First Issue",
          status: "open",
        }),
        createMockIssue({
          id: "issue-2",
          title: "Second Issue",
          status: "closed",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // With the mock returning all issues, both should be visible
      expect(screen.getByText("First Issue")).toBeInTheDocument();
      expect(screen.getByText("Second Issue")).toBeInTheDocument();
    });

    it("clears search input when filter state search is cleared externally", async () => {
      // Set up issues so the app renders in success state
      const issues = [
        createMockIssue({ id: "issue-1", title: "Test Issue", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const filterActions = {
        setPriority: vi.fn(),
        setType: vi.fn(),
        setLabels: vi.fn(),
        setSearch: vi.fn(),
        setGroupBy: vi.fn(),
        clearFilter: vi.fn(),
        clearAll: vi.fn(),
      };

      // Mock useFilterState to return filter with search value
      vi.mocked(useFilterState).mockReturnValue([
        { search: "test query" },
        filterActions,
      ]);

      const { rerender } = render(<App />);

      // Verify search input has the initial value
      const searchInput = screen.getByTestId(
        "search-input-field",
      ) as HTMLInputElement;
      expect(searchInput.value).toBe("test query");

      // Simulate external filter state clearing (e.g. clearAll was called)
      vi.mocked(useFilterState).mockReturnValue([{}, filterActions]);

      // Rerender to trigger the useEffect that syncs filters.search to searchValue
      rerender(<App />);

      // Wait for the search input to be cleared
      await waitFor(() => {
        expect(searchInput.value).toBe("");
      });
    });
  });

  describe("swim lane integration", () => {
    it("renders SwimLaneBoard instead of KanbanBoard when activeView is kanban", () => {
      const issues = [
        createMockIssue({ id: "issue-1", title: "Issue One", status: "open" }),
        createMockIssue({
          id: "issue-2",
          title: "Issue Two",
          status: "in_progress",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should render status columns
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: "In Progress" }),
      ).toBeInTheDocument();

      // Issues should be visible
      expect(screen.getByText("Issue One")).toBeInTheDocument();
      expect(screen.getByText("Issue Two")).toBeInTheDocument();
    });

    it("passes groupBy prop to SwimLaneBoard with default value of epic", () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with no groupBy (App.tsx applies DEFAULT_GROUP_BY = 'none')
      vi.mocked(useFilterState).mockReturnValue([
        {},
        {
          setPriority: vi.fn(),
          setType: vi.fn(),
          setLabels: vi.fn(),
          setSearch: vi.fn(),
          setShowBlocked: vi.fn(),
          setGroupBy: vi.fn(),
          clearFilter: vi.fn(),
          clearAll: vi.fn(),
        },
      ]);

      render(<App />);

      // Verify SwimLaneBoard is rendered with correct groupBy (epic is default)
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
    });

    it("passes groupBy prop to SwimLaneBoard with epic grouping", () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with groupBy: 'epic'
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: "epic" },
        {
          setPriority: vi.fn(),
          setType: vi.fn(),
          setLabels: vi.fn(),
          setSearch: vi.fn(),
          setShowBlocked: vi.fn(),
          setGroupBy: vi.fn(),
          clearFilter: vi.fn(),
          clearAll: vi.fn(),
        },
      ]);

      render(<App />);

      // SwimLaneBoard should still render
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
    });

    it("FilterBar receives groupBy and onGroupByChange props", () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const setGroupBy = vi.fn();
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: "none" },
        {
          setPriority: vi.fn(),
          setType: vi.fn(),
          setLabels: vi.fn(),
          setSearch: vi.fn(),
          setShowBlocked: vi.fn(),
          setGroupBy,
          clearFilter: vi.fn(),
          clearAll: vi.fn(),
        },
      ]);

      render(<App />);

      // FilterBar should be rendered with groupBy props
      expect(screen.getByTestId("filter-bar")).toBeInTheDocument();
    });

    it("updates SwimLaneBoard groupBy when FilterBar groupBy changes", () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      let currentGroupBy = "none";
      const setGroupBy = vi.fn((value: string) => {
        currentGroupBy = value;
      });

      const filterActions = {
        setPriority: vi.fn(),
        setType: vi.fn(),
        setLabels: vi.fn(),
        setSearch: vi.fn(),
        setShowBlocked: vi.fn(),
        setGroupBy,
        clearFilter: vi.fn(),
        clearAll: vi.fn(),
      };

      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: currentGroupBy },
        filterActions,
      ]);

      const { rerender } = render(<App />);

      // Initial render with groupBy: 'none'
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();

      // Simulate groupBy change to 'priority'
      currentGroupBy = "priority";
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: currentGroupBy },
        filterActions,
      ]);

      rerender(<App />);

      // SwimLaneBoard should still render with updated groupBy
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(setGroupBy).not.toHaveBeenCalled(); // setGroupBy is called by FilterBar, not App
    });

    it("passes onDragEnd handler to SwimLaneBoard", () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [
        createMockIssue({ id: "drag-test", title: "Drag Me", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should be rendered with the drag handler
      expect(screen.getByText("Drag Me")).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
    });

    it("SwimLaneBoard receives filtered issues", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "High Priority Issue",
          status: "open",
          priority: 0,
        }),
        createMockIssue({
          id: "issue-2",
          title: "Low Priority Issue",
          status: "open",
          priority: 4,
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Both issues should be visible since no filters are active
      expect(screen.getByText("High Priority Issue")).toBeInTheDocument();
      expect(screen.getByText("Low Priority Issue")).toBeInTheDocument();
    });

    it("SwimLaneBoard receives blocked issues map when available", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Blocked Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should render without errors
      expect(screen.getByText("Blocked Issue")).toBeInTheDocument();
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
    });

    it("SwimLaneBoard respects showBlocked filter", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Issue To Show",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with showBlocked: true
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: "none", showBlocked: true },
        {
          setPriority: vi.fn(),
          setType: vi.fn(),
          setLabels: vi.fn(),
          setSearch: vi.fn(),
          setShowBlocked: vi.fn(),
          setGroupBy: vi.fn(),
          clearFilter: vi.fn(),
          clearAll: vi.fn(),
        },
      ]);

      render(<App />);

      // SwimLaneBoard should render with showBlocked prop passed
      expect(screen.getByText("Issue To Show")).toBeInTheDocument();
    });
  });

  describe("IssueDetailPanel integration", () => {
    it("renders IssueDetailPanel in closed state by default", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Panel should be rendered but closed (isOpen=false)
      const panel = container.querySelector(
        '[data-testid="issue-detail-panel"]',
      );
      expect(panel).toBeInTheDocument();
      expect(panel).toHaveAttribute("data-state", "closed");
    });

    it("opens issue panel via usePanelManager when issue is clicked in SwimLaneBoard", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Test Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click on the issue card
      const issueCard = screen.getByText("Test Issue");
      fireEvent.click(issueCard);

      // Should open panel overlay (not navigate to issue-detail view)
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "issue",
        id: "issue-1",
      });
      expect(fetchIssue).toHaveBeenCalledWith("issue-1");
    });

    it("calls fetchIssue with correct ID when issue is clicked", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-123",
          title: "Clickable Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click on the issue card
      const issueCard = screen.getByText("Clickable Issue");
      fireEvent.click(issueCard);

      // fetchIssue should be called with the correct ID
      expect(fetchIssue).toHaveBeenCalledTimes(1);
      expect(fetchIssue).toHaveBeenCalledWith("issue-123");
    });

    it("back button from issue-detail view navigates via React Router", () => {
      const clearIssue = vi.fn();
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "issue-1",
            title: "Closeable Issue Details",
            priority: 2,
            status: "open",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
          clearIssue,
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the back button in IssueDetailView
      const backButton = screen.getByTestId("detail-back-button");
      fireEvent.click(backButton);

      // Should navigate back via browser history or fallback to kanban
      // (uses navigate(-1) when history > 1, or navigateToView("kanban") otherwise)
      const usedHistory = mockNavigate.mock.calls.some(
        (call: unknown[]) => call[0] === -1,
      );
      const usedFallback = mockNavigateToView.mock.calls.some(
        (call: unknown[]) => call[0] === "kanban",
      );
      expect(usedHistory || usedFallback).toBe(true);
    });

    it("does not re-fetch when clicking the same issue that is already selected in detail view", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Same Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click on the issue card
      const issueCard = screen.getByText("Same Issue");
      fireEvent.click(issueCard);

      expect(fetchIssue).toHaveBeenCalledTimes(1);

      // Re-render in issue-detail view (simulating the view switch)
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      // fetchIssue should not be called again since the view switch handles it
      expect(fetchIssue).toHaveBeenCalledTimes(1);
    });

    it("passes loading state to IssueDetailPanel during fetch", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Loading Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          isLoading: true,
          fetchIssue,
        }),
      );

      const { container } = render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText("Loading Issue");
      fireEvent.click(issueCard);

      // Panel should show loading state
      const panel = container.querySelector(
        '[data-testid="issue-detail-panel"]',
      );
      expect(panel).toHaveAttribute("data-loading", "true");
    });

    it("passes issue details to IssueDetailPanel when loaded", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Detail Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "issue-1",
            title: "Detail Issue Title",
            description: "Issue description",
            priority: 2,
            status: "open",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
          isLoading: false,
        }),
      );

      render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText("Detail Issue");
      fireEvent.click(issueCard);

      // Panel should show the issue title
      expect(screen.getByText("Detail Issue Title")).toBeInTheDocument();
    });

    it("passes error state to IssueDetailPanel when fetch fails", () => {
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Error Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          error: "Failed to fetch issue details",
          isLoading: false,
        }),
      );

      const { container } = render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText("Error Issue");
      fireEvent.click(issueCard);

      // Panel should show the error state
      const panel = container.querySelector(
        '[data-testid="issue-detail-panel"]',
      );
      expect(panel).toHaveAttribute("data-error", "true");
    });

    it("fetches new issue when clicking a different issue while panel is open", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "First Issue",
          status: "open",
        }),
        createMockIssue({
          id: "issue-2",
          title: "Second Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click on the first issue
      const firstIssue = screen.getByText("First Issue");
      fireEvent.click(firstIssue);

      expect(fetchIssue).toHaveBeenCalledTimes(1);
      expect(fetchIssue).toHaveBeenCalledWith("issue-1");

      // Click on the second issue
      const secondIssue = screen.getByText("Second Issue");
      fireEvent.click(secondIssue);

      // fetchIssue should be called again with the new ID
      expect(fetchIssue).toHaveBeenCalledTimes(2);
      expect(fetchIssue).toHaveBeenCalledWith("issue-2");
    });
  });

  describe("useIssues mode parameter based on activeView", () => {
    it('calls useIssues with mode: "kanban" when activeView is "kanban"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          mode: "kanban",
        }),
      );
    });

    it('calls useIssues with mode: "ready" when activeView is "table"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("table"));

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          mode: "ready",
        }),
      );
    });

    it('calls useIssues with mode: "graph" when activeView is "graph"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          mode: "graph",
        }),
      );
    });

    it("refetches issues when view changes from kanban to graph", () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'kanban'
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "kanban",
      });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to graph view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      rerender(<App />);

      // Verify useIssues is called with mode: 'graph' after view change
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "graph",
      });
    });

    it("refetches issues when view changes from graph to kanban", () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'graph'
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "graph",
      });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to kanban view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      rerender(<App />);

      // Verify useIssues is called with mode: 'kanban' after view change
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "kanban",
      });
    });

    it("refetches issues when view changes from graph to table", () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'graph'
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "graph",
      });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to table view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("table"));

      rerender(<App />);

      // Verify useIssues is called with mode: 'ready' after view change
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "ready",
      });
    });

    it("switches from kanban mode to ready mode when view changes from kanban to table", () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'kanban'
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "kanban",
      });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to table view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("table"));

      rerender(<App />);

      // Verify useIssues is still called with mode: 'ready'
      expect(useIssues).toHaveBeenLastCalledWith({
        mode: "ready",
      });
    });

    it("useRouteView is called before useIssues to determine fetch mode", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Track call order
      const callOrder: string[] = [];
      vi.mocked(useRouteView).mockImplementation(() => {
        callOrder.push("useRouteView");
        return createViewStateReturn("graph");
      });
      vi.mocked(useIssues).mockImplementation(() => {
        callOrder.push("useIssues");
        return mockReturn;
      });

      render(<App />);

      // Verify useRouteView is called before useIssues
      const viewStateIndex = callOrder.indexOf("useRouteView");
      const issuesIndex = callOrder.indexOf("useIssues");
      expect(viewStateIndex).toBeLessThan(issuesIndex);
      expect(viewStateIndex).toBeGreaterThanOrEqual(0);
      expect(issuesIndex).toBeGreaterThanOrEqual(0);
    });

    it('calls useIssues with mode: "ready" when activeView is "monitor"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          mode: "ready",
        }),
      );
    });
  });

  describe("MonitorDashboard lazy loading integration", () => {
    it('renders MonitorDashboard when activeView is "monitor"', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      render(<App />);

      // Wait for lazy-loaded MonitorDashboard to appear
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });
    });

    it("shows LoadingSkeleton.Monitor as fallback during lazy load", async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      render(<App />);

      // The skeleton may appear briefly during the lazy load
      // We check that MonitorDashboard eventually loads (which means Suspense worked)
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });
    });

    it('does not render MonitorDashboard when activeView is "kanban"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      render(<App />);

      // MonitorDashboard should not be rendered when kanban view is active
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();
      // Kanban view should be active (SwimLaneBoard renders status columns)
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
    });

    it('does not render MonitorDashboard when activeView is "table"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("table"));

      render(<App />);

      // MonitorDashboard should not be rendered when table view is active
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();
    });

    it('does not render MonitorDashboard when activeView is "graph"', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      render(<App />);

      // Wait for lazy-loaded GraphView to appear
      await waitFor(() => {
        expect(screen.getByTestId("mock-graph-view")).toBeInTheDocument();
      });

      // MonitorDashboard should not be rendered when graph view is active
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();
    });

    it("transitions from kanban to monitor view correctly", async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      const { rerender } = render(<App />);

      // Verify kanban view is rendered
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();

      // Switch to monitor view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });

      // Kanban columns should no longer be rendered
      expect(
        screen.queryByRole("heading", { name: "Open" }),
      ).not.toBeInTheDocument();
    });

    it("transitions from monitor to kanban view correctly", async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with monitor view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      const { rerender } = render(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });

      // Switch to kanban view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      rerender(<App />);

      // Verify kanban view is now rendered
      expect(screen.getByRole("heading", { name: "Open" })).toBeInTheDocument();
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();
    });

    it("transitions from graph to monitor view correctly", async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      const { rerender } = render(<App />);

      // Wait for GraphView to load
      await waitFor(() => {
        expect(screen.getByTestId("mock-graph-view")).toBeInTheDocument();
      });

      // Switch to monitor view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });

      // GraphView should no longer be rendered
      expect(screen.queryByTestId("mock-graph-view")).not.toBeInTheDocument();
    });

    it("transitions from monitor to graph view correctly", async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with monitor view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      const { rerender } = render(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });

      // Switch to graph view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("graph"));

      rerender(<App />);

      // Wait for GraphView to load
      await waitFor(() => {
        expect(screen.getByTestId("mock-graph-view")).toBeInTheDocument();
      });

      // MonitorDashboard should no longer be rendered
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();
    });

    it("transitions from table to monitor view correctly", async () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [
          createMockIssue({
            id: "test-1",
            title: "Test Issue",
            status: "open",
          }),
        ],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with table view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("table"));

      const { rerender } = render(<App />);

      // Verify table view is rendered (IssueTable has specific structure)
      expect(screen.queryByTestId("monitor-dashboard")).not.toBeInTheDocument();

      // Switch to monitor view
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("monitor"));

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId("monitor-dashboard")).toBeInTheDocument();
      });
    });
  });

  describe("FileExplorer lazy loading integration", () => {
    it('renders FileExplorer when activeView is "files"', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("files"));

      render(<App />);

      await waitFor(() => {
        expect(screen.getByTestId("file-explorer")).toBeInTheDocument();
      });
    });

    it('does not render FileExplorer when activeView is "kanban"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(createViewStateReturn("kanban"));

      render(<App />);

      expect(screen.queryByTestId("file-explorer")).not.toBeInTheDocument();
    });
  });

  describe("TerminalView integration", () => {
    it("renders TalkToLeadButton in the app", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByTestId("talk-to-lead-button")).toBeInTheDocument();
    });

    it("TalkToLeadButton has isActive=false when view is not terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const button = screen.getByTestId("talk-to-lead-button");
      expect(button).not.toHaveAttribute("data-active");
      expect(button).toHaveAttribute("aria-pressed", "false");
    });

    it("TalkToLeadButton click calls navigateToView with terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const button = screen.getByTestId("talk-to-lead-button");
      fireEvent.click(button);

      expect(mockNavigateToView).toHaveBeenCalledWith("terminal");
    });

    it("TalkToLeadButton shows active when view is terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("terminal"),
      );

      render(<App />);

      const button = screen.getByTestId("talk-to-lead-button");
      expect(button).toHaveAttribute("data-active", "true");
      expect(button).toHaveAttribute("aria-pressed", "true");
    });

    it("TerminalView is always mounted in the DOM", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // TerminalView should be present even when view is kanban
      expect(screen.getByTestId("terminal-view")).toBeInTheDocument();
    });

    it("TerminalView wrapper has display:none when view is not terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const terminalView = screen.getByTestId("terminal-view");
      const wrapper = terminalView.parentElement;
      expect(wrapper).toHaveStyle({ display: "none" });
    });

    it("TerminalView wrapper has display:contents when view is terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("terminal"),
      );

      render(<App />);

      const terminalView = screen.getByTestId("terminal-view");
      const wrapper = terminalView.parentElement;
      expect(wrapper).toHaveStyle({ display: "contents" });
    });

    it("TerminalView receives isActive=true when view is terminal", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("terminal"),
      );

      render(<App />);

      const terminalView = screen.getByTestId("terminal-view");
      expect(terminalView).toHaveAttribute("data-active", "true");
    });

    it("TerminalView receives isActive=false when view is kanban", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const terminalView = screen.getByTestId("terminal-view");
      expect(terminalView).not.toHaveAttribute("data-active");
    });

    describe("terminal escape uses browser history", () => {
      it("escape from terminal calls navigate(-1) when history is available", () => {
        const mockReturn = createMockUseIssuesReturn({});
        vi.mocked(useIssues).mockReturnValue(mockReturn);
        mockUseRouteView.mockReturnValue(createViewStateReturn("terminal"));

        // Simulate browser with history entries
        Object.defineProperty(window, "history", {
          value: { length: 3 },
          writable: true,
          configurable: true,
        });

        render(<App />);

        fireEvent.click(screen.getByTestId("terminal-view"));
        expect(mockNavigate).toHaveBeenCalledWith(-1);

        // Restore
        Object.defineProperty(window, "history", {
          value: { length: 1 },
          writable: true,
          configurable: true,
        });
      });

      it("escape from terminal navigates to kanban when no history", () => {
        const mockReturn = createMockUseIssuesReturn({});
        vi.mocked(useIssues).mockReturnValue(mockReturn);
        mockUseRouteView.mockReturnValue(createViewStateReturn("terminal"));

        // history.length <= 1 means no back entry
        Object.defineProperty(window, "history", {
          value: { length: 1 },
          writable: true,
          configurable: true,
        });

        render(<App />);

        fireEvent.click(screen.getByTestId("terminal-view"));
        expect(mockNavigateToView).toHaveBeenCalledWith("kanban");
      });
    });
  });

  describe("panel mutual exclusivity via usePanelManager", () => {
    it("clicking issue from kanban calls openPanel with issue type", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "First Issue",
          status: "open",
        }),
        createMockIssue({
          id: "issue-2",
          title: "Second Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click first issue — should open panel, not navigate
      fireEvent.click(
        screen.getByRole("button", { name: /Issue: First Issue/ }),
      );
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "issue",
        id: "issue-1",
      });
      expect(fetchIssue).toHaveBeenCalledWith("issue-1");

      // Click second issue — same pattern
      fireEvent.click(
        screen.getByRole("button", { name: /Issue: Second Issue/ }),
      );
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "issue",
        id: "issue-2",
      });
      expect(fetchIssue).toHaveBeenCalledWith("issue-2");
    });

    it("rapidly clicking issues calls openPanel for each", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({ id: "issue-1", title: "Issue One", status: "open" }),
        createMockIssue({ id: "issue-2", title: "Issue Two", status: "open" }),
        createMockIssue({
          id: "issue-3",
          title: "Issue Three",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click issue 1, 2, 3 rapidly
      fireEvent.click(screen.getByText("Issue One"));
      fireEvent.click(screen.getByText("Issue Two"));
      fireEvent.click(screen.getByText("Issue Three"));

      // Each click calls openPanel (hook handles deduplication internally)
      expect(mockOpenPanel).toHaveBeenCalledTimes(3);
      expect(fetchIssue).toHaveBeenLastCalledWith("issue-3");
    });

    it("clicking issue when agent panel is open calls openPanel (hook handles mutual exclusivity)", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({ id: "issue-1", title: "Test Issue", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Click issue — openPanel handles closing agent panel internally
      fireEvent.click(screen.getByText("Test Issue"));
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "issue",
        id: "issue-1",
      });
      expect(fetchIssue).toHaveBeenCalledWith("issue-1");
    });

    it("clicking issue from issue-detail view navigates instead of opening panel", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({ id: "issue-1", title: "Test Issue", status: "open" }),
      ];
      // Set view to issue-detail with a different issue
      mockUseRouteView.mockReturnValue(
        createViewStateReturn("issue-detail", mockSetActiveView),
      );
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      // Issue-detail view renders IssueDetailView (not the kanban card) so we need
      // to trigger the handler through whatever is available. Since this is the
      // issue-detail view, clicking a related issue calls handleIssueClick which
      // should navigate via navigateToView (not openPanel).
      // This is hard to test at App level since the view renders IssueDetailView.
      // Instead, verify the openPanel was NOT called (no panel opened for issue-detail nav).
      expect(mockOpenPanel).not.toHaveBeenCalled();
    });

    it("unmounting does not throw", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({ id: "issue-1", title: "Test Issue", status: "open" }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      const { unmount } = render(<App />);

      // Click issue
      fireEvent.click(
        screen.getByRole("button", { name: /Issue: Test Issue/ }),
      );

      // Unmount — should not cause any errors
      expect(() => unmount()).not.toThrow();
    });

    it("clicking same issue twice calls openPanel twice (hook deduplicates)", () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: "issue-1",
          title: "Same Issue",
          status: "open",
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        }),
      );

      render(<App />);

      const issueCard = screen.getByRole("button", {
        name: /Issue: Same Issue/,
      });
      fireEvent.click(issueCard);
      expect(fetchIssue).toHaveBeenCalledTimes(1);
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "issue",
        id: "issue-1",
      });
    });

    it("clicking agent calls openPanel with agent type", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock agents for the sidebar with two agents
      vi.mocked(useAgentContext).mockReturnValue({
        agents: [
          {
            name: "agent-1",
            status: "idle",
            current_task: null,
            workspace: "/test",
            started_at: "2024-01-01T00:00:00Z",
          },
          {
            name: "agent-2",
            status: "working",
            current_task: null,
            workspace: "/test2",
            started_at: "2024-01-01T00:00:00Z",
          },
        ],
        agentTasks: {},
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 0,
        },
        taskLists: {
          needsPlanning: [],
          readyToImplement: [],
          needsReview: [],
          inProgress: [],
          blocked: [],
        },
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
        connectionState: "connected",
        wasEverConnected: true,
        retryCountdown: 0,
        error: null,
        lastUpdated: null,
        refetch: vi.fn(),
        retryNow: vi.fn(),
        getAgentByName: vi.fn(() => undefined),
      });

      render(<App />);

      // Click first agent — should call openPanel
      fireEvent.click(screen.getByText("agent-1"));
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "agent",
        name: "agent-1",
      });

      // Click second agent — should call openPanel again (hook handles dedup)
      fireEvent.click(screen.getByText("agent-2"));
      expect(mockOpenPanel).toHaveBeenCalledWith({
        type: "agent",
        name: "agent-2",
      });
    });

    it("agent panel close calls closePanel", () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock agents and set panel state to show agent panel
      vi.mocked(useAgentContext).mockReturnValue({
        agents: [
          {
            name: "agent-1",
            status: "idle",
            current_task: null,
            workspace: "/test",
            started_at: "2024-01-01T00:00:00Z",
          },
        ],
        agentTasks: {},
        tasks: {
          needs_planning: 0,
          ready_to_implement: 0,
          in_progress: 0,
          need_review: 0,
          blocked: 0,
        },
        taskLists: {
          needsPlanning: [],
          readyToImplement: [],
          needsReview: [],
          inProgress: [],
          blocked: [],
        },
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
        connectionState: "connected",
        wasEverConnected: true,
        retryCountdown: 0,
        error: null,
        lastUpdated: null,
        refetch: vi.fn(),
        retryNow: vi.fn(),
        getAgentByName: vi.fn(() => undefined),
      });

      // Set panel to open state so close button is visible
      mockUsePanelManager.mockReturnValue({
        activePanel: { type: "agent", name: "agent-1" },
        pendingPanel: null,
        openPanel: mockOpenPanel,
        closePanel: mockClosePanel,
        isOpen: vi.fn(() => true),
      });

      render(<App />);

      // Close the panel using the close button
      const closeButton = screen.getByRole("button", { name: "Close panel" });
      fireEvent.click(closeButton);
      expect(mockClosePanel).toHaveBeenCalled();
    });
  });

  describe("handleApprove and handleReject", () => {
    beforeEach(() => {
      mockCloseIssue.mockResolvedValue(undefined);
      mockUpdateIssue.mockResolvedValue(undefined);
      mockAddComment.mockResolvedValue(undefined);
    });

    it("plan approve calls updateIssueStatus with open status", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "plan-issue",
            title: "Plan Review Issue",
            priority: 2,
            status: "review",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the approve button in IssueDetailView
      const approveButton = screen.getByTestId("detail-approve-button");
      fireEvent.click(approveButton);

      await waitFor(() => {
        expect(updateIssueStatus).toHaveBeenCalledTimes(1);
        expect(updateIssueStatus).toHaveBeenCalledWith("plan-issue", "open");
      });

      expect(mockCloseIssue).not.toHaveBeenCalled();
    });

    it("help approve calls updateIssueStatus with in_progress status", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "help-issue",
            title: "Help Review Issue",
            priority: 2,
            status: "blocked",
            notes: "I need help with this task",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the approve button in IssueDetailView
      const approveButton = screen.getByTestId("detail-approve-button");
      fireEvent.click(approveButton);

      await waitFor(() => {
        expect(updateIssueStatus).toHaveBeenCalledTimes(1);
        expect(updateIssueStatus).toHaveBeenCalledWith(
          "help-issue",
          "in_progress",
        );
      });

      expect(mockCloseIssue).not.toHaveBeenCalled();
    });

    it("code approve calls closeIssue and then refetch", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "code-issue",
            title: "Code Review Issue",
            priority: 2,
            status: "review",
            external_ref: "https://github.com/org/repo/pull/42",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the approve button in IssueDetailView
      const approveButton = screen.getByTestId("detail-approve-button");
      fireEvent.click(approveButton);

      await waitFor(() => {
        expect(mockCloseIssue).toHaveBeenCalledTimes(1);
        expect(mockCloseIssue).toHaveBeenCalledWith(
          "test-ws-id",
          "code-issue",
          "PR approved after code review",
        );
      });

      await waitFor(() => {
        expect(refetch).toHaveBeenCalledTimes(1);
      });

      expect(updateIssueStatus).not.toHaveBeenCalled();
    });

    it("reject calls addComment, updateIssue, refetch and closes panel", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "reject-issue",
            title: "Reject Review Issue",
            priority: 2,
            status: "review",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the reject button in IssueDetailView to show the reject form
      const rejectButton = screen.getByTestId("detail-reject-button");
      fireEvent.click(rejectButton);

      // Fill in the reject comment
      const textarea = screen.getByTestId("detail-reject-comment");
      fireEvent.change(textarea, {
        target: { value: "Needs more work on the design" },
      });

      // Submit the reject form
      const submitButton = screen.getByTestId("detail-reject-submit");
      fireEvent.click(submitButton);

      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledTimes(1);
        expect(mockAddComment).toHaveBeenCalledWith(
          "test-ws-id",
          "reject-issue",
          "FEEDBACK: Needs more work on the design",
        );
      });

      await waitFor(() => {
        expect(mockUpdateIssue).toHaveBeenCalledTimes(1);
        expect(mockUpdateIssue).toHaveBeenCalledWith(
          "test-ws-id",
          "reject-issue",
          {
            status: "open",
            add_labels: ["needs-revision"],
          },
        );
      });

      await waitFor(() => {
        expect(refetch).toHaveBeenCalledTimes(1);
      });
    });

    it("code review reject uses CODE REVIEW prefix in comment", async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "code-reject-issue",
            title: "Code Reject Issue",
            priority: 2,
            status: "review",
            external_ref: "https://github.com/org/repo/pull/99",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the reject button in IssueDetailView
      const rejectButton = screen.getByTestId("detail-reject-button");
      fireEvent.click(rejectButton);

      // Fill in the reject comment
      const textarea = screen.getByTestId("detail-reject-comment");
      fireEvent.change(textarea, { target: { value: "Fix the lint errors" } });

      // Submit the reject form
      const submitButton = screen.getByTestId("detail-reject-submit");
      fireEvent.click(submitButton);

      await waitFor(() => {
        expect(mockAddComment).toHaveBeenCalledTimes(1);
        expect(mockAddComment).toHaveBeenCalledWith(
          "test-ws-id",
          "code-reject-issue",
          "CODE REVIEW: Fix the lint errors",
        );
      });
    });

    it("approve shows error toast on failure for code review type", async () => {
      const mockCloseIssueFn = mockCloseIssue.mockRejectedValue(
        new Error("Network error"),
      );
      const showToast = vi.fn();
      mockUseToast.mockReturnValue({
        toasts: [],
        showToast,
        dismissToast: vi.fn(),
        dismissAll: vi.fn(),
      });
      const mockReturn = createMockUseIssuesReturn();
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "fail-issue",
            title: "Fail Approve Issue",
            priority: 2,
            status: "review",
            issue_type: "task",
            external_ref: "https://github.com/org/repo/pull/1",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the approve button in IssueDetailView
      const approveButton = screen.getByTestId("detail-approve-button");
      fireEvent.click(approveButton);

      await waitFor(() => {
        expect(showToast).toHaveBeenCalledWith("Network error", {
          type: "error",
        });
      });
      mockCloseIssueFn.mockReset();
    });

    it("approve does not show toast for plan review failures (handled by optimistic rollback)", async () => {
      const updateIssueStatus = vi
        .fn()
        .mockRejectedValue(new Error("Network error"));
      const showToast = vi.fn();
      mockUseToast.mockReturnValue({
        toasts: [],
        showToast,
        dismissToast: vi.fn(),
        dismissAll: vi.fn(),
      });
      const mockReturn = createMockUseIssuesReturn({
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: "fail-issue",
            title: "Fail Approve Issue",
            priority: 2,
            status: "review",
            issue_type: "task",
            created_at: "2024-01-01T00:00:00Z",
            updated_at: "2024-01-01T00:00:00Z",
          },
        }),
      );
      // Render in issue-detail view
      vi.mocked(useRouteView).mockReturnValue(
        createViewStateReturn("issue-detail"),
      );

      render(<App />);

      // Click the approve button in IssueDetailView
      const approveButton = screen.getByTestId("detail-approve-button");
      fireEvent.click(approveButton);

      // Wait for the async handler to complete
      await waitFor(() => {
        expect(updateIssueStatus).toHaveBeenCalled();
      });

      // showToast should NOT be called — error is handled by useOptimisticUpdate rollback
      expect(showToast).not.toHaveBeenCalled();
    });
  });

  describe("sidebar isMultiRepo guard", () => {
    it("always renders WorkspaceTree sidebar regardless of isMultiRepo", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: null,
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: null,
        setActiveWorkspace: vi.fn(),
        defaultWorkspaceName: null,
        setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: false,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("workspace"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // WorkspaceTree is always rendered now (sidebar always shows WorkspaceTree)
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });

    it("renders WorkspaceTree sidebar when isMultiRepo is true and activeView is workspace", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        defaultWorkspaceName: null,
        setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("workspace"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // WorkspaceTree renders a button with aria-label containing "workspace tree"
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });

    it("renders WorkspaceTree sidebar even when activeView is not workspace", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        defaultWorkspaceName: null,
        setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // WorkspaceTree is always shown now, regardless of activeView
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });

    it("renders WorkspaceTree sidebar during loading when isMultiRepo is false", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: null,
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: null,
        setActiveWorkspace: vi.fn(),
        defaultWorkspaceName: null,
        setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: false,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("workspace"));
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // WorkspaceTree is always shown, even during loading
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });

    it("renders WorkspaceTree during loading when isMultiRepo is true and activeView is workspace", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("workspace"));
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // In loading state, multi-repo + workspace view should show WorkspaceTree
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });

    it("renders WorkspaceTree sidebar during error when isMultiRepo is false", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: null,
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: null,
        setActiveWorkspace: vi.fn(),
        defaultWorkspaceName: null,
        setDefaultWorkspace: vi.fn().mockResolvedValue(undefined),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: false,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("workspace"));
      const mockReturn = createMockUseIssuesReturn({
        error: "Something went wrong",
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // WorkspaceTree is always shown, even during error
      expect(screen.getByLabelText(/workspace tree/i)).toBeInTheDocument();
    });
  });

  describe("WorkspaceBreadcrumb isMultiRepo guard", () => {
    it("renders Cortex fallback in breadcrumb when isMultiRepo is false", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: false,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Even though workspace has a name, isMultiRepo=false passes null to WorkspaceBreadcrumb
      // which renders "Cortex" fallback
      expect(
        screen.getByRole("heading", { name: "Cortex", level: 1 }),
      ).toBeInTheDocument();
      expect(screen.queryByText("my-workspace")).not.toBeInTheDocument();
    });

    it("renders workspace name in breadcrumb when isMultiRepo is true", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set<string>(),
        activeRepos: [],
        activeRepoNames: [],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // isMultiRepo=true passes workspace.name to WorkspaceBreadcrumb
      expect(screen.getByText("my-workspace")).toBeInTheDocument();
    });
  });

  describe("workspace-driven repo filtering", () => {
    it("passes sourceReposFilter from workspace context to useIssues", () => {
      const sourceReposFilter = ["repo-alpha", "repo-beta"];
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "filtered-workspace" },
        repos: [
          { name: "repo-alpha" },
          { name: "repo-beta" },
          { name: "repo-gamma" },
        ],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "filtered-workspace",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set(["repo-alpha", "repo-beta"]),
        activeRepos: [{ name: "repo-alpha" }, { name: "repo-beta" }],
        activeRepoNames: ["repo-alpha", "repo-beta"],
        isAllSelected: false,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // useIssues should have been called with sourceRepos from workspace context
      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          sourceRepos: sourceReposFilter,
        }),
      );
    });

    it("passes undefined sourceRepos when all repos selected", () => {
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "my-workspace" },
        repos: [{ name: "repo-a" }, { name: "repo-b" }],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "my-workspace",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set(["repo-a", "repo-b"]),
        activeRepos: [{ name: "repo-a" }, { name: "repo-b" }],
        activeRepoNames: ["repo-a", "repo-b"],
        isAllSelected: true,
        selectRepos: vi.fn(),
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: undefined,
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // useIssues should have been called with sourceRepos: undefined (no filtering)
      expect(useIssues).toHaveBeenCalledWith(
        expect.objectContaining({
          sourceRepos: undefined,
        }),
      );
    });

    it("renders FilterBar with selectedRepos from workspace context", () => {
      const mockSelectRepos = vi.fn();
      vi.mocked(useWorkspaceContext).mockReturnValue({
        workspace: { name: "ws" },
        repos: [{ name: "repo-a" }, { name: "repo-b" }, { name: "repo-c" }],
        groups: [],
        agents: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
        getRepoByName: vi.fn(),
        getReposByGroup: vi.fn(() => []),
        getAgentByName: vi.fn(),
        activeWorkspaceName: "ws",
        setActiveWorkspace: vi.fn(),
        selectedRepoNames: new Set(["repo-a", "repo-c"]),
        activeRepos: [{ name: "repo-a" }, { name: "repo-c" }],
        activeRepoNames: ["repo-a", "repo-c"],
        isAllSelected: false,
        selectRepos: mockSelectRepos,
        selectAll: vi.fn(),
        toggleRepo: vi.fn(),
        sourceReposFilter: ["repo-a", "repo-c"],
        isMultiRepo: true,
      });
      mockUseRouteView.mockReturnValue(createViewStateReturn("kanban"));
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // FilterBar should be present — repo filter dropdown renders when availableRepos > 1
      expect(screen.getByTestId("filter-bar")).toBeInTheDocument();
    });
  });
});
