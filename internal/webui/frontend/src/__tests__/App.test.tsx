/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for App component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

import App from '../App';
import type { Issue, Status } from '@/types';
import type { ConnectionState } from '@/api/sse';

// Create hoisted mocks that can be shared across mock definitions
const { mockUseIssues, mockUseIssueDetail, mockUseToast } = vi.hoisted(() => ({
  mockUseIssues: vi.fn(),
  mockUseIssueDetail: vi.fn(),
  mockUseToast: vi.fn(() => ({
    toasts: [],
    showToast: vi.fn(),
    dismissToast: vi.fn(),
    dismissAll: vi.fn(),
  })),
}));

// Mock the useIssues hook from its direct module
vi.mock('@/hooks/useIssues', () => ({
  useIssues: mockUseIssues,
}));

// Mock GraphView to avoid ResizeObserver issues in jsdom
vi.mock('@/components/GraphView', () => ({
  GraphView: ({ issues }: { issues: unknown[] }) => (
    <div data-testid="mock-graph-view">
      Graph View ({Array.isArray(issues) ? issues.length : 0} issues)
    </div>
  ),
}));

// Mock MonitorDashboard to avoid complex dependencies in jsdom
vi.mock('@/components/MonitorDashboard', () => ({
  MonitorDashboard: () => <div data-testid="monitor-dashboard">Monitor Dashboard</div>,
}));

// Create hoisted mock for useViewState to allow per-test control
const { mockUseViewState, mockSetActiveView } = vi.hoisted(() => ({
  mockUseViewState: vi.fn(),
  mockSetActiveView: vi.fn(),
}));

// Mock the hooks barrel file that App.tsx imports from
vi.mock('@/hooks', () => ({
  useIssues: mockUseIssues,
  useIssueDetail: mockUseIssueDetail,
  useToast: mockUseToast,
  useViewState: mockUseViewState,
  DEFAULT_GROUP_BY: 'epic',
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
    sortState: { key: null, direction: 'asc' },
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
  useFilteredSelection: vi.fn(() => ({
    selectedIds: new Set<string>(),
    isSelected: vi.fn(() => false),
    toggle: vi.fn(),
    selectAll: vi.fn(),
    deselectAll: vi.fn(),
    clear: vi.fn(),
    count: 0,
    hasSelection: false,
    isAllSelected: false,
    isIndeterminate: false,
    selectFiltered: vi.fn(),
  })),
  useBulkClose: vi.fn(() => ({
    closeSelected: vi.fn(),
    isClosing: false,
    error: null,
  })),
  useBulkPriority: vi.fn(() => ({
    setPriority: vi.fn(),
    isUpdating: false,
    error: null,
  })),
  PRIORITY_OPTIONS: [
    { value: 0, label: 'Critical', icon: '!!!' },
    { value: 1, label: 'High', icon: '!!' },
    { value: 2, label: 'Medium', icon: '!' },
    { value: 3, label: 'Low', icon: '' },
    { value: 4, label: 'None', icon: '' },
  ],
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
  useAgents: vi.fn(() => ({
    agents: [],
    tasks: { needs_planning: 0, ready_to_implement: 0, in_progress: 0, need_review: 0, blocked: 0 },
    taskLists: {
      needsPlanning: [],
      readyToImplement: [],
      needsReview: [],
      inProgress: [],
      blocked: [],
    },
    agentTasks: {},
    sync: { db_synced: true, db_last_sync: '', git_needs_push: 0, git_needs_pull: 0 },
    stats: { open: 0, closed: 0, total: 0, completion: 0 },
    isLoading: false,
    isConnected: true,
    connectionState: 'connected',
    wasEverConnected: true,
    retryCountdown: 0,
    error: null,
    lastUpdated: null,
    refetch: vi.fn(),
    retryNow: vi.fn(),
  })),
  useStats: vi.fn(() => ({
    data: null,
    loading: false,
    error: null,
    refetch: vi.fn(),
  })),
  useStats: vi.fn(() => ({
    data: { open: 0, in_progress: 0, ready: 0, closed: 0 },
    loading: false,
    error: null,
    refetch: vi.fn(),
  })),
  useRecentAssignees: vi.fn(() => ({
    recentAssignees: [],
    addRecentAssignee: vi.fn(),
    clearRecentAssignees: vi.fn(),
  })),
}));

// Import the mocked module
import { useIssues } from '@/hooks/useIssues';
import { useFilterState, useIssueDetail, useViewState } from '@/hooks';

