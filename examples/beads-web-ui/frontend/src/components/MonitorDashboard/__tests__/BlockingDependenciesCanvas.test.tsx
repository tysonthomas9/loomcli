/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BlockingDependenciesCanvas component.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, type Mock } from 'vitest';
import '@testing-library/jest-dom';

// Import mocks after vi.mock calls
import { useAutoLayout } from '@/hooks/useAutoLayout';
import { useBlockedIssues } from '@/hooks/useBlockedIssues';
import { useGraphData } from '@/hooks/useGraphData';
import type { Issue, IssueNode, DependencyEdge, BlockedIssue } from '@/types';

import { BlockingDependenciesCanvas } from '../BlockingDependenciesCanvas';

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
  Controls: vi.fn(() => <div data-testid="zoom-controls" />),
}));

// Mock child components
vi.mock('../BlockingNode', () => ({
  BlockingNode: vi.fn(() => <div data-testid="blocking-node" />),
}));

vi.mock('../BlockingEdge', () => ({
  BlockingEdge: vi.fn(() => <div data-testid="blocking-edge" />),
}));

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
 * Setup mocks with default return values.
 */
function setupMocks(
  options: {
    nodes?: IssueNode[];
    edges?: DependencyEdge[];
    blockedIssues?: BlockedIssue[] | null;
  } = {}
) {
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

describe('BlockingDependenciesCanvas', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    setupMocks();
  });

  describe('empty state', () => {
    it('renders empty state when no blocking dependencies', () => {
      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(screen.getByText('No blocking dependencies')).toBeInTheDocument();
      expect(screen.queryByTestId('react-flow')).not.toBeInTheDocument();
    });

    it('renders with testid "blocking-dependencies-canvas"', () => {
      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(screen.getByTestId('blocking-dependencies-canvas')).toBeInTheDocument();
    });
  });

  describe('rendering with data', () => {
    it('renders ReactFlow when blocking dependencies exist', () => {
      const issues = [
        createTestIssue({ id: 'issue-1', title: 'First Issue' }),
        createTestIssue({ id: 'issue-2', title: 'Second Issue' }),
      ];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      render(<BlockingDependenciesCanvas issues={issues} />);

      expect(screen.getByTestId('react-flow')).toBeInTheDocument();
      expect(screen.getByTestId('react-flow')).toHaveAttribute('data-node-count', '2');
    });

    it('renders legend with correct color indicators', () => {
      const issues = [createTestIssue({ id: 'issue-1', title: 'Test' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      render(<BlockingDependenciesCanvas issues={issues} />);

      const legend = screen.getByLabelText('Graph legend');
      expect(legend).toBeInTheDocument();
      expect(screen.getByText('Healthy')).toBeInTheDocument();
      expect(screen.getByText('Blocking')).toBeInTheDocument();
      expect(screen.getByText('Blocked')).toBeInTheDocument();
    });

    it('renders zoom controls', () => {
      const issues = [createTestIssue({ id: 'issue-1', title: 'Test' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      render(<BlockingDependenciesCanvas issues={issues} />);

      expect(screen.getByTestId('zoom-controls')).toBeInTheDocument();
    });
  });

  describe('interactions', () => {
    it('calls onNodeClick when a node is clicked', () => {
      const issues = [createTestIssue({ id: 'issue-click', title: 'Clickable Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      const onNodeClick = vi.fn();
      render(<BlockingDependenciesCanvas issues={issues} onNodeClick={onNodeClick} />);

      const node = screen.getByTestId('node-node-issue-click');
      fireEvent.click(node);

      expect(onNodeClick).toHaveBeenCalledTimes(1);
      expect(onNodeClick).toHaveBeenCalledWith(issues[0]);
    });

    it('does not throw when onNodeClick is not provided', () => {
      const issues = [createTestIssue({ id: 'issue-no-click', title: 'Non-clickable' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      render(<BlockingDependenciesCanvas issues={issues} />);

      const node = screen.getByTestId('node-node-issue-no-click');
      expect(() => fireEvent.click(node)).not.toThrow();
    });

    it('calls onExpandClick when expand button is clicked', () => {
      const onExpandClick = vi.fn();
      render(<BlockingDependenciesCanvas issues={[]} onExpandClick={onExpandClick} />);

      const expandButton = screen.getByRole('button', { name: /expand to full graph view/i });
      fireEvent.click(expandButton);

      expect(onExpandClick).toHaveBeenCalledTimes(1);
    });

    it('does not render expand button when onExpandClick is not provided', () => {
      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(
        screen.queryByRole('button', { name: /expand to full graph view/i })
      ).not.toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies custom className', () => {
      render(<BlockingDependenciesCanvas issues={[]} className="custom-class" />);

      const container = screen.getByTestId('blocking-dependencies-canvas');
      expect(container.className).toContain('custom-class');
    });

    it('preserves base class when additional className is provided', () => {
      render(<BlockingDependenciesCanvas issues={[]} className="custom-class" />);

      const container = screen.getByTestId('blocking-dependencies-canvas');
      expect(container.className).toMatch(/canvas/);
    });
  });

  describe('filtering', () => {
    it('excludes closed issues from visualization', () => {
      const issues = [
        createTestIssue({ id: 'open-1', title: 'Open Issue', status: 'open' }),
        createTestIssue({ id: 'closed-1', title: 'Closed Issue', status: 'closed' }),
        createTestIssue({ id: 'in-progress-1', title: 'In Progress Issue', status: 'in_progress' }),
      ];

      render(<BlockingDependenciesCanvas issues={issues} />);

      const callArgs = (useGraphData as Mock).mock.calls[0][0];
      expect(callArgs).toHaveLength(2);
      expect(callArgs.some((issue: Issue) => issue.id === 'open-1')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'in-progress-1')).toBe(true);
      expect(callArgs.some((issue: Issue) => issue.id === 'closed-1')).toBe(false);
    });

    it('only shows blocking dependency types', () => {
      const issues = [createTestIssue({ id: 'issue-1', title: 'Test Issue' })];
      const nodes = createTestNodes(issues);

      setupMocks({ nodes });

      render(<BlockingDependenciesCanvas issues={issues} />);

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
    });
  });

  describe('hook integration', () => {
    it('calls useBlockedIssues hook', () => {
      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(useBlockedIssues).toHaveBeenCalledWith({ enabled: true });
    });

    it('passes blocked issue IDs to useGraphData options', () => {
      const blockedIssues: BlockedIssue[] = [
        {
          ...createTestIssue({ id: 'blocked-1' }),
          blocked_by_count: 1,
          blocked_by: ['blocker-1'],
        },
      ];

      setupMocks({ blockedIssues });

      render(<BlockingDependenciesCanvas issues={[]} />);

      const callArgs = (useGraphData as Mock).mock.calls[0][1];
      const blockedIssueIds = callArgs.blockedIssueIds;
      expect(blockedIssueIds.has('blocked-1')).toBe(true);
    });

    it('handles null blockedIssues data', () => {
      setupMocks({ blockedIssues: null });

      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(screen.getByTestId('blocking-dependencies-canvas')).toBeInTheDocument();
      const callArgs = (useGraphData as Mock).mock.calls[0][1];
      expect(callArgs.blockedIssueIds.size).toBe(0);
    });
  });

  describe('edge cases', () => {
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
      render(<BlockingDependenciesCanvas issues={[]} onNodeClick={onNodeClick} />);

      const node = screen.getByTestId('node-node-orphan');
      fireEvent.click(node);

      expect(onNodeClick).not.toHaveBeenCalled();
    });

    it('handles empty issues array gracefully', () => {
      render(<BlockingDependenciesCanvas issues={[]} />);

      expect(screen.getByTestId('blocking-dependencies-canvas')).toBeInTheDocument();
      expect(screen.getByText('No blocking dependencies')).toBeInTheDocument();
    });
  });
});
