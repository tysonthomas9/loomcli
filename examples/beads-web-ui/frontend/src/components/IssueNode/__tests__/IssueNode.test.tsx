/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueNode component.
 */

import '@testing-library/jest-dom';
import { render, screen } from '@testing-library/react';
import { ReactFlowProvider } from '@xyflow/react';
import { describe, it, expect } from 'vitest';

import type { Issue, IssueNodeData } from '@/types';

import { IssueNode } from '../IssueNode';
import type { IssueNodeProps } from '../IssueNode';

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
 * Create test node data for IssueNode component.
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
    ...overrides,
  };
}

/**
 * Create test props for IssueNode component.
 */
function createTestProps(overrides: Partial<IssueNodeProps> = {}): IssueNodeProps {
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
  } as IssueNodeProps;
}

/**
 * Wrapper component to provide ReactFlow context.
 */
function renderWithProvider(props: IssueNodeProps) {
  return render(
    <ReactFlowProvider>
      <IssueNode {...props} />
    </ReactFlowProvider>
  );
}

describe('IssueNode', () => {
  describe('rendering', () => {
    it('renders issue title', () => {
      const issue = createTestIssue({ title: 'My Issue Title' });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: 'My Issue Title' }),
      });
      renderWithProvider(props);

      expect(screen.getByRole('heading', { name: 'My Issue Title' })).toBeInTheDocument();
    });

    it('renders shortened issue ID', () => {
      const issue = createTestIssue({ id: 'beads-abc123def456' });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      // Should show last 7 characters for long IDs
      expect(screen.getByText('3def456')).toBeInTheDocument();
    });

    it('renders short ID as-is', () => {
      const issue = createTestIssue({ id: 'bd-xyz' });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      expect(screen.getByText('bd-xyz')).toBeInTheDocument();
    });

    it('renders priority badge with correct text', () => {
      const issue = createTestIssue({ priority: 1 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 1 }),
      });
      renderWithProvider(props);

      expect(screen.getByText('P1')).toBeInTheDocument();
    });

    it('renders issue type badge when present', () => {
      const issue = createTestIssue({ issue_type: 'epic' });
      const props = createTestProps({
        data: createTestNodeData({ issue, issueType: 'epic' }),
      });
      renderWithProvider(props);

      expect(screen.getByText('epic')).toBeInTheDocument();
    });

    it('hides issue type badge when undefined', () => {
      const issue = createTestIssue();
      const props = createTestProps({
        data: createTestNodeData({ issue, issueType: undefined }),
      });
      renderWithProvider(props);

      // Should not have any type badge
      expect(screen.queryByText('epic')).not.toBeInTheDocument();
      expect(screen.queryByText('task')).not.toBeInTheDocument();
    });

    it('shows dependency counts when non-zero', () => {
      const props = createTestProps({
        data: createTestNodeData({ dependencyCount: 3, dependentCount: 5 }),
      });
      renderWithProvider(props);

      expect(screen.getByText(/← 3/)).toBeInTheDocument();
      expect(screen.getByText(/5 →/)).toBeInTheDocument();
    });

    it('hides dependency counts when zero', () => {
      const props = createTestProps({
        data: createTestNodeData({ dependencyCount: 0, dependentCount: 0 }),
      });
      renderWithProvider(props);

      // The spans exist but should be empty
      expect(screen.queryByText(/← \d/)).not.toBeInTheDocument();
      expect(screen.queryByText(/\d →/)).not.toBeInTheDocument();
    });

    it('renders with article element', () => {
      const props = createTestProps();
      const { container } = renderWithProvider(props);

      expect(container.querySelector('article')).toBeInTheDocument();
    });
  });

  describe('priority display', () => {
    it.each([0, 1, 2, 3, 4] as const)('renders P%i correctly', (priority) => {
      const issue = createTestIssue({ priority });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority }),
      });
      const { container } = renderWithProvider(props);

      expect(screen.getByText(`P${priority}`)).toBeInTheDocument();
      expect(container.querySelector('[data-priority]')).toHaveAttribute(
        'data-priority',
        String(priority)
      );
    });

    /**
     * P2 priority badge contrast fix verification.
     * The CSS uses data-priority="2" to apply dark text color for WCAG AA contrast
     * on the yellow background. This test ensures the attribute is correctly set.
     */
    it('P2 priority badge has data-priority="2" for CSS contrast styling', () => {
      const issue = createTestIssue({ priority: 2 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 2 }),
      });
      renderWithProvider(props);

      const priorityBadge = screen.getByText('P2');
      expect(priorityBadge).toHaveAttribute('data-priority', '2');
    });

    it('defaults to P4 when priority is undefined', () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: undefined }),
      });
      renderWithProvider(props);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('defaults to P4 for out of range priority (negative)', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: -1 });
      const props = createTestProps({
        // @ts-expect-error Testing invalid priority
        data: createTestNodeData({ issue, priority: -1 }),
      });
      renderWithProvider(props);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('defaults to P4 for out of range priority (> 4)', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 5 });
      const props = createTestProps({
        // @ts-expect-error Testing invalid priority
        data: createTestNodeData({ issue, priority: 5 }),
      });
      renderWithProvider(props);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('data-priority attribute matches priority', () => {
      const issue = createTestIssue({ priority: 3 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 3 }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-priority', '3');
    });
  });

  describe('priority badge styling', () => {
    it.each([0, 1, 2, 3, 4] as const)(
      'applies priority%i class to priority badge for priority %i',
      (priority) => {
        const issue = createTestIssue({ priority });
        const props = createTestProps({
          data: createTestNodeData({ issue, priority }),
        });
        renderWithProvider(props);

        const priorityBadge = screen.getByText(`P${priority}`);
        // CSS Modules hashes class names, so we check for the pattern
        expect(priorityBadge.className).toMatch(new RegExp(`priority${priority}`));
      }
    );

    it('applies both priorityBadge base class and priority-specific class', () => {
      const issue = createTestIssue({ priority: 2 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 2 }),
      });
      renderWithProvider(props);

      const priorityBadge = screen.getByText('P2');
      // Should have both the base priorityBadge class and priority2 class
      expect(priorityBadge.className).toMatch(/priorityBadge/);
      expect(priorityBadge.className).toMatch(/priority2/);
    });

    it('priority badge has data-priority attribute for backwards compatibility', () => {
      const issue = createTestIssue({ priority: 1 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 1 }),
      });
      renderWithProvider(props);

      const priorityBadge = screen.getByText('P1');
      expect(priorityBadge).toHaveAttribute('data-priority', '1');
    });

    it.each([0, 1, 2, 3, 4] as const)(
      'priority badge has data-priority="%i" attribute',
      (priority) => {
        const issue = createTestIssue({ priority });
        const props = createTestProps({
          data: createTestNodeData({ issue, priority }),
        });
        renderWithProvider(props);

        const priorityBadge = screen.getByText(`P${priority}`);
        expect(priorityBadge).toHaveAttribute('data-priority', String(priority));
      }
    );

    it('applies priority4 class when priority is undefined (default)', () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: undefined }),
      });
      renderWithProvider(props);

      const priorityBadge = screen.getByText('P4');
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute('data-priority', '4');
    });

    it('applies priority4 class for out of range priority', () => {
      // @ts-expect-error Testing invalid priority
      const issue = createTestIssue({ priority: 99 });
      const props = createTestProps({
        // @ts-expect-error Testing invalid priority
        data: createTestNodeData({ issue, priority: 99 }),
      });
      renderWithProvider(props);

      const priorityBadge = screen.getByText('P4');
      expect(priorityBadge.className).toMatch(/priority4/);
      expect(priorityBadge).toHaveAttribute('data-priority', '4');
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

    it('priority badge has aria-label', () => {
      const issue = createTestIssue({ priority: 0 });
      const props = createTestProps({
        data: createTestNodeData({ issue, priority: 0 }),
      });
      renderWithProvider(props);

      expect(screen.getByLabelText('Priority 0')).toBeInTheDocument();
    });
  });

  describe('status display', () => {
    it.each(['open', 'in_progress', 'blocked', 'closed', 'deferred'] as const)(
      'sets data-status="%s" attribute',
      (status) => {
        const issue = createTestIssue({ status });
        const props = createTestProps({
          data: createTestNodeData({ issue, status }),
        });
        const { container } = renderWithProvider(props);

        const article = container.querySelector('article');
        expect(article).toHaveAttribute('data-status', status);
      }
    );

    it('sets data-status="unknown" when status is undefined', () => {
      const issue = createTestIssue();
      delete issue.status;
      const props = createTestProps({
        data: createTestNodeData({ issue, status: undefined }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-status', 'unknown');
    });
  });

  describe('edge cases', () => {
    it('renders Untitled for empty title', () => {
      const issue = createTestIssue({ title: '' });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: '' }),
      });
      renderWithProvider(props);

      expect(screen.getByRole('heading', { name: 'Untitled' })).toBeInTheDocument();
    });

    it('renders unknown for empty ID', () => {
      const issue = createTestIssue({ id: '' });
      const props = createTestProps({
        data: createTestNodeData({ issue }),
      });
      renderWithProvider(props);

      expect(screen.getByText('unknown')).toBeInTheDocument();
    });

    it('handles very long titles', () => {
      const longTitle = 'A'.repeat(200);
      const issue = createTestIssue({ title: longTitle });
      const props = createTestProps({
        data: createTestNodeData({ issue, title: longTitle }),
      });
      renderWithProvider(props);

      // Should still render, truncation is handled by CSS
      expect(screen.getByRole('heading', { name: longTitle })).toBeInTheDocument();
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
        data: createTestNodeData({ issue, title: 'Minimal', priority: 2 }),
      });
      renderWithProvider(props);

      expect(screen.getByRole('heading', { name: 'Minimal' })).toBeInTheDocument();
      expect(screen.getByText('min-id')).toBeInTheDocument();
      expect(screen.getByText('P2')).toBeInTheDocument();
    });
  });

  describe('isReady display', () => {
    it('sets data-is-ready="true" when isReady is true', () => {
      const props = createTestProps({
        data: createTestNodeData({ isReady: true }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-is-ready', 'true');
    });

    it('sets data-is-ready="false" when isReady is false', () => {
      const props = createTestProps({
        data: createTestNodeData({ isReady: false }),
      });
      const { container } = renderWithProvider(props);

      const article = container.querySelector('article');
      expect(article).toHaveAttribute('data-is-ready', 'false');
    });
  });
});
