/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for MiniDependencyGraph component.
 */

import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { MiniDependencyGraph } from '../MiniDependencyGraph';
import type { MiniDependencyGraphProps } from '../MiniDependencyGraph';
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
  ReactFlow: vi.fn(({ children, onNodeClick, nodes }) => (
    <div data-testid="react-flow" data-node-count={nodes?.length ?? 0}>
      {/* Simulate node rendering to test click handlers */}
      {nodes?.map((node: IssueNode) => (
        <div
          key={node.id}
          data-testid={`node-${node.id}`}
          onClick={(event) => onNodeClick?.(event, node)}
        >
          {node.data?.title}
        </div>
      ))}
      {children}
    </div>
  )),
}));

// Mock child components
vi.mock('@/components', () => ({
  IssueNode: vi.fn(() => <div data-testid="issue-node" />),
  DependencyEdge: vi.fn(() => <div data-testid="dependency-edge" />),
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
      blockedCount: 0,
      isRootBlocker: false,
      isClosed: issue.status === 'closed',
    },
  }));
}

/**
 * Create test edges.
 */
function createTestEdges(): DependencyEdge[] {
  return [];
}

/**
 * Create test props for MiniDependencyGraph component.
 */
function createTestProps(overrides: Partial<MiniDependencyGraphProps> = {}): MiniDependencyGraphProps {
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
    orphanEdgeCount: 0,
    missingTargetIds: new Set<string>(),
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

describe('MiniDependencyGraph', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setupMocks();
  });

  describe('rendering', () => {
    it('renders with testid "mini-dependency-graph"', () => {
      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
    });

    it('shows empty state when no issues', () => {
      const props = createTestProps({ issues: [] });
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
      // Should show empty state message, not ReactFlow
      expect(screen.queryByTestId('react-flow')).not.toBeInTheDocument();
    });

    it('shows empty state message "No blocking dependencies" when issues have no blocking deps', () => {
      const props = createTestProps({ issues: [] });
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByText('No blocking dependencies')).toBeInTheDocument();
    });

    it('renders nodes for issues with blocking deps', () => {
      const issues = [
        createTestIssue({ id: 'issue-1', title: 'First Issue' }),
        createTestIssue({ id: 'issue-2', title: 'Second Issue' }),
      ];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('react-flow')).toBeInTheDocument();
      expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '2');
    });
  });

  describe('filtering', () => {
    it('only shows blocking dependency types (blocks, conditional-blocks, waits-for)', () => {
      const issues = [createTestIssue({ id: 'issue-1', title: 'Test Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues });
      render(<MiniDependencyGraph {...props} />);

      // Verify useGraphData was called with includeDependencyTypes containing only blocking types
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          includeDependencyTypes: expect.arrayContaining([
            'blocks',
            'conditional-blocks',
            'waits-for',
          ]),
        })
      );

      // Verify non-blocking types are not included
      const callArgs = (useGraphData as Mock).mock.calls[0][1];
      const depTypes = callArgs.includeDependencyTypes;
      expect(depTypes).not.toContain('related');
      expect(depTypes).not.toContain('discovered-from');
      expect(depTypes).not.toContain('replies-to');
    });

    it('excludes closed issues from visualization', () => {
      const issues = [
        createTestIssue({ id: 'open-1', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'closed-1', title: 'Closed Issue', status: 'closed' }),
        createTestIssue({ id: 'in-progress-1', title: 'In Progress Issue', status: 'in_progress' }),
      ];

      const props = createTestProps({ issues });
      render(<MiniDependencyGraph {...props} />);

      // Verify useGraphData was called without closed issues
      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(2);
      expect(callArgs.some((issue: Issue) => issue.id === 'open-1')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'in-progress-1')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'closed-1')).toBe(false);
    });
  });

  describe('interactions', () => {
    it('calls onNodeClick with correct issue when node clicked', () => {
      const issues = [createTestIssue({ id: 'issue-click', title: 'Clickable Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const onNodeClick = vi.fn();
      const props = createTestProps({ issues, onNodeClick });
      render(<MiniDependencyGraph {...props} />);

      // Click on the node
      const node = screen.getByTestId('node-node-issue-click');
      fireEvent.click(node);

      expect(onNodeClick).toHaveBeenCalledTimes(1);
      expect(onNodeClick).toHaveBeenCalledWith(issues[0]);
    });

    it('does not throw when onNodeClick is not provided', () => {
      const issues = [createTestIssue({ id: 'issue-no-click', title: 'Non-clickable' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const props = createTestProps({ issues }); // No onNodeClick
      render(<MiniDependencyGraph {...props} />);

      // This should not throw
      const node = screen.getByTestId('node-node-issue-no-click');
      expect(() => fireEvent.click(node)).not.toThrow();
    });

    it('calls onExpandClick when expand button clicked', () => {
      const onExpandClick = vi.fn();
      const props = createTestProps({ onExpandClick });
      render(<MiniDependencyGraph {...props} />);

      const expandButton = screen.getByRole('button', { name: /expand to full graph view/i });
      fireEvent.click(expandButton);

      expect(onExpandClick).toHaveBeenCalledTimes(1);
    });

    it('does not render expand button when onExpandClick is not provided', () => {
      const props = createTestProps(); // No onExpandClick
      render(<MiniDependencyGraph {...props} />);

      expect(screen.queryByRole('button', { name: /expand to full graph view/i })).not.toBeInTheDocument();
    });

    it('expand button has accessible label', () => {
      const onExpandClick = vi.fn();
      const props = createTestProps({ onExpandClick });
      render(<MiniDependencyGraph {...props} />);

      const expandButton = screen.getByRole('button', { name: /expand to full graph view/i });
      expect(expandButton).toHaveAttribute('aria-label', 'Expand to full graph view');
    });
  });

  describe('props', () => {
    it('applies custom className', () => {
      const props = createTestProps({ className: 'custom-graph-class' });
      render(<MiniDependencyGraph {...props} />);

      const container = screen.getByTestId('mini-dependency-graph');
      expect(container.className).toContain('custom-graph-class');
    });

    it('preserves base class when additional className is provided', () => {
      const props = createTestProps({ className: 'custom-class' });
      render(<MiniDependencyGraph {...props} />);

      const container = screen.getByTestId('mini-dependency-graph');
      expect(container.className).toMatch(/miniGraph/);
    });

    it('does not add extra class when className is not provided', () => {
      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

      const container = screen.getByTestId('mini-dependency-graph');
      expect(container.className).toMatch(/miniGraph/);
      expect(container.className).not.toContain('undefined');
    });

    it('respects layoutDirection prop with default LR', () => {
      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'LR' })
      );
    });

    it('respects layoutDirection prop TB', () => {
      const props = createTestProps({ layoutDirection: 'TB' });
      render(<MiniDependencyGraph {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({ direction: 'TB' })
      );
    });
  });

  describe('hook integration', () => {
    it('calls useBlockedIssues hook', () => {
      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

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
      render(<MiniDependencyGraph {...props} />);

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

    it('handles null blockedIssues data', () => {
      setupMocks({ blockedIssues: null });

      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
      // With null blockedIssues, useGraphData should receive empty Set
      expect(useGraphData).toHaveBeenCalledWith(
        expect.anything(),
        expect.objectContaining({
          blockedIssueIds: expect.any(Set),
        })
      );

      // Verify empty set
      const callArgs = (useGraphData as Mock).mock.calls[0][1];
      expect(callArgs.blockedIssueIds.size).toBe(0);
    });

    it('passes nodes to useAutoLayout hook', () => {
      const issues = [createTestIssue()];
      const nodes = createTestNodes(issues);
      const edges = createTestEdges();

      setupMocks({ nodes, edges });

      const props = createTestProps({ issues });
      render(<MiniDependencyGraph {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(nodes, edges, expect.any(Object));
    });

    it('uses compact spacing for mini view', () => {
      const props = createTestProps();
      render(<MiniDependencyGraph {...props} />);

      expect(useAutoLayout).toHaveBeenCalledWith(
        expect.anything(),
        expect.anything(),
        expect.objectContaining({
          nodesep: 30,
          ranksep: 60,
        })
      );
    });
  });

  describe('edge cases', () => {
    it('handles issues with no dependencies', () => {
      const issues = [createTestIssue({ dependencies: [] })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes, edges: [] });

      const props = createTestProps({ issues });
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
    });

    it('handles empty issues array gracefully', () => {
      const props = createTestProps({ issues: [] });
      render(<MiniDependencyGraph {...props} />);

      expect(screen.getByTestId('mini-dependency-graph')).toBeInTheDocument();
      expect(screen.getByText('No blocking dependencies')).toBeInTheDocument();
    });

    it('handles node click when node data has no issue', () => {
      const nodesWithoutIssue: IssueNode[] = [
        {
          id: 'node-orphan',
          type: 'issue',
          position: { x: 0, y: 0 },
          data: {
            issue: undefined as unknown as Issue,
            title: 'Orphan Node',
            status: undefined,
            priority: 4,
            issueType: undefined,
            dependencyCount: 0,
            dependentCount: 0,
            isReady: false,
            blockedCount: 0,
            isRootBlocker: false,
            isClosed: false,
          },
        },
      ];

      setupMocks({ nodes: nodesWithoutIssue });

      const onNodeClick = vi.fn();
      const props = createTestProps({ issues: [], onNodeClick });
      render(<MiniDependencyGraph {...props} />);

      // Click should not call onNodeClick when node.data.issue is undefined
      const node = screen.getByTestId('node-node-orphan');
      fireEvent.click(node);

      expect(onNodeClick).not.toHaveBeenCalled();
    });
  });
});
