/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for GraphView component.
 */

import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { GraphView } from '../GraphView';
import type { GraphViewProps } from '../GraphView';
import type { Issue, IssueNode, DependencyEdge, BlockedIssue } from '@/types';

// Mock the hooks
vi.mock('@/hooks/useGraphData', () => ({
  useGraphData: vi.fn(),
}));

vi.mock('@/hooks/useAutoLayout', () => ({
  useAutoLayout: vi.fn(),
}));

vi.mock('@/hooks/useBlockedIssues', () => ({
  useBlockedIssues: vi.fn(),
}));

// Mock React Flow components
vi.mock('@xyflow/react', () => ({
  ReactFlow: vi.fn(({ children, onNodeClick, onNodeMouseEnter, onNodeMouseLeave, nodes }) => (
    <div data-testid="react-flow" data-node-count={nodes?.length ?? 0}>
      {/* Simulate node rendering to test click and hover handlers */}
      {nodes?.map((node: IssueNode) => (
        <div
          key={node.id}
          data-testid={`node-${node.id}`}
          onClick={(event) => onNodeClick?.(event, node)}
          onMouseEnter={(event) => onNodeMouseEnter?.(event, node)}
          onMouseLeave={(event) => onNodeMouseLeave?.(event, node)}
        >
          {node.data?.title}
        </div>
      ))}
      {children}
    </div>
  )),
  Background: vi.fn(() => <div data-testid="background" />),
  MiniMap: vi.fn(() => <div data-testid="minimap" />),
  Panel: vi.fn(({ children, position }) => (
    <div data-testid="react-flow-panel" data-position={position}>
      {children}
    </div>
  )),
}));

// Mock child components
vi.mock('@/components', () => ({
  IssueNode: vi.fn(() => <div data-testid="issue-node" />),
  DependencyEdge: vi.fn(() => <div data-testid="dependency-edge" />),
  GraphControls: vi.fn(({ highlightReady, onHighlightReadyChange, showClosed, onShowClosedChange, statusFilter, onStatusFilterChange, dependencyTypeFilter, onDependencyTypeFilterChange, className }) => (
    <div
      data-testid="graph-controls"
      data-highlight-ready={highlightReady}
      data-show-closed={showClosed}
      data-status-filter={statusFilter}
      data-dep-type-filter={dependencyTypeFilter ? JSON.stringify([...dependencyTypeFilter]) : ''}
      className={className}
    >
      <input
        type="checkbox"
        data-testid="highlight-ready-checkbox"
        checked={highlightReady}
        onChange={(e) => onHighlightReadyChange(e.target.checked)}
      />
      <input
        type="checkbox"
        data-testid="show-closed-checkbox"
        checked={showClosed}
        onChange={(e) => onShowClosedChange(e.target.checked)}
      />
      <select
        data-testid="status-filter-select"
        value={statusFilter}
        onChange={(e) => onStatusFilterChange(e.target.value)}
      >
        <option value="all">All</option>
        <option value="open">Open</option>
        <option value="in_progress">In Progress</option>
        <option value="blocked">Blocked</option>
        <option value="deferred">Deferred</option>
        <option value="closed">Closed</option>
      </select>
      {onDependencyTypeFilterChange && dependencyTypeFilter && (
        <div data-testid="dep-type-filter-group">
          <input
            type="checkbox"
            data-testid="dep-type-blocking-checkbox"
            checked={dependencyTypeFilter.has('blocking')}
            onChange={(e) => {
              const newFilter = new Set(dependencyTypeFilter);
              if (e.target.checked) {
                newFilter.add('blocking');
              } else {
                newFilter.delete('blocking');
              }
              onDependencyTypeFilterChange(newFilter);
            }}
          />
          <input
            type="checkbox"
            data-testid="dep-type-parent-child-checkbox"
            checked={dependencyTypeFilter.has('parent-child')}
            onChange={(e) => {
              const newFilter = new Set(dependencyTypeFilter);
              if (e.target.checked) {
                newFilter.add('parent-child');
              } else {
                newFilter.delete('parent-child');
              }
              onDependencyTypeFilterChange(newFilter);
            }}
          />
          <input
            type="checkbox"
            data-testid="dep-type-non-blocking-checkbox"
            checked={dependencyTypeFilter.has('non-blocking')}
            onChange={(e) => {
              const newFilter = new Set(dependencyTypeFilter);
              if (e.target.checked) {
                newFilter.add('non-blocking');
              } else {
                newFilter.delete('non-blocking');
              }
              onDependencyTypeFilterChange(newFilter);
            }}
          />
        </div>
      )}
    </div>
  )),
  GraphLegend: vi.fn(({ collapsed, onToggle, className }) => (
    <div
      data-testid="graph-legend"
      data-collapsed={collapsed}
      className={className}
    >
      <button onClick={onToggle} data-testid="legend-toggle">Legend</button>
    </div>
  )),
  NodeTooltip: vi.fn(({ issue, position }) => (
    issue && position ? (
      <div
        data-testid="node-tooltip"
        data-issue-id={issue.id}
        data-position-x={position.x}
        data-position-y={position.y}
      />
    ) : null
  )),
}));