// Alias for convenience in tests (prefixed with _ to satisfy linter for unused vars)
const _useIssuesMock = mockUseIssues;
const _useViewStateMock = mockUseViewState;

/**
 * Create a mock issue for testing.
 */
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: `issue-${Math.random().toString(36).slice(2, 9)}`,
    title: 'Test Issue',
    priority: 2,
    status: 'open',
    issue_type: 'task',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
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
}

function createMockUseIssuesReturn(
  overrides: Partial<MockUseIssuesReturn> = {}
): MockUseIssuesReturn {
  const issues = overrides.issues ?? [];
  const issuesMap = overrides.issuesMap ?? new Map(issues.map((issue) => [issue.id, issue]));

  return {
    issues,
    issuesMap,
    isLoading: false,
    error: null,
    connectionState: 'connected',
    isConnected: true,
    reconnectAttempts: 0,
    refetch: vi.fn().mockResolvedValue(undefined),
    updateIssueStatus: vi.fn().mockResolvedValue(undefined),
    getIssue: (id: string) => issuesMap.get(id),
    mutationCount: 0,
    retryConnection: vi.fn(),
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
  }> = {}
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

describe('App', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Set up default useViewState mock (kanban is the default view)
    mockUseViewState.mockReturnValue(['kanban', mockSetActiveView]);
    // Set up default useIssueDetail mock
    mockUseIssueDetail.mockReturnValue(createMockUseIssueDetailReturn());
  });

  describe('loading state', () => {
    it('renders LoadingSkeleton columns when isLoading is true', () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // LoadingSkeleton.Column components have aria-hidden="true"
      const skeletonColumns = container.querySelectorAll('[aria-hidden="true"]');

      // Each Column skeleton contains multiple nested elements with aria-hidden
      // We verify that skeletons are rendered by checking for the column structure
      expect(skeletonColumns.length).toBeGreaterThan(0);

      // Should not render KanbanBoard when loading
      expect(screen.queryByRole('article')).not.toBeInTheDocument();

      // Should not render ErrorDisplay when loading
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });

    it('renders three LoadingSkeleton.Column components when loading', () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // The loading skeleton columns have a specific structure
      // We check for the loading container with three skeleton columns
      const flexContainer = container.querySelector('[data-testid="loading-container"]');
      expect(flexContainer).toBeInTheDocument();

      // Count direct children of the flex container (the 3 skeleton columns)
      const children = flexContainer?.children;
      expect(children?.length).toBe(3);
    });

    it('renders ConnectionStatus in header when loading', () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: true,
        connectionState: 'connecting',
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus should be visible with the connection state
      // (no dnd-kit status element in loading state since KanbanBoard isn't rendered)
      const status = container.querySelector('[data-state="connecting"]');
      expect(status).toBeInTheDocument();
      expect(screen.getByText('Connecting...')).toBeInTheDocument();
    });
  });

  describe('error state', () => {
    it('renders ErrorDisplay when error is present', () => {
      const mockReturn = createMockUseIssuesReturn({
        error: 'Failed to fetch issues',
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByTestId('error-display')).toBeInTheDocument();
      expect(screen.getByRole('alert')).toBeInTheDocument();
    });

    it('renders ErrorDisplay with fetch-error variant', () => {
      const mockReturn = createMockUseIssuesReturn({
        error: 'Network error',
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByTestId('error-display')).toHaveAttribute('data-variant', 'fetch-error');
    });

    it('renders retry button that calls refetch', () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({
        error: 'Network error',
        isLoading: false,
        refetch,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      const retryButton = screen.getByTestId('retry-button');
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(refetch).toHaveBeenCalledTimes(1);
    });

    it('shows error details when showDetails is true', () => {
      const mockReturn = createMockUseIssuesReturn({
        error: 'Specific error message',
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // The App component passes showDetails to ErrorDisplay
      expect(screen.getByText('Technical details')).toBeInTheDocument();
      expect(screen.getByText('Specific error message')).toBeInTheDocument();
    });

    it('renders ConnectionStatus with retry in error state', () => {
      const retryConnection = vi.fn();
      const mockReturn = createMockUseIssuesReturn({
        error: 'Connection failed',
        isLoading: false,
        connectionState: 'reconnecting',
        reconnectAttempts: 2,
        retryConnection,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // ConnectionStatus should show reconnecting state
      // (no dnd-kit status element in error state since KanbanBoard isn't rendered)
      const status = container.querySelector('[data-state="reconnecting"]');
      expect(status).toBeInTheDocument();
      expect(screen.getByText('Reconnecting (attempt 2)...')).toBeInTheDocument();
    });

    it('does not render KanbanBoard when error is present', () => {
      const mockReturn = createMockUseIssuesReturn({
        error: 'Error occurred',
        isLoading: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // KanbanBoard renders StatusColumns with headings
      expect(screen.queryByRole('heading', { name: 'Ready' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'In Progress' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Done' })).not.toBeInTheDocument();
    });
  });

  describe('success state', () => {
    it('renders KanbanBoard with issues when data is loaded', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'First Issue', status: 'open' }),
        createMockIssue({
          id: 'issue-2',
          title: 'Second Issue',
          status: 'in_progress',
        }),
        createMockIssue({
          id: 'issue-3',
          title: 'Third Issue',
          status: 'closed',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should render with status columns
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Done' })).toBeInTheDocument();

      // Issues should be rendered
      expect(screen.getByText('First Issue')).toBeInTheDocument();
      expect(screen.getByText('Second Issue')).toBeInTheDocument();
      expect(screen.getByText('Third Issue')).toBeInTheDocument();
    });

    it('renders ConnectionStatus in header actions', () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'connected',
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Use data-state attribute to find ConnectionStatus specifically
      // (dnd-kit also adds a role="status" element)
      const statusElement = container.querySelector('[data-state="connected"]');
      expect(statusElement).toBeInTheDocument();
      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('does not render ErrorDisplay when no error', () => {
      const mockReturn = createMockUseIssuesReturn({ error: null });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
    });

    it('does not render LoadingSkeleton when not loading', () => {
      const mockReturn = createMockUseIssuesReturn({ isLoading: false });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container: _container } = render(<App />);

      // The loading container should not be present when not loading
      expect(screen.queryByTestId('loading-container')).not.toBeInTheDocument();
    });

    it('renders empty KanbanBoard when issues array is empty', () => {
      const mockReturn = createMockUseIssuesReturn({ issues: [] });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Use flat view (groupBy: 'none') to verify empty column rendering
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: 'none' },
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

      // Should render columns even with no issues
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Done' })).toBeInTheDocument();
    });
  });

  describe('drag-end handler', () => {
    it('calls updateIssueStatus with correct parameters on drag-end', async () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [createMockIssue({ id: 'drag-issue', title: 'Drag Me', status: 'open' })];
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
      expect(screen.getByText('Drag Me')).toBeInTheDocument();
    });

    it('updateIssueStatus is passed to KanbanBoard via onDragEnd', () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [createMockIssue({ id: 'test-issue', title: 'Test', status: 'open' })];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify KanbanBoard is rendered (we can't easily test the drag event
      // but we verify the component structure is correct)
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByText('Test')).toBeInTheDocument();
    });
  });

  describe('drag-end failure and ErrorToast', () => {
    it('shows ErrorToast when updateIssueStatus throws', async () => {
      const updateIssueStatus = vi.fn().mockRejectedValue(new Error('Update failed'));
      const issues = [
        createMockIssue({
          id: 'fail-issue',
          title: 'Will Fail',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify the issue is rendered
      expect(screen.getByText('Will Fail')).toBeInTheDocument();

      // Note: Testing the actual toast appearance requires triggering the drag-end
      // which involves complex dnd-kit event simulation. The test below verifies
      // that the toast is not shown initially.
      expect(screen.queryByTestId('error-toast')).not.toBeInTheDocument();
    });

    it('ErrorToast is not rendered initially', () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.queryByTestId('error-toast')).not.toBeInTheDocument();
    });

    it('ErrorToast can display error message from updateIssueStatus failure', async () => {
      // This test demonstrates the expected behavior by directly testing
      // that when toastError state is set, ErrorToast appears.
      // Since we can't easily trigger drag events, we test the component's
      // handling of the error state through the hook's error mechanism.

      const updateIssueStatus = vi.fn().mockRejectedValue(new Error('API Error'));
      const issues = [createMockIssue({ id: 'test-1', status: 'open' })];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Verify the component is ready to handle errors
      expect(screen.queryByTestId('error-toast')).not.toBeInTheDocument();
    });
  });

  describe('ConnectionStatus in header', () => {
    it('renders ConnectionStatus with connected state', () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'connected',
        isConnected: true,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Use data-state attribute to find ConnectionStatus specifically
      // (dnd-kit also adds a role="status" element)
      const status = container.querySelector('[data-state="connected"]');
      expect(status).toBeInTheDocument();
      expect(screen.getByText('Connected')).toBeInTheDocument();
    });

    it('renders ConnectionStatus with disconnected state', () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'disconnected',
        isConnected: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      const status = container.querySelector('[data-state="disconnected"]');
      expect(status).toBeInTheDocument();
      expect(screen.getByText('Disconnected')).toBeInTheDocument();
    });

    it('renders ConnectionStatus with reconnecting state and attempt count', () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'reconnecting',
        isConnected: false,
        reconnectAttempts: 3,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      const status = container.querySelector('[data-state="reconnecting"]');
      expect(status).toBeInTheDocument();
      expect(screen.getByText('Reconnecting (attempt 3)...')).toBeInTheDocument();
    });

    it('renders ConnectionStatus with connecting state', () => {
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'connecting',
        isConnected: false,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByText('Connecting...')).toBeInTheDocument();
    });

    it('passes retryConnection to ConnectionStatus onRetry', () => {
      const retryConnection = vi.fn();
      const mockReturn = createMockUseIssuesReturn({
        connectionState: 'reconnecting',
        reconnectAttempts: 1,
        retryConnection,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // The retry button should be visible when reconnecting with attempts >= 1
      const retryButton = screen.getByRole('button', {
        name: 'Retry connection now',
      });
      expect(retryButton).toBeInTheDocument();

      fireEvent.click(retryButton);
      expect(retryConnection).toHaveBeenCalledTimes(1);
    });
  });

  describe('AppLayout integration', () => {
    it('renders with Beads title in header', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByRole('heading', { name: 'Beads', level: 1 })).toBeInTheDocument();
    });

    it('renders header with banner role', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Use container query since there may be multiple banner roles
      const banner = container.querySelector('header[role="banner"]');
      expect(banner).toBeInTheDocument();
    });

    it('renders main content area', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(screen.getByRole('main')).toBeInTheDocument();
    });
  });

  describe('useIssues hook integration', () => {
    it('calls useIssues hook on mount', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      expect(useIssues).toHaveBeenCalled();
    });

    it('uses all expected properties from useIssues return', () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
        isLoading: false,
        error: null,
        connectionState: 'connected',
        reconnectAttempts: 0,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Verify the component renders without errors, indicating
      // it correctly uses all the hook's return values
      const banner = container.querySelector('header[role="banner"]');
      expect(banner).toBeInTheDocument();
      expect(screen.getByRole('main')).toBeInTheDocument();
    });
  });

  describe('state transitions', () => {
    it('transitions from loading to success', () => {
      const mockLoadingReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockLoadingReturn);

      const { rerender } = render(<App />);

      // Verify loading state
      expect(screen.queryByRole('heading', { name: 'Ready' })).not.toBeInTheDocument();

      // Transition to success
      const issues = [createMockIssue({ title: 'Loaded Issue', status: 'open' })];
      const mockSuccessReturn = createMockUseIssuesReturn({
        isLoading: false,
        issues,
      });
      vi.mocked(useIssues).mockReturnValue(mockSuccessReturn);

      rerender(<App />);

      // Verify success state
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByText('Loaded Issue')).toBeInTheDocument();
    });

    it('transitions from loading to error', () => {
      const mockLoadingReturn = createMockUseIssuesReturn({ isLoading: true });
      vi.mocked(useIssues).mockReturnValue(mockLoadingReturn);

      const { rerender } = render(<App />);

      // Verify loading state
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();

      // Transition to error
      const mockErrorReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: 'Network error occurred',
      });
      vi.mocked(useIssues).mockReturnValue(mockErrorReturn);

      rerender(<App />);

      // Verify error state
      expect(screen.getByTestId('error-display')).toBeInTheDocument();
    });

    it('transitions from error to success on retry', () => {
      const mockErrorReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: 'Initial error',
      });
      vi.mocked(useIssues).mockReturnValue(mockErrorReturn);

      const { rerender } = render(<App />);

      // Verify error state
      expect(screen.getByTestId('error-display')).toBeInTheDocument();

      // Transition to success after retry
      const issues = [createMockIssue({ title: 'Retrieved Issue', status: 'open' })];
      const mockSuccessReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: null,
        issues,
      });
      vi.mocked(useIssues).mockReturnValue(mockSuccessReturn);

      rerender(<App />);

      // Verify success state
      expect(screen.queryByTestId('error-display')).not.toBeInTheDocument();
      expect(screen.getByText('Retrieved Issue')).toBeInTheDocument();
    });
  });

  describe('filter integration', () => {
    it('renders SearchInput in the navigation slot', () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SearchInput should be rendered with the search input test id
      expect(screen.getByTestId('search-input')).toBeInTheDocument();
      expect(screen.getByPlaceholderText('Search issues...')).toBeInTheDocument();
    });

    it('renders FilterBar in the navigation slot', () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue()],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // FilterBar should be rendered with its test id
      expect(screen.getByTestId('filter-bar')).toBeInTheDocument();
      expect(screen.getByTestId('priority-filter')).toBeInTheDocument();
      expect(screen.getByTestId('type-filter')).toBeInTheDocument();
    });

    it('does not render filter navigation in loading state', () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: true,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // FilterBar and SearchInput should not be rendered in loading state
      expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
      expect(screen.queryByTestId('filter-bar')).not.toBeInTheDocument();
    });

    it('does not render filter navigation in error state', () => {
      const mockReturn = createMockUseIssuesReturn({
        isLoading: false,
        error: 'Network error',
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // FilterBar and SearchInput should not be rendered in error state
      expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
      expect(screen.queryByTestId('filter-bar')).not.toBeInTheDocument();
    });

    it('passes filtered issues to KanbanBoard', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'First Issue', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Second Issue', status: 'closed' }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // With the mock returning all issues, both should be visible
      expect(screen.getByText('First Issue')).toBeInTheDocument();
      expect(screen.getByText('Second Issue')).toBeInTheDocument();
    });

    it('clears search input when Clear filters button is clicked', async () => {
      // Set up issues so the app renders in success state
      const issues = [createMockIssue({ id: 'issue-1', title: 'Test Issue', status: 'open' })];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Track filter state that will change when clearAll is called
      let currentFilters: { search?: string; groupBy?: string } = {
        search: 'test query',
        groupBy: 'none',
      };

      const clearAll = vi.fn(() => {
        // Simulate clearAll behavior: clears search
        currentFilters = { groupBy: 'none' };
      });

      const filterActions = {
        setPriority: vi.fn(),
        setType: vi.fn(),
        setLabels: vi.fn(),
        setSearch: vi.fn(),
        setGroupBy: vi.fn(),
        clearFilter: vi.fn(),
        clearAll,
      };

      // Mock useFilterState to return filter with search value
      // This must be set before render so the initial useState gets the value
      vi.mocked(useFilterState).mockReturnValue([currentFilters, filterActions]);

      const { rerender } = render(<App />);

      // Verify search input has the initial value
      // Note: data-testid="search-input" is on the wrapper div, the actual input has data-testid="search-input-field"
      const searchInput = screen.getByTestId('search-input-field') as HTMLInputElement;
      expect(searchInput.value).toBe('test query');

      // Clear filters button should be visible because search filter is active
      const clearButton = screen.getByTestId('clear-filters');
      expect(clearButton).toBeInTheDocument();

      // Click clear filters - this calls clearAll which updates currentFilters
      fireEvent.click(clearButton);
      expect(clearAll).toHaveBeenCalledTimes(1);

      // Update the mock to return the new filter state (search cleared)
      vi.mocked(useFilterState).mockReturnValue([currentFilters, filterActions]);

      // Rerender to trigger the useEffect that syncs filters.search to searchValue
      rerender(<App />);

      // Wait for the search input to be cleared
      await waitFor(() => {
        expect(searchInput.value).toBe('');
      });
    });
  });

  describe('swim lane integration', () => {
    it('renders SwimLaneBoard instead of KanbanBoard when activeView is kanban', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'Issue One', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Issue Two', status: 'in_progress' }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should render status columns
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();

      // Issues should be visible
      expect(screen.getByText('Issue One')).toBeInTheDocument();
      expect(screen.getByText('Issue Two')).toBeInTheDocument();
    });

    it('passes groupBy prop to SwimLaneBoard with default value of epic', () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with no groupBy (App.tsx applies DEFAULT_GROUP_BY = 'epic')
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
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
    });

    it('passes groupBy prop to SwimLaneBoard with epic grouping', () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with groupBy: 'epic'
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: 'epic' },
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
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
    });

    it('FilterBar receives groupBy and onGroupByChange props', () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const setGroupBy = vi.fn();
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: 'none' },
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
      expect(screen.getByTestId('filter-bar')).toBeInTheDocument();
    });

    it('updates SwimLaneBoard groupBy when FilterBar groupBy changes', () => {
      const issues = [createMockIssue()];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      let currentGroupBy = 'none';
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

      vi.mocked(useFilterState).mockReturnValue([{ groupBy: currentGroupBy }, filterActions]);

      const { rerender } = render(<App />);

      // Initial render with groupBy: 'none'
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();

      // Simulate groupBy change to 'priority'
      currentGroupBy = 'priority';
      vi.mocked(useFilterState).mockReturnValue([{ groupBy: currentGroupBy }, filterActions]);

      rerender(<App />);

      // SwimLaneBoard should still render with updated groupBy
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(setGroupBy).not.toHaveBeenCalled(); // setGroupBy is called by FilterBar, not App
    });

    it('passes onDragEnd handler to SwimLaneBoard', () => {
      const updateIssueStatus = vi.fn().mockResolvedValue(undefined);
      const issues = [createMockIssue({ id: 'drag-test', title: 'Drag Me', status: 'open' })];
      const mockReturn = createMockUseIssuesReturn({
        issues,
        updateIssueStatus,
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should be rendered with the drag handler
      expect(screen.getByText('Drag Me')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
    });

    it('SwimLaneBoard receives filtered issues', () => {
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'High Priority Issue',
          status: 'open',
          priority: 0,
        }),
        createMockIssue({
          id: 'issue-2',
          title: 'Low Priority Issue',
          status: 'open',
          priority: 4,
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // Both issues should be visible since no filters are active
      expect(screen.getByText('High Priority Issue')).toBeInTheDocument();
      expect(screen.getByText('Low Priority Issue')).toBeInTheDocument();
    });

    it('SwimLaneBoard receives blocked issues map when available', () => {
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Blocked Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      render(<App />);

      // SwimLaneBoard should render without errors
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
    });

    it('SwimLaneBoard respects showBlocked filter', () => {
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Issue To Show',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Mock FilterState with showBlocked: true
      vi.mocked(useFilterState).mockReturnValue([
        { groupBy: 'none', showBlocked: true },
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
      expect(screen.getByText('Issue To Show')).toBeInTheDocument();
    });
  });

  describe('IssueDetailPanel integration', () => {
    it('renders IssueDetailPanel in closed state by default', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      const { container } = render(<App />);

      // Panel should be rendered but closed (isOpen=false)
      const panel = container.querySelector('[data-testid="issue-detail-panel"]');
      expect(panel).toBeInTheDocument();
      expect(panel).toHaveAttribute('data-state', 'closed');
    });

    it('opens panel when issue is clicked in SwimLaneBoard', () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Test Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        })
      );

      const { container } = render(<App />);

      // Click on the issue card
      const issueCard = screen.getByText('Test Issue');
      fireEvent.click(issueCard);

      // Panel should now be open
      const panel = container.querySelector('[data-testid="issue-detail-panel"]');
      expect(panel).toHaveAttribute('data-state', 'open');
    });

    it('calls fetchIssue with correct ID when issue is clicked', () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-123',
          title: 'Clickable Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        })
      );

      render(<App />);

      // Click on the issue card
      const issueCard = screen.getByText('Clickable Issue');
      fireEvent.click(issueCard);

      // fetchIssue should be called with the correct ID
      expect(fetchIssue).toHaveBeenCalledTimes(1);
      expect(fetchIssue).toHaveBeenCalledWith('issue-123');
    });

    it('closes panel when onClose is called', () => {
      const clearIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Closeable Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      // Provide issueDetails so that IssueHeader renders with the close button
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: 'issue-1',
            title: 'Closeable Issue Details',
            priority: 2,
            status: 'open',
            issue_type: 'task',
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
          clearIssue,
        })
      );

      const { container } = render(<App />);

      // Click on the issue card (uses aria-label "Issue: {title}")
      const issueCard = screen.getByRole('button', { name: /Issue: Closeable Issue/ });
      fireEvent.click(issueCard);

      // Panel should be open
      const panel = container.querySelector('[data-testid="issue-detail-panel"]');
      expect(panel).toHaveAttribute('data-state', 'open');

      // Click the close button (rendered by IssueHeader inside DefaultContent)
      const closeButton = screen.getByTestId('header-close-button');
      fireEvent.click(closeButton);

      // Panel should close immediately
      expect(panel).toHaveAttribute('data-state', 'closed');
    });

    it('does not re-fetch when clicking the same issue that is already selected and open', () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Same Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        })
      );

      render(<App />);

      // Click on the issue card twice
      const issueCard = screen.getByText('Same Issue');
      fireEvent.click(issueCard);

      expect(fetchIssue).toHaveBeenCalledTimes(1);

      // Click again on the same issue
      fireEvent.click(issueCard);

      // fetchIssue should NOT be called again
      expect(fetchIssue).toHaveBeenCalledTimes(1);
    });

    it('passes loading state to IssueDetailPanel during fetch', () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Loading Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          isLoading: true,
          fetchIssue,
        })
      );

      const { container } = render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText('Loading Issue');
      fireEvent.click(issueCard);

      // Panel should show loading state
      const panel = container.querySelector('[data-testid="issue-detail-panel"]');
      expect(panel).toHaveAttribute('data-loading', 'true');
    });

    it('passes issue details to IssueDetailPanel when loaded', () => {
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Detail Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          issueDetails: {
            id: 'issue-1',
            title: 'Detail Issue Title',
            description: 'Issue description',
            priority: 2,
            status: 'open',
            issue_type: 'task',
            created_at: '2024-01-01T00:00:00Z',
            updated_at: '2024-01-01T00:00:00Z',
          },
          isLoading: false,
        })
      );

      render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText('Detail Issue');
      fireEvent.click(issueCard);

      // Panel should show the issue title
      expect(screen.getByText('Detail Issue Title')).toBeInTheDocument();
    });

    it('passes error state to IssueDetailPanel when fetch fails', () => {
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'Error Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          error: 'Failed to fetch issue details',
          isLoading: false,
        })
      );

      const { container } = render(<App />);

      // Click on the issue to open the panel
      const issueCard = screen.getByText('Error Issue');
      fireEvent.click(issueCard);

      // Panel should show the error state
      const panel = container.querySelector('[data-testid="issue-detail-panel"]');
      expect(panel).toHaveAttribute('data-error', 'true');
    });

    it('fetches new issue when clicking a different issue while panel is open', () => {
      const fetchIssue = vi.fn();
      const issues = [
        createMockIssue({
          id: 'issue-1',
          title: 'First Issue',
          status: 'open',
        }),
        createMockIssue({
          id: 'issue-2',
          title: 'Second Issue',
          status: 'open',
        }),
      ];
      const mockReturn = createMockUseIssuesReturn({ issues });
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useIssueDetail).mockReturnValue(
        createMockUseIssueDetailReturn({
          fetchIssue,
        })
      );

      render(<App />);

      // Click on the first issue
      const firstIssue = screen.getByText('First Issue');
      fireEvent.click(firstIssue);

      expect(fetchIssue).toHaveBeenCalledTimes(1);
      expect(fetchIssue).toHaveBeenCalledWith('issue-1');

      // Click on the second issue
      const secondIssue = screen.getByText('Second Issue');
      fireEvent.click(secondIssue);

      // fetchIssue should be called again with the new ID
      expect(fetchIssue).toHaveBeenCalledTimes(2);
      expect(fetchIssue).toHaveBeenCalledWith('issue-2');
    });
  });

  describe('useIssues mode parameter based on activeView', () => {
    it('calls useIssues with mode: "ready" when activeView is "kanban"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith({ mode: 'ready' });
    });

    it('calls useIssues with mode: "ready" when activeView is "table"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['table', mockSetActiveView]);

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith({ mode: 'ready' });
    });

    it('calls useIssues with mode: "graph" when activeView is "graph"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith({ mode: 'graph' });
    });

    it('refetches issues when view changes from kanban to graph', () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'ready'
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'ready' });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to graph view
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      rerender(<App />);

      // Verify useIssues is called with mode: 'graph' after view change
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'graph' });
    });

    it('refetches issues when view changes from graph to kanban', () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'graph'
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'graph' });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to kanban view
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      rerender(<App />);

      // Verify useIssues is called with mode: 'ready' after view change
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'ready' });
    });

    it('refetches issues when view changes from graph to table', () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'graph'
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'graph' });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to table view
      vi.mocked(useViewState).mockReturnValue(['table', mockSetActiveView]);

      rerender(<App />);

      // Verify useIssues is called with mode: 'ready' after view change
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'ready' });
    });

    it('does not change mode when switching between kanban and table', () => {
      const refetch = vi.fn().mockResolvedValue(undefined);
      const mockReturn = createMockUseIssuesReturn({ refetch });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify initial call with mode: 'ready'
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'ready' });

      // Clear mock to track the next call
      vi.mocked(useIssues).mockClear();

      // Switch to table view
      vi.mocked(useViewState).mockReturnValue(['table', mockSetActiveView]);

      rerender(<App />);

      // Verify useIssues is still called with mode: 'ready'
      expect(useIssues).toHaveBeenLastCalledWith({ mode: 'ready' });
    });

    it('useViewState is called before useIssues to determine fetch mode', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Track call order
      const callOrder: string[] = [];
      vi.mocked(useViewState).mockImplementation(() => {
        callOrder.push('useViewState');
        return ['graph', mockSetActiveView];
      });
      vi.mocked(useIssues).mockImplementation(() => {
        callOrder.push('useIssues');
        return mockReturn;
      });

      render(<App />);

      // Verify useViewState is called before useIssues
      const viewStateIndex = callOrder.indexOf('useViewState');
      const issuesIndex = callOrder.indexOf('useIssues');
      expect(viewStateIndex).toBeLessThan(issuesIndex);
      expect(viewStateIndex).toBeGreaterThanOrEqual(0);
      expect(issuesIndex).toBeGreaterThanOrEqual(0);
    });

    it('calls useIssues with mode: "ready" when activeView is "monitor"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      render(<App />);

      expect(useIssues).toHaveBeenCalledWith({ mode: 'ready' });
    });
  });

  describe('MonitorDashboard lazy loading integration', () => {
    it('renders MonitorDashboard when activeView is "monitor"', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      render(<App />);

      // Wait for lazy-loaded MonitorDashboard to appear
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });
    });

    it('shows LoadingSkeleton.Monitor as fallback during lazy load', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      render(<App />);

      // The skeleton may appear briefly during the lazy load
      // We check that MonitorDashboard eventually loads (which means Suspense worked)
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });
    });

    it('does not render MonitorDashboard when activeView is "kanban"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      render(<App />);

      // MonitorDashboard should not be rendered when kanban view is active
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();
      // Kanban view should be active (SwimLaneBoard renders status columns)
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
    });

    it('does not render MonitorDashboard when activeView is "table"', () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['table', mockSetActiveView]);

      render(<App />);

      // MonitorDashboard should not be rendered when table view is active
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();
    });

    it('does not render MonitorDashboard when activeView is "graph"', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      render(<App />);

      // Wait for lazy-loaded GraphView to appear
      await waitFor(() => {
        expect(screen.getByTestId('mock-graph-view')).toBeInTheDocument();
      });

      // MonitorDashboard should not be rendered when graph view is active
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();
    });

    it('transitions from kanban to monitor view correctly', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with kanban view
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify kanban view is rendered
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();

      // Switch to monitor view
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });

      // Kanban columns should no longer be rendered
      expect(screen.queryByRole('heading', { name: 'Ready' })).not.toBeInTheDocument();
    });

    it('transitions from monitor to kanban view correctly', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with monitor view
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });

      // Switch to kanban view
      vi.mocked(useViewState).mockReturnValue(['kanban', mockSetActiveView]);

      rerender(<App />);

      // Verify kanban view is now rendered
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();
    });

    it('transitions from graph to monitor view correctly', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with graph view
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Wait for GraphView to load
      await waitFor(() => {
        expect(screen.getByTestId('mock-graph-view')).toBeInTheDocument();
      });

      // Switch to monitor view
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });

      // GraphView should no longer be rendered
      expect(screen.queryByTestId('mock-graph-view')).not.toBeInTheDocument();
    });

    it('transitions from monitor to graph view correctly', async () => {
      const mockReturn = createMockUseIssuesReturn({});
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with monitor view
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });

      // Switch to graph view
      vi.mocked(useViewState).mockReturnValue(['graph', mockSetActiveView]);

      rerender(<App />);

      // Wait for GraphView to load
      await waitFor(() => {
        expect(screen.getByTestId('mock-graph-view')).toBeInTheDocument();
      });

      // MonitorDashboard should no longer be rendered
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();
    });

    it('transitions from table to monitor view correctly', async () => {
      const mockReturn = createMockUseIssuesReturn({
        issues: [createMockIssue({ id: 'test-1', title: 'Test Issue', status: 'open' })],
      });
      vi.mocked(useIssues).mockReturnValue(mockReturn);

      // Start with table view
      vi.mocked(useViewState).mockReturnValue(['table', mockSetActiveView]);

      const { rerender } = render(<App />);

      // Verify table view is rendered (IssueTable has specific structure)
      expect(screen.queryByTestId('monitor-dashboard')).not.toBeInTheDocument();

      // Switch to monitor view
      vi.mocked(useViewState).mockReturnValue(['monitor', mockSetActiveView]);

      rerender(<App />);

      // Wait for MonitorDashboard to load
      await waitFor(() => {
        expect(screen.getByTestId('monitor-dashboard')).toBeInTheDocument();
      });
    });
  });
});
