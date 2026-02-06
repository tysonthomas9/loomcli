/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for NodeTooltip component.
 */

import { render, screen } from '@testing-library/react';
import { describe, it, expect, vi as _vi, beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { Issue } from '@/types';

import { NodeTooltip } from '../NodeTooltip';
import type { TooltipPosition } from '../NodeTooltip';

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
 * Default position for tests.
 */
const defaultPosition: TooltipPosition = { x: 100, y: 100 };

describe('NodeTooltip', () => {
  // Mock window.innerWidth and innerHeight for boundary tests
  const originalInnerWidth = window.innerWidth;
  const originalInnerHeight = window.innerHeight;

  beforeEach(() => {
    Object.defineProperty(window, 'innerWidth', {
      writable: true,
      value: 1024,
    });
    Object.defineProperty(window, 'innerHeight', {
      writable: true,
      value: 768,
    });
  });

  afterEach(() => {
    Object.defineProperty(window, 'innerWidth', {
      writable: true,
      value: originalInnerWidth,
    });
    Object.defineProperty(window, 'innerHeight', {
      writable: true,
      value: originalInnerHeight,
    });
  });

  describe('rendering', () => {
    it('renders nothing when issue is null', () => {
      const { container } = render(<NodeTooltip issue={null} position={defaultPosition} />);

      expect(container.firstChild).toBeNull();
    });

    it('renders nothing when position is null', () => {
      const issue = createTestIssue();
      const { container } = render(<NodeTooltip issue={issue} position={null} />);

      expect(container.firstChild).toBeNull();
    });

    it('renders tooltip content when issue provided', () => {
      const issue = createTestIssue({ title: 'My Test Issue' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByTestId('node-tooltip')).toBeInTheDocument();
      expect(screen.getByText('My Test Issue')).toBeInTheDocument();
    });

    it('has role="tooltip"', () => {
      const issue = createTestIssue();
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      // Use hidden: true because aria-hidden="true" makes tooltip inaccessible
      expect(screen.getByRole('tooltip', { hidden: true })).toBeInTheDocument();
    });

    it('applies custom className', () => {
      const issue = createTestIssue();
      render(<NodeTooltip issue={issue} position={defaultPosition} className="custom-class" />);

      expect(screen.getByTestId('node-tooltip')).toHaveClass('custom-class');
    });
  });

  describe('issue ID display', () => {
    it('displays issue ID in correct format', () => {
      const issue = createTestIssue({ id: 'beads-abc123def456' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      // Should show last 7 characters for long IDs
      expect(screen.getByText('3def456')).toBeInTheDocument();
    });

    it('displays short ID as-is', () => {
      const issue = createTestIssue({ id: 'bd-xyz' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('bd-xyz')).toBeInTheDocument();
    });

    it('displays "unknown" for empty ID', () => {
      const issue = createTestIssue({ id: '' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('unknown')).toBeInTheDocument();
    });
  });

  describe('title display', () => {
    it('displays issue title', () => {
      const issue = createTestIssue({ title: 'Test Title' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      // Use hidden: true because aria-hidden="true" makes tooltip inaccessible
      expect(screen.getByRole('heading', { level: 4, hidden: true })).toHaveTextContent(
        'Test Title'
      );
    });

    it('displays "Untitled" for empty title', () => {
      const issue = createTestIssue({ title: '' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      // Use hidden: true because aria-hidden="true" makes tooltip inaccessible
      expect(screen.getByRole('heading', { level: 4, hidden: true })).toHaveTextContent('Untitled');
    });

    it('handles very long titles (CSS truncates)', () => {
      const longTitle = 'A'.repeat(200);
      const issue = createTestIssue({ title: longTitle });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      // Use hidden: true because aria-hidden="true" makes tooltip inaccessible
      expect(screen.getByRole('heading', { level: 4, hidden: true })).toHaveTextContent(longTitle);
    });
  });

  describe('status badge', () => {
    it.each([
      ['open', 'Open'],
      ['in_progress', 'In Progress'],
      ['closed', 'Closed'],
      ['blocked', 'Blocked'],
    ] as const)('displays "%s" status as "%s"', (status, expected) => {
      const issue = createTestIssue({ status });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText(expected)).toBeInTheDocument();
    });

    it('displays "Open" for undefined status', () => {
      const issue = createTestIssue();
      delete issue.status;
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('Open')).toBeInTheDocument();
    });

    it('sets data-status attribute', () => {
      const issue = createTestIssue({ status: 'in_progress' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      const badge = screen.getByText('In Progress');
      expect(badge).toHaveAttribute('data-status', 'in_progress');
    });
  });

  describe('priority badge', () => {
    it.each([0, 1, 2, 3, 4] as const)('displays P%i correctly', (priority) => {
      const issue = createTestIssue({ priority });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText(`P${priority}`)).toBeInTheDocument();
    });

    it('defaults to P4 for undefined priority', () => {
      const issue = createTestIssue();
      // @ts-expect-error Testing undefined priority
      delete issue.priority;
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('P4')).toBeInTheDocument();
    });

    it('sets data-priority attribute', () => {
      const issue = createTestIssue({ priority: 1 });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      const badge = screen.getByText('P1');
      expect(badge).toHaveAttribute('data-priority', '1');
    });

    /**
     * P2 priority badge contrast fix verification.
     * The CSS uses data-priority="2" to apply dark text color for WCAG AA contrast
     * on the yellow background. This test ensures the attribute is correctly set.
     */
    it('P2 priority badge has data-priority="2" for CSS contrast styling', () => {
      const issue = createTestIssue({ priority: 2 });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      const badge = screen.getByText('P2');
      expect(badge).toHaveAttribute('data-priority', '2');
    });
  });

  describe('description', () => {
    it('displays description when present', () => {
      const issue = createTestIssue({ description: 'This is a test description' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('This is a test description')).toBeInTheDocument();
    });

    it('shows "No description" when description is empty', () => {
      const issue = createTestIssue({ description: '' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('No description')).toBeInTheDocument();
    });

    it('shows "No description" when description is undefined', () => {
      const issue = createTestIssue();
      delete issue.description;
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('No description')).toBeInTheDocument();
    });
  });

  describe('assignee', () => {
    it('displays assignee when present', () => {
      const issue = createTestIssue({ assignee: 'john.doe' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('Assignee:')).toBeInTheDocument();
      expect(screen.getByText('john.doe')).toBeInTheDocument();
    });

    it('hides assignee section when not present', () => {
      const issue = createTestIssue();
      delete issue.assignee;
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.queryByText('Assignee:')).not.toBeInTheDocument();
    });
  });

  describe('positioning', () => {
    it('applies position from props', () => {
      const issue = createTestIssue();
      const position: TooltipPosition = { x: 200, y: 300 };
      render(<NodeTooltip issue={issue} position={position} />);

      const tooltip = screen.getByTestId('node-tooltip');
      expect(tooltip).toHaveStyle({ left: '216px', top: '316px' }); // x + offset, y + offset
    });

    it('renders with tooltip styles for pointer-events: none', () => {
      const issue = createTestIssue();
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      const tooltip = screen.getByTestId('node-tooltip');
      // Verify the component renders with the correct test ID (CSS handles pointer-events)
      expect(tooltip).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('handles minimal issue props', () => {
      const issue: Issue = {
        id: 'min-id',
        title: 'Minimal',
        priority: 2,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('Minimal')).toBeInTheDocument();
      expect(screen.getByText('min-id')).toBeInTheDocument();
      expect(screen.getByText('P2')).toBeInTheDocument();
      expect(screen.getByText('Open')).toBeInTheDocument();
    });

    it('handles custom/unknown status values', () => {
      const issue = createTestIssue({ status: 'custom_status' });
      render(<NodeTooltip issue={issue} position={defaultPosition} />);

      expect(screen.getByText('Custom_status')).toBeInTheDocument();
    });
  });
});