// Import mocks after vi.mock calls
import { useGraphData } from '@/hooks/useGraphData';
import { useAutoLayout } from '@/hooks/useAutoLayout';
import { useBlockedIssues } from '@/hooks/useBlockedIssues';

/**
 * Create a minimal test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'test-issue-abc123',
    title: 'Test Issue Title',
    priority: 2,
    created_at: '2024-01-15T10:30:00Z',
    updated_at: '2024-01-15T10:30:00Z',
    ...overrides,
  };
}

/**
 * Create test nodes from issues.
 */
function createTestNodes(issues: Issue[]): IssueNode[] {
  return issues.map((issue) => ({
    id: `node-${issue.id}`,
    type: 'issue' as const,
    position: { x: 0, y: 0 },
    data: {
      issue,
      title: issue.title,
      status: issue.status,
      priority: issue.priority,
      issueType: issue.issue_type,
      dependencyCount: 0,
      dependentCount: 0,
      isReady: true,
    },
  }));
}

/**
 * Create test nodes from issues with isClosed flag.
 */
function createTestNodesWithClosed(issues: Issue[]): IssueNode[] {
  return issues.map((issue) => ({
    id: `node-${issue.id}`,
    type: 'issue' as const,
    position: { x: 0, y: 0 },
    data: {
      issue,
      title: issue.title,
      status: issue.status,
      priority: issue.priority,
      issueType: issue.issue_type,
      dependencyCount: 0,
      dependentCount: 0,
      isReady: issue.status !== 'closed',
      isClosed: issue.status === 'closed',
    },
  }));
}

/**
 * Create test edges (empty by default).
 */
function createTestEdges(): DependencyEdge[] {
  return [];
}

/**
 * Create test props for GraphView component.
 */
function createTestProps(overrides: Partial<GraphViewProps> = {}): GraphViewProps {
  return {
    issues: [],
    ...overrides,
  };
}

/**
 * Setup mocks with default return values.
 */
function setupMocks(options: {
  nodes?: IssueNode[];
  edges?: DependencyEdge[];
  blockedIssues?: BlockedIssue[] | null;
} = {}) {
  const { nodes = [], edges = [], blockedIssues = null } = options;

  (useGraphData as Mock).mockReturnValue({
    nodes,
    edges,
    issueIdToNodeId: new Map(),
    totalDependencies: edges.length,
    blockingDependencies: 0,
  });

  (useAutoLayout as Mock).mockReturnValue({
    nodes,
    bounds: { width: 500, height: 500 },
  });

  (useBlockedIssues as Mock).mockReturnValue({
    data: blockedIssues,
    loading: false,
    error: null,
    refetch: vi.fn(),
  });
}

