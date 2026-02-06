/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for BlockingNode component.
 */

import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import type { NodeProps } from '@xyflow/react';
import { describe, it, expect } from 'vitest';

import type { Issue, IssueNodeData, IssueNode as IssueNodeType } from '@/types';

import { BlockingNode } from '../BlockingNode';

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
 * Create test node data for BlockingNode component.
 */
function createTestNodeData(overrides: Partial<IssueNodeData> = {}): IssueNodeData {
  const issue = overrides.issue || createTestIssue();
  return {
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
    isClosed: false,
    ...overrides,
  };
}

/**
 * Create test props for BlockingNode component.
 */
function createTestProps(
  overrides: Partial<NodeProps<IssueNodeType>> = {}
): NodeProps<IssueNodeType> {
  const data = overrides.data || createTestNodeData();
  return {
    id: 'node-1',
    data,
    type: 'issue',
    selected: false,
    isConnectable: true,
    zIndex: 0,
    positionAbsoluteX: 0,
    positionAbsoluteY: 0,
    ...overrides,
  } as NodeProps<IssueNodeType>;
}

/**
 * Wrapper component to provide ReactFlow context.
 */
function renderWithProvider(props: NodeProps<IssueNodeType>) {
  return render(
    <ReactFlowProvider>
      <BlockingNode {...props} />
    </ReactFlowProvider>
  );
}

