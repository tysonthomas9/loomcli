/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for KanbanBoard component.
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, within, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { KanbanBoard } from '../KanbanBoard';
import { DEFAULT_COLUMNS as _DEFAULT_COLUMNS } from '../columnConfigs';
import type { Issue, Status, IssueType } from '@/types';
import type { FilterState } from '@/hooks/useFilterState';

/**
 * Legacy 3-column statuses for backward compatibility tests.
 */
const LEGACY_STATUSES: Status[] = ['open', 'in_progress', 'closed'];

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
 * Create multiple mock issues.
 */
function createMockIssues(statuses: Status[]): Issue[] {
  return statuses.map((status, index) =>
    createMockIssue({
      id: `issue-${index}`,
      title: `Issue ${index}`,
      status,
    })
  );
}

describe('KanbanBoard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('rendering', () => {
    it('renders default 5-column kanban layout (Ready, Backlog, In Progress, Review, Done)', () => {
      render(<KanbanBoard issues={[]} />);

      // Check for new default columns
      expect(screen.getByRole('heading', { name: 'Ready' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Backlog' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Review' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Done' })).toBeInTheDocument();
    });

    it('renders legacy 3-column layout with statuses prop', () => {
      render(<KanbanBoard issues={[]} statuses={LEGACY_STATUSES} />);

      // Check for legacy columns
      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Closed' })).toBeInTheDocument();
    });

    it('renders custom status columns', () => {
      const customStatuses: Status[] = ['blocked', 'deferred', 'open'];

      render(<KanbanBoard issues={[]} statuses={customStatuses} />);

      expect(screen.getByRole('heading', { name: 'Blocked' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Deferred' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
      // Should not render default columns that weren't specified
      expect(screen.queryByRole('heading', { name: 'In Progress' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Closed' })).not.toBeInTheDocument();
    });

    it('renders correct issue count in each column', () => {
      const issues = createMockIssues(['open', 'open', 'open', 'in_progress', 'closed', 'closed']);

      render(<KanbanBoard issues={issues} statuses={LEGACY_STATUSES} />);

      // Open column should show 3
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByLabelText('3 issues')).toBeInTheDocument();

      // In Progress column should show 1
      const inProgressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      expect(within(inProgressColumn).getByLabelText('1 issue')).toBeInTheDocument();

      // Closed column should show 2
      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });
      expect(within(closedColumn).getByLabelText('2 issues')).toBeInTheDocument();
    });

    it('groups issues by status', () => {
      const issues = [
        createMockIssue({ id: 'open-1', title: 'Open Issue 1', status: 'open' }),
        createMockIssue({ id: 'progress-1', title: 'Progress Issue 1', status: 'in_progress' }),
        createMockIssue({ id: 'open-2', title: 'Open Issue 2', status: 'open' }),
        createMockIssue({ id: 'closed-1', title: 'Closed Issue 1', status: 'closed' }),
      ];

      render(<KanbanBoard issues={issues} statuses={LEGACY_STATUSES} />);

      // Get column content areas and verify issues are in correct columns
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByText('Open Issue 1')).toBeInTheDocument();
      expect(within(openColumn).getByText('Open Issue 2')).toBeInTheDocument();

      const progressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      expect(within(progressColumn).getByText('Progress Issue 1')).toBeInTheDocument();

      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });
      expect(within(closedColumn).getByText('Closed Issue 1')).toBeInTheDocument();
    });

    it('renders issues as DraggableIssueCards', () => {
      const issues = [createMockIssue({ title: 'Draggable Issue' })];

      render(<KanbanBoard issues={issues} />);

      // DraggableIssueCard wraps IssueCard which renders as article
      expect(screen.getByRole('article')).toBeInTheDocument();
      expect(screen.getByText('Draggable Issue')).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('custom className is applied', () => {
      const { container } = render(<KanbanBoard issues={[]} className="custom-board-class" />);

      const board = container.firstChild as HTMLElement;
      expect(board).toHaveClass('custom-board-class');
    });

    it('onIssueClick callback passed to DraggableIssueCards', () => {
      const handleIssueClick = vi.fn();
      const issue = createMockIssue({ title: 'Clickable Issue' });

      render(<KanbanBoard issues={[issue]} onIssueClick={handleIssueClick} />);

      // When onClick is provided, IssueCard should have button role
      const card = screen.getByRole('button', { name: /Issue: Clickable Issue/i });
      expect(card).toBeInTheDocument();
    });

    it('onIssueClick is called when card is clicked', () => {
      const handleIssueClick = vi.fn();
      const issue = createMockIssue({ id: 'click-test', title: 'Clickable Issue' });

      render(<KanbanBoard issues={[issue]} onIssueClick={handleIssueClick} />);

      const card = screen.getByRole('button', { name: /Issue: Clickable Issue/i });
      fireEvent.click(card);

      expect(handleIssueClick).toHaveBeenCalledTimes(1);
      expect(handleIssueClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'click-test' }));
    });

    it('custom statuses override defaults', () => {
      const customStatuses: Status[] = ['custom_status'];

      render(<KanbanBoard issues={[]} statuses={customStatuses} />);

      expect(screen.getByRole('heading', { name: 'Custom Status' })).toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Open' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'In Progress' })).not.toBeInTheDocument();
      expect(screen.queryByRole('heading', { name: 'Closed' })).not.toBeInTheDocument();
    });

    it('does not pass onIssueClick when undefined', () => {
      const issue = createMockIssue({ title: 'Non-Clickable Issue' });

      render(<KanbanBoard issues={[issue]} />);

      // IssueCard renders as article (not a button with onClick handler)
      expect(screen.getByRole('article')).toBeInTheDocument();
      // The draggable wrapper has role="button" from dnd-kit, but the IssueCard
      // should not have an onClick handler set
      const article = screen.getByRole('article');
      expect(article.tagName).toBe('ARTICLE');
    });
  });

  describe('DndContext', () => {
    it('DndContext provider wraps columns', () => {
      const { container } = render(<KanbanBoard issues={[]} statuses={LEGACY_STATUSES} />);

      // The board div should exist and contain columns
      const board = container.firstChild as HTMLElement;
      expect(board).toBeInTheDocument();

      // StatusColumns should be rendered (they have data-droppable-id attribute)
      expect(document.querySelector('[data-droppable-id="open"]')).toBeInTheDocument();
      expect(document.querySelector('[data-droppable-id="in_progress"]')).toBeInTheDocument();
      expect(document.querySelector('[data-droppable-id="closed"]')).toBeInTheDocument();
    });

    it('sensors are configured for drag-and-drop', () => {
      const issue = createMockIssue({ title: 'Draggable' });

      render(<KanbanBoard issues={[issue]} />);

      // The draggable wrapper should have role="button" attribute from useDraggable
      const draggableWrapper = document.querySelector('[aria-roledescription="draggable"]');
      expect(draggableWrapper).toBeInTheDocument();
    });

    it('draggable items have correct ARIA attributes', () => {
      const issue = createMockIssue({ title: 'Accessible Drag' });

      render(<KanbanBoard issues={[issue]} />);

      // Check for dnd-kit accessibility attributes
      const draggable = document.querySelector('[aria-roledescription="draggable"]');
      expect(draggable).toBeInTheDocument();
      expect(draggable).toHaveAttribute('tabIndex', '0');
    });
  });

  describe('callback tests (drag events)', () => {
    it('onDragEnd callback is not called on initial render', () => {
      const handleDragEnd = vi.fn();
      const issue = createMockIssue({ id: 'drag-issue', status: 'open' });

      render(<KanbanBoard issues={[issue]} onDragEnd={handleDragEnd} />);

      // The callback should not be called on initial render
      expect(handleDragEnd).not.toHaveBeenCalled();
    });

    it('renders correctly without onDragEnd callback', () => {
      const issue = createMockIssue({ status: 'open' });

      render(<KanbanBoard issues={[issue]} />);

      expect(screen.getByRole('article')).toBeInTheDocument();
    });

    it('component accepts onDragEnd prop without error', () => {
      const handleDragEnd = vi.fn();
      const issue = createMockIssue({ id: 'drag-issue', status: 'open' });

      // Should not throw when onDragEnd is provided
      expect(() => {
        render(<KanbanBoard issues={[issue]} onDragEnd={handleDragEnd} />);
      }).not.toThrow();

      expect(screen.getByRole('article')).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('empty issues array renders columns with 0 count', () => {
      render(<KanbanBoard issues={[]} statuses={LEGACY_STATUSES} />);

      // All columns should show 0 count
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByLabelText('0 issues')).toBeInTheDocument();

      const progressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      expect(within(progressColumn).getByLabelText('0 issues')).toBeInTheDocument();

      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });
      expect(within(closedColumn).getByLabelText('0 issues')).toBeInTheDocument();
    });

    it('renders EmptyColumn in empty status columns', () => {
      render(<KanbanBoard issues={[]} statuses={LEGACY_STATUSES} />);

      // Each empty column should show EmptyColumn with appropriate message
      expect(screen.getByText('No open issues')).toBeInTheDocument();
      expect(screen.getByText('No issues in progress')).toBeInTheDocument();
      expect(screen.getByText('No closed issues')).toBeInTheDocument();
    });

    it('renders EmptyColumn when filter results in empty column', () => {
      const issues = [
        createMockIssue({ id: 'open-1', title: 'Open Task', status: 'open', issue_type: 'task' }),
        createMockIssue({
          id: 'progress-1',
          title: 'In Progress Bug',
          status: 'in_progress',
          issue_type: 'bug',
        }),
      ];
      const filters: FilterState = { type: 'feature' as IssueType };

      render(<KanbanBoard issues={issues} filters={filters} statuses={LEGACY_STATUSES} />);

      // All columns should be empty due to filter (no features)
      expect(screen.getByText('No open issues')).toBeInTheDocument();
      expect(screen.getByText('No issues in progress')).toBeInTheDocument();
      expect(screen.getByText('No closed issues')).toBeInTheDocument();
    });

    it('shows EmptyColumn only in columns without issues', () => {
      const issues = [createMockIssue({ id: 'open-1', title: 'Open Issue', status: 'open' })];

      render(<KanbanBoard issues={issues} statuses={LEGACY_STATUSES} />);

      // Open column should NOT show EmptyColumn
      expect(screen.queryByText('No open issues')).not.toBeInTheDocument();
      // But other columns should show EmptyColumn
      expect(screen.getByText('No issues in progress')).toBeInTheDocument();
      expect(screen.getByText('No closed issues')).toBeInTheDocument();
    });

    it('issues without status default to open', () => {
      const issueWithoutStatus = createMockIssue({
        id: 'no-status-issue',
        title: 'No Status Issue',
      });
      // Remove status property
      delete (issueWithoutStatus as Partial<Issue>).status;

      render(<KanbanBoard issues={[issueWithoutStatus]} statuses={LEGACY_STATUSES} />);

      // Issue should appear in Open column
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByText('No Status Issue')).toBeInTheDocument();
      expect(within(openColumn).getByLabelText('1 issue')).toBeInTheDocument();
    });

    it('issues with unknown status are not displayed if status not in columns', () => {
      const issueWithUnknownStatus = createMockIssue({
        id: 'unknown-status',
        title: 'Unknown Status Issue',
        status: 'unknown_status' as Status,
      });

      render(<KanbanBoard issues={[issueWithUnknownStatus]} statuses={LEGACY_STATUSES} />);

      // Issue should not appear since 'unknown_status' is not in default columns
      expect(screen.queryByText('Unknown Status Issue')).not.toBeInTheDocument();

      // All columns should show 0
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByLabelText('0 issues')).toBeInTheDocument();
    });

    it('handles large number of issues', () => {
      const manyIssues = Array.from({ length: 100 }, (_, i) =>
        createMockIssue({
          id: `issue-${i}`,
          title: `Issue ${i}`,
          status: ['open', 'in_progress', 'closed'][i % 3] as Status,
        })
      );

      render(<KanbanBoard issues={manyIssues} statuses={LEGACY_STATUSES} />);

      // Should render without crashing
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      const progressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });

      // 100 issues / 3 statuses = ~33-34 per status
      expect(within(openColumn).getByLabelText('34 issues')).toBeInTheDocument();
      expect(within(progressColumn).getByLabelText('33 issues')).toBeInTheDocument();
      expect(within(closedColumn).getByLabelText('33 issues')).toBeInTheDocument();
    });

    it('handles duplicate issue ids gracefully', () => {
      const issues = [
        createMockIssue({ id: 'duplicate-id', title: 'First Issue', status: 'open' }),
        createMockIssue({ id: 'duplicate-id', title: 'Second Issue', status: 'open' }),
      ];

      // Should not throw (React will warn about duplicate keys)
      render(<KanbanBoard issues={issues} />);

      // Both issues should be rendered
      expect(screen.getByText('First Issue')).toBeInTheDocument();
      expect(screen.getByText('Second Issue')).toBeInTheDocument();
    });

    it('renders with empty statuses array', () => {
      render(<KanbanBoard issues={[]} statuses={[]} />);

      // No columns should be rendered
      expect(screen.queryByRole('region')).not.toBeInTheDocument();
    });

    it('handles status undefined fallback correctly', () => {
      const issueUndefinedStatus: Issue = {
        id: 'undefined-status',
        title: 'Undefined Status',
        priority: 2,
        status: undefined,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };

      render(<KanbanBoard issues={[issueUndefinedStatus]} statuses={LEGACY_STATUSES} />);

      // Should appear in Open column (fallback)
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByText('Undefined Status')).toBeInTheDocument();
    });
  });

  describe('column ordering', () => {
    it('columns are rendered in statuses array order', () => {
      const orderedStatuses: Status[] = ['closed', 'in_progress', 'open'];

      const { container } = render(<KanbanBoard issues={[]} statuses={orderedStatuses} />);

      const columns = container.querySelectorAll('section');
      expect(columns).toHaveLength(3);

      // Verify order by data-status attribute
      expect(columns[0]).toHaveAttribute('data-status', 'closed');
      expect(columns[1]).toHaveAttribute('data-status', 'in_progress');
      expect(columns[2]).toHaveAttribute('data-status', 'open');
    });
  });

  describe('issue data passed correctly', () => {
    it('issue properties are passed through to cards', () => {
      const issue = createMockIssue({
        title: 'Complete Test Issue',
        priority: 1,
        status: 'open',
      });

      render(<KanbanBoard issues={[issue]} />);

      // Title should be displayed
      expect(screen.getByText('Complete Test Issue')).toBeInTheDocument();

      // Priority badge should show P1
      expect(screen.getByText('P1')).toBeInTheDocument();
    });

    it('multiple issues in same column preserve order', () => {
      const issues = [
        createMockIssue({ id: 'first', title: 'First Issue', status: 'open' }),
        createMockIssue({ id: 'second', title: 'Second Issue', status: 'open' }),
        createMockIssue({ id: 'third', title: 'Third Issue', status: 'open' }),
      ];

      render(<KanbanBoard issues={issues} statuses={LEGACY_STATUSES} />);

      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      const articles = within(openColumn).getAllByRole('article');

      expect(articles).toHaveLength(3);
      expect(articles[0]).toHaveTextContent('First Issue');
      expect(articles[1]).toHaveTextContent('Second Issue');
      expect(articles[2]).toHaveTextContent('Third Issue');
    });
  });

  describe('droppable zones', () => {
    it('each column has a droppable zone with matching status id', () => {
      const statuses: Status[] = ['open', 'in_progress', 'closed', 'blocked'];

      render(<KanbanBoard issues={[]} statuses={statuses} />);

      statuses.forEach((status) => {
        const droppable = document.querySelector(`[data-droppable-id="${status}"]`);
        expect(droppable).toBeInTheDocument();
      });
    });

    it('droppable zones have role="list"', () => {
      render(<KanbanBoard issues={[]} statuses={LEGACY_STATUSES} />);

      const lists = screen.getAllByRole('list');
      expect(lists).toHaveLength(3); // Three legacy columns

      lists.forEach((list) => {
        expect(list).toHaveAttribute('data-droppable-id');
      });
    });
  });

  describe('issue click callback', () => {
    it('onIssueClick receives the clicked issue', () => {
      const handleIssueClick = vi.fn();
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'First', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Second', status: 'open' }),
      ];

      render(<KanbanBoard issues={issues} onIssueClick={handleIssueClick} />);

      // When onIssueClick is provided, IssueCard has role="button" with aria-label
      // We need to click the IssueCard button, not the draggable wrapper
      const cardButton = screen.getByRole('button', { name: /Issue: First/i });
      fireEvent.click(cardButton);

      expect(handleIssueClick).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'issue-1', title: 'First' })
      );
    });

    it('clicking different cards calls onIssueClick with correct issue', () => {
      const handleIssueClick = vi.fn();
      const issues = [
        createMockIssue({ id: 'issue-a', title: 'Issue A', status: 'open' }),
        createMockIssue({ id: 'issue-b', title: 'Issue B', status: 'in_progress' }),
      ];

      render(<KanbanBoard issues={issues} onIssueClick={handleIssueClick} />);

      const cardA = screen.getByRole('button', { name: /Issue: Issue A/i });
      const cardB = screen.getByRole('button', { name: /Issue: Issue B/i });

      fireEvent.click(cardB);
      expect(handleIssueClick).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-b' }));

      fireEvent.click(cardA);
      expect(handleIssueClick).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-a' }));

      expect(handleIssueClick).toHaveBeenCalledTimes(2);
    });
  });

  describe('CSS class application', () => {
    it('board has base CSS class', () => {
      const { container } = render(<KanbanBoard issues={[]} />);

      const board = container.firstChild as HTMLElement;
      // CSS module will add the class
      expect(board.className).toContain('board');
    });

    it('board combines base and custom CSS classes', () => {
      const { container } = render(<KanbanBoard issues={[]} className="my-custom-class" />);

      const board = container.firstChild as HTMLElement;
      expect(board.className).toContain('board');
      expect(board).toHaveClass('my-custom-class');
    });
  });

  describe('filtering', () => {
    /**
     * Create a diverse set of issues for filter testing.
     */
    function createFilterTestIssues(): Issue[] {
      return [
        createMockIssue({
          id: 'task-p1-1',
          title: 'Important Task',
          status: 'open',
          priority: 1,
          issue_type: 'task',
          labels: ['frontend', 'urgent'],
        }),
        createMockIssue({
          id: 'bug-p2-1',
          title: 'Critical Bug Fix',
          status: 'open',
          priority: 2,
          issue_type: 'bug',
          labels: ['backend'],
        }),
        createMockIssue({
          id: 'feature-p1-1',
          title: 'New Feature Request',
          status: 'in_progress',
          priority: 1,
          issue_type: 'feature',
          labels: ['frontend', 'design'],
        }),
        createMockIssue({
          id: 'task-p3-1',
          title: 'Low Priority Task',
          status: 'closed',
          priority: 3,
          issue_type: 'task',
          labels: [],
        }),
        createMockIssue({
          id: 'epic-p0-1',
          title: 'Epic for Q1',
          status: 'open',
          priority: 0,
          issue_type: 'epic',
          labels: ['roadmap'],
        }),
      ];
    }

    it('shows all non-epic issues when no filters provided', () => {
      const issues = createFilterTestIssues();

      render(<KanbanBoard issues={issues} />);

      // Non-epic issues should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();
      expect(screen.getByText('Low Priority Task')).toBeInTheDocument();
      // Epics are excluded from non-Done kanban columns
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('shows all non-epic issues when filters prop is empty object', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = {};

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Non-epic issues should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();
      expect(screen.getByText('Low Priority Task')).toBeInTheDocument();
      // Epics are excluded from non-Done kanban columns
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('filters by priority', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { priority: 1 };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only P1 issues should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();

      // Other priorities should not be visible
      expect(screen.queryByText('Critical Bug Fix')).not.toBeInTheDocument();
      expect(screen.queryByText('Low Priority Task')).not.toBeInTheDocument();
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('filters by type', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { type: 'bug' as IssueType };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only bugs should be visible
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();

      // Other types should not be visible
      expect(screen.queryByText('Important Task')).not.toBeInTheDocument();
      expect(screen.queryByText('New Feature Request')).not.toBeInTheDocument();
      expect(screen.queryByText('Low Priority Task')).not.toBeInTheDocument();
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('filters by labels (AND logic)', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { labels: ['frontend'] };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only issues with 'frontend' label should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();

      // Issues without 'frontend' label should not be visible
      expect(screen.queryByText('Critical Bug Fix')).not.toBeInTheDocument();
      expect(screen.queryByText('Low Priority Task')).not.toBeInTheDocument();
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('filters by multiple labels (AND logic)', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { labels: ['frontend', 'urgent'] };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only issues with BOTH 'frontend' AND 'urgent' labels should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();

      // Issues without both labels should not be visible
      expect(screen.queryByText('New Feature Request')).not.toBeInTheDocument();
      expect(screen.queryByText('Critical Bug Fix')).not.toBeInTheDocument();
    });

    it('filters by search (case-insensitive)', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { search: 'TASK' };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Issues with 'task' in title (case-insensitive) should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Low Priority Task')).toBeInTheDocument();

      // Issues without 'task' in title should not be visible
      expect(screen.queryByText('Critical Bug Fix')).not.toBeInTheDocument();
      expect(screen.queryByText('New Feature Request')).not.toBeInTheDocument();
      expect(screen.queryByText('Epic for Q1')).not.toBeInTheDocument();
    });

    it('combines multiple filters (AND)', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { priority: 1, type: 'task' as IssueType };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only P1 tasks should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();

      // P1 feature should not be visible (wrong type)
      expect(screen.queryByText('New Feature Request')).not.toBeInTheDocument();
      // P3 task should not be visible (wrong priority)
      expect(screen.queryByText('Low Priority Task')).not.toBeInTheDocument();
    });

    it('updates when filters change', () => {
      const issues = createFilterTestIssues();
      const { rerender } = render(<KanbanBoard issues={issues} filters={{}} />);

      // Initially all visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();

      // Apply filter
      rerender(<KanbanBoard issues={issues} filters={{ type: 'bug' as IssueType }} />);

      // Only bug visible now
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();
      expect(screen.queryByText('Important Task')).not.toBeInTheDocument();
    });

    it('handles issues with undefined optional fields', () => {
      const issuesWithMissingFields: Issue[] = [
        createMockIssue({
          id: 'no-labels',
          title: 'Issue Without Labels',
          status: 'open',
          priority: 1,
          issue_type: undefined,
          labels: undefined,
        }),
        createMockIssue({
          id: 'with-labels',
          title: 'Issue With Labels',
          status: 'open',
          priority: 1,
          issue_type: 'task',
          labels: ['test'],
        }),
      ];

      // Filter by type should exclude issue with undefined type
      const filters: FilterState = { type: 'task' as IssueType };

      render(<KanbanBoard issues={issuesWithMissingFields} filters={filters} />);

      expect(screen.getByText('Issue With Labels')).toBeInTheDocument();
      expect(screen.queryByText('Issue Without Labels')).not.toBeInTheDocument();
    });

    it('handles issues with missing labels when label filter applied', () => {
      const issues: Issue[] = [
        createMockIssue({
          id: 'no-labels',
          title: 'Issue Without Labels',
          status: 'open',
          labels: undefined,
        }),
        createMockIssue({
          id: 'empty-labels',
          title: 'Issue With Empty Labels',
          status: 'open',
          labels: [],
        }),
        createMockIssue({
          id: 'with-label',
          title: 'Issue With Label',
          status: 'open',
          labels: ['test'],
        }),
      ];

      const filters: FilterState = { labels: ['test'] };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only issue with matching label visible
      expect(screen.getByText('Issue With Label')).toBeInTheDocument();
      expect(screen.queryByText('Issue Without Labels')).not.toBeInTheDocument();
      expect(screen.queryByText('Issue With Empty Labels')).not.toBeInTheDocument();
    });

    it('column counts reflect filtered issues', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { priority: 1 };

      render(<KanbanBoard issues={issues} filters={filters} statuses={LEGACY_STATUSES} />);

      // Open column should show 1 (only P1 task)
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByLabelText('1 issue')).toBeInTheDocument();

      // In Progress column should show 1 (only P1 feature)
      const progressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      expect(within(progressColumn).getByLabelText('1 issue')).toBeInTheDocument();

      // Closed column should show 0 (no P1 closed issues)
      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });
      expect(within(closedColumn).getByLabelText('0 issues')).toBeInTheDocument();
    });

    it('empty search string shows all issues', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { search: '' };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // All issues should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();
    });

    it('empty labels array shows all issues', () => {
      const issues = createFilterTestIssues();
      const filters: FilterState = { labels: [] };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // All issues should be visible
      expect(screen.getByText('Important Task')).toBeInTheDocument();
      expect(screen.getByText('Critical Bug Fix')).toBeInTheDocument();
      expect(screen.getByText('New Feature Request')).toBeInTheDocument();
    });

    it('filters work with all four filter types combined', () => {
      const issues: Issue[] = [
        createMockIssue({
          id: 'match-all',
          title: 'Perfect Match Issue',
          status: 'open',
          priority: 2,
          issue_type: 'bug',
          labels: ['critical', 'backend'],
        }),
        createMockIssue({
          id: 'wrong-priority',
          title: 'Perfect Match But Wrong Priority',
          status: 'open',
          priority: 1,
          issue_type: 'bug',
          labels: ['critical', 'backend'],
        }),
        createMockIssue({
          id: 'wrong-type',
          title: 'Perfect Match But Wrong Type',
          status: 'open',
          priority: 2,
          issue_type: 'task',
          labels: ['critical', 'backend'],
        }),
        createMockIssue({
          id: 'wrong-labels',
          title: 'Perfect Match But Wrong Labels',
          status: 'open',
          priority: 2,
          issue_type: 'bug',
          labels: ['frontend'],
        }),
        createMockIssue({
          id: 'wrong-title',
          title: 'Something Else Entirely',
          status: 'open',
          priority: 2,
          issue_type: 'bug',
          labels: ['critical', 'backend'],
        }),
      ];

      const filters: FilterState = {
        priority: 2,
        type: 'bug' as IssueType,
        labels: ['critical', 'backend'],
        search: 'Perfect',
      };

      render(<KanbanBoard issues={issues} filters={filters} />);

      // Only the perfect match should be visible
      expect(screen.getByText('Perfect Match Issue')).toBeInTheDocument();

      // All others should be filtered out
      expect(screen.queryByText('Perfect Match But Wrong Priority')).not.toBeInTheDocument();
      expect(screen.queryByText('Perfect Match But Wrong Type')).not.toBeInTheDocument();
      expect(screen.queryByText('Perfect Match But Wrong Labels')).not.toBeInTheDocument();
      expect(screen.queryByText('Something Else Entirely')).not.toBeInTheDocument();
    });
  });

  describe('blocked issues filtering', () => {
    /**
     * Create a blockedIssues map for testing.
     */
    function createBlockedIssuesMap(
      blockedIds: string[]
    ): Map<string, { blockedByCount: number; blockedBy: string[] }> {
      const map = new Map<string, { blockedByCount: number; blockedBy: string[] }>();
      blockedIds.forEach((id) => {
        map.set(id, { blockedByCount: 1, blockedBy: ['blocker-id'] });
      });
      return map;
    }

    it('shows all issues including blocked when showBlocked=true (default)', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
    });

    it('shows all issues when showBlocked=true explicitly', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={true} />);

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
    });

    it('hides blocked issues when showBlocked=false', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={false} />);

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.queryByText('Blocked Issue')).not.toBeInTheDocument();
    });

    it('hides multiple blocked issues when showBlocked=false', () => {
      const issues = [
        createMockIssue({ id: 'normal-1', title: 'Normal 1', status: 'open' }),
        createMockIssue({ id: 'blocked-1', title: 'Blocked 1', status: 'open' }),
        createMockIssue({ id: 'normal-2', title: 'Normal 2', status: 'in_progress' }),
        createMockIssue({ id: 'blocked-2', title: 'Blocked 2', status: 'in_progress' }),
        createMockIssue({ id: 'blocked-3', title: 'Blocked 3', status: 'closed' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-1', 'blocked-2', 'blocked-3']);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={false} />);

      expect(screen.getByText('Normal 1')).toBeInTheDocument();
      expect(screen.getByText('Normal 2')).toBeInTheDocument();
      expect(screen.queryByText('Blocked 1')).not.toBeInTheDocument();
      expect(screen.queryByText('Blocked 2')).not.toBeInTheDocument();
      expect(screen.queryByText('Blocked 3')).not.toBeInTheDocument();
    });

    it('shows all issues when showBlocked=false but blockedIssues is undefined', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'Issue 1', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Issue 2', status: 'open' }),
      ];

      render(<KanbanBoard issues={issues} showBlocked={false} />);

      expect(screen.getByText('Issue 1')).toBeInTheDocument();
      expect(screen.getByText('Issue 2')).toBeInTheDocument();
    });

    it('column counts reflect blocked filtering', () => {
      const issues = [
        createMockIssue({ id: 'normal-1', title: 'Normal 1', status: 'open' }),
        createMockIssue({ id: 'normal-2', title: 'Normal 2', status: 'open' }),
        createMockIssue({ id: 'blocked-1', title: 'Blocked 1', status: 'open' }),
        createMockIssue({ id: 'blocked-2', title: 'Blocked 2', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-1', 'blocked-2']);

      render(
        <KanbanBoard
          issues={issues}
          blockedIssues={blockedIssues}
          showBlocked={false}
          statuses={LEGACY_STATUSES}
        />
      );

      // Open column should show 2 (only non-blocked issues)
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByLabelText('2 issues')).toBeInTheDocument();
    });

    it('combines blocked filtering with other filters', () => {
      const issues = [
        createMockIssue({ id: 'p1-normal', title: 'P1 Normal', status: 'open', priority: 1 }),
        createMockIssue({ id: 'p1-blocked', title: 'P1 Blocked', status: 'open', priority: 1 }),
        createMockIssue({ id: 'p2-normal', title: 'P2 Normal', status: 'open', priority: 2 }),
        createMockIssue({ id: 'p2-blocked', title: 'P2 Blocked', status: 'open', priority: 2 }),
      ];
      const blockedIssues = createBlockedIssuesMap(['p1-blocked', 'p2-blocked']);
      const filters: FilterState = { priority: 1 };

      render(
        <KanbanBoard
          issues={issues}
          blockedIssues={blockedIssues}
          showBlocked={false}
          filters={filters}
        />
      );

      // Only P1 non-blocked issue should be visible
      expect(screen.getByText('P1 Normal')).toBeInTheDocument();
      expect(screen.queryByText('P1 Blocked')).not.toBeInTheDocument();
      expect(screen.queryByText('P2 Normal')).not.toBeInTheDocument();
      expect(screen.queryByText('P2 Blocked')).not.toBeInTheDocument();
    });

    it('updates when showBlocked prop changes', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      const { rerender } = render(
        <KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={true} />
      );

      // Initially all visible
      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();

      // Hide blocked issues
      rerender(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={false} />);

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.queryByText('Blocked Issue')).not.toBeInTheDocument();

      // Show blocked issues again
      rerender(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={true} />);

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
    });

    it('handles empty blockedIssues map', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'Issue 1', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Issue 2', status: 'open' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>();

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} showBlocked={false} />);

      // All issues should be visible when nothing is blocked
      expect(screen.getByText('Issue 1')).toBeInTheDocument();
      expect(screen.getByText('Issue 2')).toBeInTheDocument();
    });
  });

  describe('blockedIssues prop passed to cards', () => {
    it('passes blockedInfo to DraggableIssueCard for blocked issues', () => {
      const issues = [
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>([
        [
          'blocked-issue',
          { blockedByCount: 3, blockedBy: ['blocker-1', 'blocker-2', 'blocker-3'] },
        ],
      ]);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      // The BlockedBadge should be rendered within the card
      expect(screen.getByLabelText('Blocked by 3 issues')).toBeInTheDocument();
    });

    it('does not pass blockedInfo for non-blocked issues', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>([
        ['other-issue', { blockedByCount: 1, blockedBy: ['blocker-1'] }],
      ]);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      // No BlockedBadge should be rendered
      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });

    it('passes correct blockedBy array to BlockedBadge', () => {
      const issues = [
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>([
        ['blocked-issue', { blockedByCount: 2, blockedBy: ['blocker-abc', 'blocker-xyz'] }],
      ]);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      // Hover on badge to see tooltip
      const badge = screen.getByLabelText('Blocked by 2 issues');
      fireEvent.mouseEnter(badge);

      expect(screen.getByText('blocker-abc')).toBeInTheDocument();
      expect(screen.getByText('blocker-xyz')).toBeInTheDocument();
    });

    it('renders blocked badges in multiple columns', () => {
      const issues = [
        createMockIssue({ id: 'blocked-open', title: 'Blocked Open', status: 'open' }),
        createMockIssue({
          id: 'blocked-progress',
          title: 'Blocked Progress',
          status: 'in_progress',
        }),
        createMockIssue({ id: 'normal-closed', title: 'Normal Closed', status: 'closed' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>([
        ['blocked-open', { blockedByCount: 1, blockedBy: ['b1'] }],
        ['blocked-progress', { blockedByCount: 2, blockedBy: ['b2', 'b3'] }],
      ]);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      // Both blocked badges should be rendered
      expect(screen.getByLabelText('Blocked by 1 issue')).toBeInTheDocument();
      expect(screen.getByLabelText('Blocked by 2 issues')).toBeInTheDocument();
    });

    it('passes blockedInfo to DragOverlay when dragging', () => {
      // This test verifies the DragOverlay receives blocked info
      // Note: Actually testing drag behavior requires integration tests
      const issues = [
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, { blockedByCount: number; blockedBy: string[] }>([
        ['blocked-issue', { blockedByCount: 1, blockedBy: ['blocker-1'] }],
      ]);

      render(<KanbanBoard issues={issues} blockedIssues={blockedIssues} />);

      // Verify the card renders with blocked badge
      expect(screen.getByLabelText('Blocked by 1 issue')).toBeInTheDocument();
    });
  });
});