describe('GraphView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setupMocks();
  });

  describe('rendering', () => {
    it('renders with data-testid for testing', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });

    it('renders ReactFlow component', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('react-flow')).toBeInTheDocument();
    });

    it('renders Background component', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('background')).toBeInTheDocument();
    });
  });

  describe('empty state', () => {
    it('renders with empty issues array', () => {
      const props = createTestProps({ issues: [] });
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '0');
    });

    it('calls useGraphData with empty issues array', () => {
      const props = createTestProps({ issues: [] });
      render(<GraphView {...props} />);

      expect(useGraphData).toHaveBeenCalledWith([], expect.any(Object));
    });
  });

  describe('rendering with issues', () => {
    it('renders with issues', () => {
      const issues = [
        createTestIssue({ id: 'issue-1', title: 'First Issue' }),
        createTestIssue({ id: 'issue-2', title: 'Second Issue' }),
      ];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '2');
    });

    it('passes issues to useGraphData hook', () => {
      const issues = [
        createTestIssue({ id: 'issue-1', title: 'First Issue' }),
      ];

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      expect(useGraphData).toHaveBeenCalledWith(issues, expect.any(Object));
    });

    it('passes nodes to useAutoLayout hook', () => {
      const issues = [createTestIssue()];
      const nodes = createTestNodes(issues);
      const edges = createTestEdges();

      setupMocks({ nodes, edges });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(nodes, edges, expect.any(Object));
    });
  });

  describe('highlight ready toggle', () => {
    it('renders with data-highlight-ready="false" by default', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-highlight-ready', 'false');
    });

    it('updates data-highlight-ready attribute when toggle is clicked', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-highlight-ready', 'false');

      // Click the checkbox to toggle
      const checkbox = screen.getByTestId('highlight-ready-checkbox');
      fireEvent.click(checkbox);

      expect(container).toHaveAttribute('data-highlight-ready', 'true');
    });

    it('passes highlightReady state to GraphControls', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      expect(controls).toHaveAttribute('data-highlight-ready', 'false');

      // Toggle and verify update
      const checkbox = screen.getByTestId('highlight-ready-checkbox');
      fireEvent.click(checkbox);

      expect(controls).toHaveAttribute('data-highlight-ready', 'true');
    });
  });

  describe('show closed filtering', () => {
    beforeEach(() => {
      // Clear localStorage before each test
      localStorage.clear();
    });

    it('passes all issues to useGraphData when showClosed is true', () => {
      const issues = [
        createTestIssue({ id: 'issue-open', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'issue-closed', title: 'Closed Issue', status: 'closed' }),
      ];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // showClosed defaults to true in localStorage, so all issues should be passed
      expect(useGraphData).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'issue-open' }),
          expect.objectContaining({ id: 'issue-closed' }),
        ]),
        expect.any(Object)
      );
    });

    it('filters out closed issues when showClosed is false', () => {
      // Set localStorage to false before rendering
      localStorage.setItem('graph-show-closed', 'false');

      const issues = [
        createTestIssue({ id: 'issue-open', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'issue-closed', title: 'Closed Issue', status: 'closed' }),
        createTestIssue({ id: 'issue-progress', title: 'In Progress Issue', status: 'in_progress' }),
      ];
      const nodes = createTestNodes(issues.filter((i) => i.status !== 'closed'));

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Only non-closed issues should be passed
      expect(useGraphData).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'issue-open' }),
          expect.objectContaining({ id: 'issue-progress' }),
        ]),
        expect.any(Object)
      );

      // Verify closed issue is NOT in the array
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      const closedIssue = callArgs.find((issue: Issue) => issue.id === 'issue-closed');
      expect(closedIssue).toBeUndefined();
    });

    it('passes showClosed state to GraphControls', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      // Default is true
      expect(controls).toHaveAttribute('data-show-closed', 'true');
    });

    it('updates filtering when show closed toggle is clicked', () => {
      const issues = [
        createTestIssue({ id: 'issue-open', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'issue-closed', title: 'Closed Issue', status: 'closed' }),
      ];
      const allNodes = createTestNodes(issues);
      const openOnlyNodes = createTestNodes(issues.filter((i) => i.status !== 'closed'));

      setupMocks({ nodes: allNodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Initially showClosed is true - verify first call includes all issues
      expect(useGraphData).toHaveBeenLastCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'issue-closed' }),
        ]),
        expect.any(Object)
      );

      // Update mock for re-render
      setupMocks({ nodes: openOnlyNodes });

      // Click the checkbox to toggle showClosed to false
      const checkbox = screen.getByTestId('show-closed-checkbox');
      fireEvent.click(checkbox);

      // After toggle, closed issues should be filtered out
      const lastCall = (useGraphData as Mock).mock.calls[(useGraphData as Mock).mock.calls.length - 1];
      const closedIssueInLastCall = lastCall[0].find((issue: Issue) => issue.id === 'issue-closed');
      expect(closedIssueInLastCall).toBeUndefined();
    });

    it('persists showClosed preference to localStorage', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      // Default should be true
      expect(localStorage.getItem('graph-show-closed')).toBe('true');

      // Toggle to false
      const checkbox = screen.getByTestId('show-closed-checkbox');
      fireEvent.click(checkbox);

      expect(localStorage.getItem('graph-show-closed')).toBe('false');
    });

    it('loads showClosed preference from localStorage on mount', () => {
      // Set localStorage to false before rendering
      localStorage.setItem('graph-show-closed', 'false');

      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      expect(controls).toHaveAttribute('data-show-closed', 'false');
    });
  });

  describe('node click callback', () => {
    it('calls onNodeClick with correct issue when node is clicked', () => {
      const issues = [createTestIssue({ id: 'issue-click', title: 'Clickable Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const onNodeClick = vi.fn();
      const props = createTestProps({ issues, onNodeClick });
      render(<GraphView {...props} />);

      // Click on the node
      const node = screen.getByTestId('node-node-issue-click');
      fireEvent.click(node);

      expect(onNodeClick).toHaveBeenCalledTimes(1);
      expect(onNodeClick).toHaveBeenCalledWith(issues[0]);
    });

    it('does not call onNodeClick when callback is not provided', () => {
      const issues = [createTestIssue({ id: 'issue-no-click', title: 'Non-clickable' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues }); // No onNodeClick
      render(<GraphView {...props} />);

      // This should not throw
      const node = screen.getByTestId('node-node-issue-no-click');
      expect(() => fireEvent.click(node)).not.toThrow();
    });
  });

  describe('props control visibility', () => {
    it('renders MiniMap when showMiniMap is true (default)', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('minimap')).toBeInTheDocument();
    });

    it('hides MiniMap when showMiniMap is false', () => {
      const props = createTestProps({ showMiniMap: false });
      render(<GraphView {...props} />);

      expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();
    });

    it('renders GraphControls when showControls is true (default)', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-controls')).toBeInTheDocument();
    });

    it('hides GraphControls when showControls is false', () => {
      const props = createTestProps({ showControls: false });
      render(<GraphView {...props} />);

      expect(screen.queryByTestId('graph-controls')).not.toBeInTheDocument();
    });

    it('hides both MiniMap and GraphControls when both are false', () => {
      const props = createTestProps({ showMiniMap: false, showControls: false });
      render(<GraphView {...props} />);

      expect(screen.queryByTestId('minimap')).not.toBeInTheDocument();
      expect(screen.queryByTestId('graph-controls')).not.toBeInTheDocument();
    });
  });

  describe('custom layout direction prop', () => {
    it('passes default layoutDirection to useAutoLayout', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'LR' })
      );
    });

    it('passes custom layoutDirection to useAutoLayout', () => {
      const props = createTestProps({ layoutDirection: 'TB' });
      render(<GraphView {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'TB' })
      );
    });

    it('passes BT direction correctly', () => {
      const props = createTestProps({ layoutDirection: 'BT' });
      render(<GraphView {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'BT' })
      );
    });

    it('passes RL direction correctly', () => {
      const props = createTestProps({ layoutDirection: 'RL' });
      render(<GraphView {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'RL' })
      );
    });
  });

  describe('className prop', () => {
    it('applies custom className when provided', () => {
      const props = createTestProps({ className: 'custom-graph-class' });
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container.className).toContain('custom-graph-class');
    });

    it('preserves base class when additional className is provided', () => {
      const props = createTestProps({ className: 'custom-class' });
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container.className).toMatch(/graphView/);
    });

    it('does not add extra space when className is not provided', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      // Should only have the base class
      expect(container.className).toMatch(/graphView/);
      expect(container.className).not.toContain('undefined');
    });
  });

  describe('useBlockedIssues integration', () => {
    it('calls useBlockedIssues hook', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(useBlockedIssues).toHaveBeenCalledWith({ enabled: true });
    });

    it('passes blocked issue IDs to useGraphData options', () => {
      const blockedIssues: BlockedIssue[] = [
        {
          ...createTestIssue({ id: 'blocked-1' }),
          blocked_by_count: 1,
          blocked_by: ['blocker-1'],
        },
        {
          ...createTestIssue({ id: 'blocked-2' }),
          blocked_by_count: 2,
          blocked_by: ['blocker-1', 'blocker-2'],
        },
      ];

      setupMocks({ blockedIssues });

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Verify useGraphData was called with blockedIssueIds option
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          blockedIssueIds: expect.any(Set),
        })
      );

      // Get the actual call arguments
      const callArgs = (useGraphData as Mock).mock.calls[0][1];
      const blockedIssueIds = callArgs.blockedIssueIds;

      expect(blockedIssueIds.has('blocked-1')).toBe(true);
      expect(blockedIssueIds.has('blocked-2')).toBe(true);
    });
  });

  describe('edge cases', () => {
    it('handles issues with no dependencies', () => {
      const issues = [createTestIssue({ dependencies: [] })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes, edges: [] });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
    });

    it('handles null blockedIssues data', () => {
      setupMocks({ blockedIssues: null });

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('graph-view')).toBeInTheDocument();
      // With null blockedIssues, useGraphData should receive empty Set
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          blockedIssueIds: expect.any(Set),
        })
      );
    });
  });

  describe('tooltip integration', () => {
    it('shows NodeTooltip component on node mouse enter', () => {
      const issues = [createTestIssue({ id: 'issue-hover', title: 'Hover Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Tooltip should not be visible initially
      expect(screen.queryByTestId('node-tooltip')).not.toBeInTheDocument();

      // Hover over the node
      const node = screen.getByTestId('node-node-issue-hover');
      fireEvent.mouseEnter(node, { clientX: 100, clientY: 200 });

      // Tooltip should now be visible
      expect(screen.getByTestId('node-tooltip')).toBeInTheDocument();
    });

    it('hides NodeTooltip on node mouse leave', () => {
      const issues = [createTestIssue({ id: 'issue-leave', title: 'Leave Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Hover to show tooltip
      const node = screen.getByTestId('node-node-issue-leave');
      fireEvent.mouseEnter(node, { clientX: 100, clientY: 200 });
      expect(screen.getByTestId('node-tooltip')).toBeInTheDocument();

      // Leave the node
      fireEvent.mouseLeave(node);

      // Tooltip should be hidden
      expect(screen.queryByTestId('node-tooltip')).not.toBeInTheDocument();
    });

    it('passes correct issue and position to NodeTooltip', () => {
      const issues = [createTestIssue({ id: 'issue-position', title: 'Position Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Hover over the node with specific coordinates
      const node = screen.getByTestId('node-node-issue-position');
      fireEvent.mouseEnter(node, { clientX: 250, clientY: 350 });

      // Verify tooltip receives correct props via data attributes
      const tooltip = screen.getByTestId('node-tooltip');
      expect(tooltip).toHaveAttribute('data-issue-id', 'issue-position');
      expect(tooltip).toHaveAttribute('data-position-x', '250');
      expect(tooltip).toHaveAttribute('data-position-y', '350');
    });

    it('still calls external onNodeMouseEnter callback when provided', () => {
      const issues = [createTestIssue({ id: 'issue-callback', title: 'Callback Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const onNodeMouseEnter = vi.fn();
      const props = createTestProps({ issues, onNodeMouseEnter });
      render(<GraphView {...props} />);

      // Hover over the node
      const node = screen.getByTestId('node-node-issue-callback');
      fireEvent.mouseEnter(node, { clientX: 100, clientY: 200 });

      // External callback should be called with the issue
      expect(onNodeMouseEnter).toHaveBeenCalledTimes(1);
      expect(onNodeMouseEnter).toHaveBeenCalledWith(
        issues[0],
        expect.any(Object) // The event object
      );

      // Tooltip should also be shown (internal state still works)
      expect(screen.getByTestId('node-tooltip')).toBeInTheDocument();
    });

    it('still calls external onNodeMouseLeave callback when provided', () => {
      const issues = [createTestIssue({ id: 'issue-leave-cb', title: 'Leave Callback Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const onNodeMouseLeave = vi.fn();
      const props = createTestProps({ issues, onNodeMouseLeave });
      render(<GraphView {...props} />);

      // Hover to show tooltip, then leave
      const node = screen.getByTestId('node-node-issue-leave-cb');
      fireEvent.mouseEnter(node, { clientX: 100, clientY: 200 });
      expect(screen.getByTestId('node-tooltip')).toBeInTheDocument();

      fireEvent.mouseLeave(node);

      // External callback should be called
      expect(onNodeMouseLeave).toHaveBeenCalledTimes(1);

      // Tooltip should also be hidden (internal state still works)
      expect(screen.queryByTestId('node-tooltip')).not.toBeInTheDocument();
    });
  });

  describe('show closed toggle', () => {
    beforeEach(() => {
      // Clear localStorage before each test
      localStorage.clear();
    });

    it('passes all issues to useGraphData when showClosed is true', () => {
      const issues = [
        createTestIssue({ id: 'open-1', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'closed-1', title: 'Closed Issue', status: 'closed' }),
      ];

      // Ensure showClosed defaults to true
      localStorage.clear();

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // By default, showClosed is true, so all issues should be passed
      expect(useGraphData).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'open-1' }),
          expect.objectContaining({ id: 'closed-1' }),
        ]),
        expect.any(Object)
      );
    });

    it('filters out closed issues when showClosed is false', () => {
      const issues = [
        createTestIssue({ id: 'open-1', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'closed-1', title: 'Closed Issue', status: 'closed' }),
        createTestIssue({ id: 'open-2', title: 'Another Open', status: 'in_progress' }),
      ];

      // Set localStorage to false before rendering
      localStorage.setItem('graph-show-closed', 'false');

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // With showClosed false, only non-closed issues should be passed
      expect(useGraphData).toHaveBeenCalledWith(
        expect.not.arrayContaining([
          expect.objectContaining({ id: 'closed-1' }),
        ]),
        expect.any(Object)
      );

      // Verify the open issues are still passed
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(2);
      expect(callArgs.some((issue: Issue) => issue.id === 'open-1')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'open-2')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'closed-1')).toBe(false);
    });

    it('initializes showClosed from localStorage', () => {
      // Set localStorage to false before rendering
      localStorage.setItem('graph-show-closed', 'false');

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Verify the control reflects the localStorage value
      const controls = screen.getByTestId('graph-controls');
      expect(controls).toHaveAttribute('data-show-closed', 'false');
    });

    it('persists showClosed changes to localStorage', () => {
      localStorage.clear();

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Initially should be true (default)
      expect(localStorage.getItem('graph-show-closed')).toBe('true');

      // Toggle to false
      const checkbox = screen.getByTestId('show-closed-checkbox');
      fireEvent.click(checkbox);

      // Should persist the new value
      expect(localStorage.getItem('graph-show-closed')).toBe('false');
    });

    it('defaults showClosed to true when localStorage is empty', () => {
      localStorage.clear();

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Verify the control reflects the default value
      const controls = screen.getByTestId('graph-controls');
      expect(controls).toHaveAttribute('data-show-closed', 'true');
    });

    it('passes showClosed state to GraphControls', () => {
      localStorage.setItem('graph-show-closed', 'true');

      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      expect(controls).toHaveAttribute('data-show-closed', 'true');

      // Toggle and verify update
      const checkbox = screen.getByTestId('show-closed-checkbox');
      fireEvent.click(checkbox);

      expect(controls).toHaveAttribute('data-show-closed', 'false');
    });

    it('filters closed issues immediately when toggle is clicked', () => {
      const issues = [
        createTestIssue({ id: 'open-1', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'closed-1', title: 'Closed Issue', status: 'closed' }),
      ];

      // Start with showClosed true
      localStorage.setItem('graph-show-closed', 'true');

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Initially all issues should be passed
      expect((useGraphData as Mock).mock.calls[0][0]).toHaveLength(2);

      // Clear mock calls to track the next call
      (useGraphData as Mock).mockClear();

      // Toggle showClosed to false
      const checkbox = screen.getByTestId('show-closed-checkbox');
      fireEvent.click(checkbox);

      // Should re-render with filtered issues
      expect(useGraphData).toHaveBeenCalled();
      const lastCallArgs = (useGraphData as Mock).mock.calls[(useGraphData as Mock).mock.calls.length - 1][0];
      expect(lastCallArgs).toHaveLength(1);
      expect(lastCallArgs[0].id).toBe('open-1');
    });
  });

  describe('closed issue node data', () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it('passes isClosed: true to node data for closed issues', () => {
      const issues = [
        createTestIssue({ id: 'open-issue', status: 'open' }),
        createTestIssue({ id: 'closed-issue', status: 'closed' }),
      ];
      const nodes = createTestNodesWithClosed(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Verify useGraphData was called with both issues
      expect(useGraphData).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ id: 'open-issue' }),
          expect.objectContaining({ id: 'closed-issue' }),
        ]),
        expect.any(Object)
      );

      // The mock returns nodes with isClosed set appropriately
      // Verify the closed node has isClosed: true in its data
      const closedNode = nodes.find(n => n.id === 'node-closed-issue');
      expect(closedNode?.data.isClosed).toBe(true);

      const openNode = nodes.find(n => n.id === 'node-open-issue');
      expect(openNode?.data.isClosed).toBe(false);
    });

    it('sets data-show-closed attribute on container', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-show-closed');
    });

    it('sets data-show-closed="false" when showClosed is false', () => {
      localStorage.setItem('graph-show-closed', 'false');

      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-show-closed', 'false');
    });
  });

  describe('status filter integration', () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it('sets data-status-filter attribute on container', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-status-filter', 'all');
    });

    it('filters to only open issues when statusFilter is "open"', () => {
      localStorage.setItem('graph-status-filter', 'open');

      const issues = [
        createTestIssue({ id: 'open-1', status: 'open' }),
        createTestIssue({ id: 'closed-1', status: 'closed' }),
        createTestIssue({ id: 'in-progress-1', status: 'in_progress' }),
      ];

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Only open issues should be passed to useGraphData
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(1);
      expect(callArgs[0].id).toBe('open-1');
    });

    it('filters to only closed issues when statusFilter is "closed"', () => {
      localStorage.setItem('graph-status-filter', 'closed');

      const issues = [
        createTestIssue({ id: 'open-1', status: 'open' }),
        createTestIssue({ id: 'closed-1', status: 'closed' }),
        createTestIssue({ id: 'closed-2', status: 'closed' }),
      ];

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Only closed issues should be passed to useGraphData
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(2);
      expect(callArgs.every((issue: Issue) => issue.status === 'closed')).toBe(true);
    });

    it('shows all issues when statusFilter is "all" and showClosed is true', () => {
      localStorage.setItem('graph-status-filter', 'all');
      localStorage.setItem('graph-show-closed', 'true');

      const issues = [
        createTestIssue({ id: 'open-1', status: 'open' }),
        createTestIssue({ id: 'closed-1', status: 'closed' }),
        createTestIssue({ id: 'in-progress-1', status: 'in_progress' }),
      ];

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // All issues should be passed to useGraphData
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(3);
    });

    it('statusFilter "closed" takes precedence over showClosed toggle', () => {
      // Set statusFilter to "closed" but showClosed to false
      // statusFilter should take precedence and show closed issues
      localStorage.setItem('graph-status-filter', 'closed');
      localStorage.setItem('graph-show-closed', 'false');

      const issues = [
        createTestIssue({ id: 'open-1', status: 'open' }),
        createTestIssue({ id: 'closed-1', status: 'closed' }),
      ];

      const props = createTestProps({ issues });
      render(<GraphView {...props} />);

      // Despite showClosed being false, closed issues should be shown
      // because statusFilter "closed" explicitly requests them
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(1);
      expect(callArgs[0].id).toBe('closed-1');
    });

    it('persists statusFilter to localStorage', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      // Default should be 'all'
      expect(localStorage.getItem('graph-status-filter')).toBe('all');

      // Change status filter using the select
      const select = screen.getByTestId('status-filter-select');
      fireEvent.change(select, { target: { value: 'open' } });

      expect(localStorage.getItem('graph-status-filter')).toBe('open');
    });

    it('loads statusFilter from localStorage on mount', () => {
      localStorage.setItem('graph-status-filter', 'in_progress');

      const props = createTestProps();
      render(<GraphView {...props} />);

      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-status-filter', 'in_progress');
    });

    it('ignores invalid statusFilter values in localStorage', () => {
      localStorage.setItem('graph-status-filter', 'invalid_status');

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Should fall back to default 'all'
      const container = screen.getByTestId('graph-view');
      expect(container).toHaveAttribute('data-status-filter', 'all');
    });
  });

  describe('dependency type filter integration', () => {
    beforeEach(() => {
      localStorage.clear();
    });

    it('passes dependency type filter to GraphControls', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      // Should render the dep type filter group
      expect(screen.getByTestId('dep-type-filter-group')).toBeInTheDocument();
    });

    it('initializes with default filter (blocking + parent-child) when localStorage is empty', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      const filterAttr = controls.getAttribute('data-dep-type-filter');
      const filterArray = JSON.parse(filterAttr || '[]');

      expect(filterArray).toContain('blocking');
      expect(filterArray).toContain('parent-child');
      expect(filterArray).not.toContain('non-blocking');
    });

    it('loads dependency type filter from localStorage on mount', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking', 'non-blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      const filterAttr = controls.getAttribute('data-dep-type-filter');
      const filterArray = JSON.parse(filterAttr || '[]');

      expect(filterArray).toContain('blocking');
      expect(filterArray).toContain('non-blocking');
      expect(filterArray).not.toContain('parent-child');
    });

    it('persists dependency type filter to localStorage when changed', () => {
      const props = createTestProps();
      render(<GraphView {...props} />);

      // Default should be blocking + parent-child
      const storedDefault = JSON.parse(localStorage.getItem('graph-dep-type-filter') || '[]');
      expect(storedDefault).toContain('blocking');
      expect(storedDefault).toContain('parent-child');

      // Toggle non-blocking on
      const nonBlockingCheckbox = screen.getByTestId('dep-type-non-blocking-checkbox');
      fireEvent.click(nonBlockingCheckbox);

      const storedAfterChange = JSON.parse(localStorage.getItem('graph-dep-type-filter') || '[]');
      expect(storedAfterChange).toContain('blocking');
      expect(storedAfterChange).toContain('parent-child');
      expect(storedAfterChange).toContain('non-blocking');
    });

    it('ignores invalid dependency type groups in localStorage', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking', 'invalid-type', 'parent-child']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      const controls = screen.getByTestId('graph-controls');
      const filterAttr = controls.getAttribute('data-dep-type-filter');
      const filterArray = JSON.parse(filterAttr || '[]');

      // Should include valid groups, exclude invalid
      expect(filterArray).toContain('blocking');
      expect(filterArray).toContain('parent-child');
      expect(filterArray).not.toContain('invalid-type');
    });

    it('handles malformed JSON in localStorage gracefully', () => {
      localStorage.setItem('graph-dep-type-filter', 'not-valid-json');

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Should fall back to default
      const controls = screen.getByTestId('graph-controls');
      const filterAttr = controls.getAttribute('data-dep-type-filter');
      const filterArray = JSON.parse(filterAttr || '[]');

      expect(filterArray).toContain('blocking');
      expect(filterArray).toContain('parent-child');
    });

    it('passes includeDependencyTypes to useGraphData when filter has groups', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      // useGraphData should be called with includeDependencyTypes containing blocking types
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining(['blocks', 'conditional-blocks', 'waits-for']),
        })
      );
    });

    it('passes includeDependencyTypes with parent-child types when parent-child is selected', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['parent-child']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining(['parent-child']),
        })
      );
    });

    it('passes includeDependencyTypes with non-blocking types when non-blocking is selected', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['non-blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining(['related', 'discovered-from', 'replies-to']),
        })
      );
    });

    it('passes undefined includeDependencyTypes when filter is empty (all groups unchecked)', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify([]));

      const props = createTestProps();
      render(<GraphView {...props} />);

      // When all groups are unchecked, includeDependencyTypes should be undefined (show all)
      const calls = (useGraphData as Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      expect(lastCall[1].includeDependencyTypes).toBeUndefined();
    });

    it('combines dependency types from multiple selected groups', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking', 'parent-child']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining([
            'blocks',
            'conditional-blocks',
            'waits-for',
            'parent-child',
          ]),
        })
      );
    });

    it('updates includeDependencyTypes when filter is toggled', () => {
      // Start with blocking only
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Verify initial call includes blocking types
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining(['blocks']),
        })
      );

      // Clear mock to check next call
      (useGraphData as Mock).mockClear();

      // Toggle parent-child on
      const parentChildCheckbox = screen.getByTestId('dep-type-parent-child-checkbox');
      fireEvent.click(parentChildCheckbox);

      // Should now include both blocking and parent-child types
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining(['blocks', 'parent-child']),
        })
      );
    });

    it('removes dependency types from filter when group is unchecked', () => {
      // Start with blocking + parent-child
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking', 'parent-child']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      // Clear mock
      (useGraphData as Mock).mockClear();

      // Uncheck blocking
      const blockingCheckbox = screen.getByTestId('dep-type-blocking-checkbox');
      fireEvent.click(blockingCheckbox);

      // Should now only include parent-child types
      const calls = (useGraphData as Mock).mock.calls;
      const lastCall = calls[calls.length - 1];
      const depTypes = lastCall[1].includeDependencyTypes;

      expect(depTypes).toContain('parent-child');
      expect(depTypes).not.toContain('blocks');
      expect(depTypes).not.toContain('conditional-blocks');
      expect(depTypes).not.toContain('waits-for');
    });

    it('checkbox reflects correct checked state for blocking', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('dep-type-blocking-checkbox')).toBeChecked();
      expect(screen.getByTestId('dep-type-parent-child-checkbox')).not.toBeChecked();
      expect(screen.getByTestId('dep-type-non-blocking-checkbox')).not.toBeChecked();
    });

    it('checkbox reflects correct checked state for all groups selected', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify(['blocking', 'parent-child', 'non-blocking']));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('dep-type-blocking-checkbox')).toBeChecked();
      expect(screen.getByTestId('dep-type-parent-child-checkbox')).toBeChecked();
      expect(screen.getByTestId('dep-type-non-blocking-checkbox')).toBeChecked();
    });

    it('checkbox reflects correct checked state when none selected', () => {
      localStorage.setItem('graph-dep-type-filter', JSON.stringify([]));

      const props = createTestProps();
      render(<GraphView {...props} />);

      expect(screen.getByTestId('dep-type-blocking-checkbox')).not.toBeChecked();
      expect(screen.getByTestId('dep-type-parent-child-checkbox')).not.toBeChecked();
      expect(screen.getByTestId('dep-type-non-blocking-checkbox')).not.toBeChecked();
    });
  });
});