describe('BlockingNode', () => {
  describe('rendering', () => {
    it('renders issue ID, title, and description', () => {
      const issue = createTestIssue({
        id: 'bd-short',
        title: 'My Task',
        description: 'A detailed description',
      });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: 'My Task' }),
      });
      renderWithProvider(props);

      expect(screen.getByText('bd-short')).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'My Task' })).toBeInTheDocument();
      expect(screen.getByText('A detailed description')).toBeInTheDocument();
    });

    it('renders shortened issue ID for long IDs', () => {
      const issue = createTestIssue({ id: 'beads-abc123def456' });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      expect(screen.getByText('3def456')).toBeInTheDocument();
    });

    it('renders Untitled for empty title', () => {
      const issue = createTestIssue({ title: '' });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: '' }),
      });
      renderWithProvider(props);

      expect(screen.getByRole('heading', { name: 'Untitled' })).toBeInTheDocument();
    });

    it('renders with article element', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(props);

      expect(container.querySelector('article')).toBeInTheDocument();
    });
  });

  describe('status badges', () => {
    it('shows "Healthy" badge for non-blocked nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: true,
          blockedCount: 0,
          isRootBlocker: false,
          isClosed: false,
        }),
      });
      renderWithProvider(props);

      expect(screen.getByText('Healthy')).toBeInTheDocument();
    });

    it('shows "Waiting" badge for blocked nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: false,
          isClosed: false,
          blockedCount: 0,
          isRootBlocker: false,
        }),
      });
      renderWithProvider(props);

      expect(screen.getByText('Waiting')).toBeInTheDocument();
    });

    it('shows "Blocking" badge text for blocking nodes (not blocked themselves)', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: true,
          blockedCount: 3,
          isRootBlocker: true,
          isClosed: false,
        }),
      });
      renderWithProvider(props);

      const badge = screen.getByText('Blocking');
      expect(badge).toBeInTheDocument();
      expect(badge).toHaveAttribute('data-status', 'blocking');
    });
  });

  describe('blocking indicators', () => {
    it('shows "Blocking N" count for blocker nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          blockedCount: 3,
          isRootBlocker: true,
          isReady: true,
          isClosed: false,
        }),
      });
      renderWithProvider(props);

      expect(screen.getByText('Blocking 3')).toBeInTheDocument();
    });

    it('does not show blocking count when blockedCount is 0', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({ issue, blockedCount: 0, isReady: true, isClosed: false }),
      });
      renderWithProvider(props);

      expect(screen.queryByText(/Blocking \d/)).not.toBeInTheDocument();
    });

    it('re-renders when issue.dependencies change', () => {
      const issue1 = createTestIssue({
        dependencies: [{ depends_on_id: 'dep-aaa1111', type: 'blocks' }] as Issue['dependencies'],
      });
      const props1 = createTestProps({
        data: createTestNodeData({ issue: issue1, isReady: false, isClosed: false }),
      });

      const { rerender } = render(
        <ReactFlowProvider>
          <BlockingNode {...props1} />
        </ReactFlowProvider>
      );
      expect(screen.getByText(/aa1111/)).toBeInTheDocument();

      const issue2 = createTestIssue({
        dependencies: [{ depends_on_id: 'dep-bbb2222', type: 'blocks' }] as Issue['dependencies'],
      });
      const props2 = createTestProps({
        data: createTestNodeData({ issue: issue2, isReady: false, isClosed: false }),
      });

      rerender(
        <ReactFlowProvider>
          <BlockingNode {...props2} />
        </ReactFlowProvider>
      );
      expect(screen.getByText(/bb2222/)).toBeInTheDocument();
    });

    it('shows "Blocked by" links for blocked nodes', () => {
      const issue = createTestIssue({
        dependencies: [
          { depends_on_id: 'dep-abc1234', type: 'blocks' },
          { depends_on_id: 'dep-xyz5678', type: 'blocks' },
        ] as Issue['dependencies'],
      });
      const props = createTestProps({
        data: createTestNodeData({ issue, isReady: false, isClosed: false }),
      });
      renderWithProvider(props);

      expect(screen.getByText(/Blocked by/)).toBeInTheDocument();
      expect(screen.getByText(/bc1234/)).toBeInTheDocument();
      expect(screen.getByText(/z5678/)).toBeInTheDocument();
    });
  });

  describe('description handling', () => {
    it('handles missing description gracefully', () => {
      const issue = createTestIssue({
        description: undefined,
        notes: undefined,
      });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      const { container } = renderWithProvider(props);

      // Description paragraph should not be rendered
      const description = container.querySelector('p');
      expect(description).not.toBeInTheDocument();
    });

    it('shows notes when description is absent', () => {
      const issue = createTestIssue({
        description: undefined,
        notes: 'Some notes here',
      });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      expect(screen.getByText('Some notes here')).toBeInTheDocument();
    });
  });

  describe('data-node-status attribute', () => {
    it('applies data-node-status="healthy" for healthy nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: true,
          blockedCount: 0,
          isRootBlocker: false,
          isClosed: false,
        }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-node-status', 'healthy');
    });

    it('applies data-node-status="blocked" for blocked nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: false,
          isClosed: false,
          blockedCount: 0,
          isRootBlocker: false,
        }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-node-status', 'blocked');
    });

    it('applies data-node-status="blocking" for blocker nodes', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({
          issue,
          isReady: true,
          blockedCount: 2,
          isRootBlocker: true,
          isClosed: false,
        }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-node-status', 'blocking');
    });
  });

  describe('selection state', () => {
    it('applies selected class when selected prop is true', () => {
      const props = createTestProps({ selected: true });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article?.className).toMatch(/selected/);
    });

    it('does not apply selected class when false', () => {
      const props = createTestProps({ selected: false });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article?.className).not.toMatch(/selected/);
    });
  });

  describe('handles', () => {
    it('renders target handle on left', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(props);

      const targetHandle = container.querySelector('[data-handlepos="left"]');
      expect(targetHandle).toBeInTheDocument();
    });

    it('renders source handle on right', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(props);

      const sourceHandle = container.querySelector('[data-handlepos="right"]');
      expect(sourceHandle).toBeInTheDocument();
    });
  });

  describe('accessibility', () => {
    it('has aria-label with issue title', () => {
      const issue = createTestIssue({ title: 'Test Accessibility' });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: 'Test Accessibility' }),
      });
      renderWithProvider(props);

      expect(screen.getByLabelText('Issue: Test Accessibility')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('renders unknown for empty ID', () => {
      const issue = createTestIssue({ id: '' });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      expect(screen.getByText('unknown')).toBeInTheDocument();
    });

    it('renders with minimal issue props', () => {
      const issue: Issue = {
        id: 'min-id',
        title: 'Minimal',
        priority: 2,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };
      const props = createTestProps({
        data: createTestNodeData({ issue, title: 'Minimal' }),
      });
      renderWithProvider(props);

      expect(screen.getByRole('heading', { name: 'Minimal' })).toBeInTheDocument();
      expect(screen.getByText('min-id')).toBeInTheDocument();
    });
  });
});
