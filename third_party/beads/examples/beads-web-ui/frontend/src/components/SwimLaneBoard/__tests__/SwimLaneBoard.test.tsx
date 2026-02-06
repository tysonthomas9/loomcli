/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SwimLaneBoard component.
 */

import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import '@testing-library/jest-dom';

import type { BlockedInfo } from '@/components/KanbanBoard';
import type { Issue, Status } from '@/types';

import { SwimLaneBoard } from '../SwimLaneBoard';

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
 * Create a blockedIssues map for testing.
 */
function createBlockedIssuesMap(blockedIds: string[]): Map<string, BlockedInfo> {
  const map = new Map<string, BlockedInfo>();
  blockedIds.forEach((id) => {
    map.set(id, { blockedByCount: 1, blockedBy: ['blocker-id'] });
  });
  return map;
}

/**
 * Default statuses for testing.
 */
const defaultStatuses: Status[] = ['open', 'in_progress', 'closed'];

describe('SwimLaneBoard', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Clear localStorage to prevent state persistence between tests
    localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    localStorage.clear();
  });

  describe('groupBy=none fallback to KanbanBoard', () => {
    it('renders KanbanBoard when groupBy=none', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'Issue 1', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Issue 2', status: 'in_progress' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="none" statuses={defaultStatuses} />);

      // KanbanBoard renders with data-testid="kanban-board" (from its className)
      // Check for KanbanBoard-specific rendering - status columns without swim lane structure
      expect(screen.getByRole('heading', { name: 'Open' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'In Progress' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Closed' })).toBeInTheDocument();

      // Should NOT have swim-lane-board test id
      expect(screen.queryByTestId('swim-lane-board')).not.toBeInTheDocument();
    });

    it('passes onIssueClick to KanbanBoard when groupBy=none', () => {
      const handleIssueClick = vi.fn();
      const issue = createMockIssue({ id: 'click-test', title: 'Clickable Issue', status: 'open' });

      render(
        <SwimLaneBoard
          issues={[issue]}
          groupBy="none"
          statuses={defaultStatuses}
          onIssueClick={handleIssueClick}
        />
      );

      const card = screen.getByRole('button', { name: /Issue: Clickable Issue/i });
      fireEvent.click(card);

      expect(handleIssueClick).toHaveBeenCalledTimes(1);
      expect(handleIssueClick).toHaveBeenCalledWith(expect.objectContaining({ id: 'click-test' }));
    });
  });

  describe('rendering swim lanes', () => {
    it('renders SwimLane for each group', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
        createMockIssue({ id: 'issue-3', assignee: 'alice', status: 'in_progress' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Should have swim-lane-board container
      expect(screen.getByTestId('swim-lane-board')).toBeInTheDocument();

      // Should render lanes for alice and bob
      expect(screen.getByRole('heading', { name: 'alice' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'bob' })).toBeInTheDocument();
    });

    it('shows correct lane titles for assignee grouping', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice' }),
        createMockIssue({ id: 'issue-2', assignee: undefined }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      expect(screen.getByRole('heading', { name: 'alice' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Unassigned' })).toBeInTheDocument();
    });

    it('shows correct lane titles for priority grouping', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', priority: 0 }),
        createMockIssue({ id: 'issue-2', priority: 1 }),
        createMockIssue({ id: 'issue-3', priority: 2 }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="priority" statuses={defaultStatuses} />);

      expect(screen.getByRole('heading', { name: 'P0 (Critical)' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'P1 (High)' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'P2 (Medium)' })).toBeInTheDocument();
    });

    it('shows correct lane titles for type grouping', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', issue_type: 'bug' }),
        createMockIssue({ id: 'issue-2', issue_type: 'feature' }),
        createMockIssue({ id: 'issue-3', issue_type: undefined }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="type" statuses={defaultStatuses} />);

      expect(screen.getByRole('heading', { name: 'Bug' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Feature' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'No Type' })).toBeInTheDocument();
    });

    it('shows correct lane titles for epic grouping', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', parent: 'epic-1', parent_title: 'Epic One' }),
        createMockIssue({ id: 'issue-2', parent: undefined }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="epic" statuses={defaultStatuses} />);

      expect(screen.getByRole('heading', { name: 'Epic One' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'Ungrouped' })).toBeInTheDocument();
    });

    it('shows correct lane titles for label grouping', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', labels: ['frontend'] }),
        createMockIssue({ id: 'issue-2', labels: ['backend'] }),
        createMockIssue({ id: 'issue-3', labels: [] }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="label" statuses={defaultStatuses} />);

      expect(screen.getByRole('heading', { name: 'frontend' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'backend' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'No Labels' })).toBeInTheDocument();
    });
  });

  describe('ungrouped lane positioning', () => {
    it('places Unassigned lane last when sorting by title', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'bob' }),
        createMockIssue({ id: 'issue-2', assignee: undefined }),
        createMockIssue({ id: 'issue-3', assignee: 'alice' }),
      ];

      const { container } = render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          sortLanesBy="title"
        />
      );

      // Select only lane title h3 elements (those inside header elements)
      const laneHeadings = container.querySelectorAll('header h3');
      const titles = Array.from(laneHeadings).map((h) => h.textContent);

      // Alice should be first (alphabetically), then Bob, then Unassigned
      expect(titles[0]).toBe('alice');
      expect(titles[1]).toBe('bob');
      expect(titles[2]).toBe('Unassigned');
    });

    it('places No Priority lane last when sorting by count', () => {
      const issueWithoutPriority: Issue = {
        id: 'no-priority',
        title: 'No Priority Issue',
        priority: undefined as unknown as number,
        status: 'open',
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };

      const issues = [
        createMockIssue({ id: 'issue-1', priority: 1 }),
        createMockIssue({ id: 'issue-2', priority: 1 }),
        createMockIssue({ id: 'issue-3', priority: 2 }),
        issueWithoutPriority,
        // Add more no-priority issues to give it higher count
        { ...issueWithoutPriority, id: 'no-priority-2' },
        { ...issueWithoutPriority, id: 'no-priority-3' },
      ];

      const { container } = render(
        <SwimLaneBoard
          issues={issues}
          groupBy="priority"
          statuses={defaultStatuses}
          sortLanesBy="count"
        />
      );

      // Select only lane title h3 elements (those inside header elements)
      const laneHeadings = container.querySelectorAll('header h3');
      const titles = Array.from(laneHeadings).map((h) => h.textContent);

      // No Priority should be last even though it has 3 issues (highest count)
      expect(titles[titles.length - 1]).toBe('No Priority');
    });
  });

  describe('collapse toggle', () => {
    it('toggles lane collapse state when toggle clicked', () => {
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' })];

      const { container: _container } = render(
        <SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />
      );

      // Get the toggle button
      const toggleButton = screen.getByTestId('collapse-toggle');

      // Initially expanded
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');

      // Click to collapse
      fireEvent.click(toggleButton);
      expect(toggleButton).toHaveAttribute('aria-expanded', 'false');

      // Click to expand again
      fireEvent.click(toggleButton);
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
    });

    it('multiple lanes can be collapsed independently', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob', status: 'open' }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      const toggleButtons = screen.getAllByTestId('collapse-toggle');
      expect(toggleButtons).toHaveLength(2);

      // Collapse first lane only
      fireEvent.click(toggleButtons[0]);

      expect(toggleButtons[0]).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButtons[1]).toHaveAttribute('aria-expanded', 'true');
    });

    it('respects defaultCollapsed prop', () => {
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' })];

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          defaultCollapsed={true}
        />
      );

      // With defaultCollapsed=true, lanes start collapsed
      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'false');
    });
  });

  describe('onIssueClick propagation', () => {
    it('calls onIssueClick when issue card is clicked', () => {
      const handleIssueClick = vi.fn();
      const issue = createMockIssue({
        id: 'click-test',
        title: 'Clickable Issue',
        assignee: 'alice',
      });

      render(
        <SwimLaneBoard
          issues={[issue]}
          groupBy="assignee"
          statuses={defaultStatuses}
          onIssueClick={handleIssueClick}
        />
      );

      const card = screen.getByRole('button', { name: /Issue: Clickable Issue/i });
      fireEvent.click(card);

      expect(handleIssueClick).toHaveBeenCalledTimes(1);
      expect(handleIssueClick).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'click-test', title: 'Clickable Issue' })
      );
    });

    it('calls onIssueClick with correct issue from different lanes', () => {
      const handleIssueClick = vi.fn();
      const issues = [
        createMockIssue({
          id: 'alice-issue',
          title: 'Alice Issue',
          assignee: 'alice',
          status: 'open',
        }),
        createMockIssue({ id: 'bob-issue', title: 'Bob Issue', assignee: 'bob', status: 'open' }),
      ];

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          onIssueClick={handleIssueClick}
        />
      );

      const aliceCard = screen.getByRole('button', { name: /Issue: Alice Issue/i });
      const bobCard = screen.getByRole('button', { name: /Issue: Bob Issue/i });

      fireEvent.click(aliceCard);
      expect(handleIssueClick).toHaveBeenLastCalledWith(
        expect.objectContaining({ id: 'alice-issue' })
      );

      fireEvent.click(bobCard);
      expect(handleIssueClick).toHaveBeenLastCalledWith(
        expect.objectContaining({ id: 'bob-issue' })
      );

      expect(handleIssueClick).toHaveBeenCalledTimes(2);
    });
  });

  describe('blocked issues filtering', () => {
    it('shows all issues including blocked when showBlocked=true (default)', () => {
      const issues = [
        createMockIssue({
          id: 'normal-issue',
          title: 'Normal Issue',
          assignee: 'alice',
          status: 'open',
        }),
        createMockIssue({
          id: 'blocked-issue',
          title: 'Blocked Issue',
          assignee: 'alice',
          status: 'open',
        }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          blockedIssues={blockedIssues}
        />
      );

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
    });

    it('hides blocked issues when showBlocked=false', () => {
      const issues = [
        createMockIssue({
          id: 'normal-issue',
          title: 'Normal Issue',
          assignee: 'alice',
          status: 'open',
        }),
        createMockIssue({
          id: 'blocked-issue',
          title: 'Blocked Issue',
          assignee: 'alice',
          status: 'open',
        }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          blockedIssues={blockedIssues}
          showBlocked={false}
        />
      );

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.queryByText('Blocked Issue')).not.toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies custom className', () => {
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice' })];

      const { container } = render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          className="custom-board-class"
        />
      );

      const board = container.querySelector('[data-testid="swim-lane-board"]');
      expect(board).toHaveClass('custom-board-class');
    });

    it('uses custom statuses', () => {
      const customStatuses: Status[] = ['blocked', 'deferred'];
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'blocked' })];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={customStatuses} />);

      // Check for custom status columns within the lane
      expect(screen.getByRole('region', { name: 'Blocked issues' })).toBeInTheDocument();
      expect(screen.getByRole('region', { name: 'Deferred issues' })).toBeInTheDocument();
    });
  });

  describe('sorting', () => {
    it('sorts lanes by title when sortLanesBy=title', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'bob' }),
        createMockIssue({ id: 'issue-2', assignee: 'alice' }),
        createMockIssue({ id: 'issue-3', assignee: 'charlie' }),
      ];

      const { container } = render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          sortLanesBy="title"
        />
      );

      // Select only lane title h3 elements (those inside header elements)
      const laneHeadings = container.querySelectorAll('header h3');
      const titles = Array.from(laneHeadings).map((h) => h.textContent);

      expect(titles[0]).toBe('alice');
      expect(titles[1]).toBe('bob');
      expect(titles[2]).toBe('charlie');
    });

    it('sorts lanes by issue count when sortLanesBy=count', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', assignee: 'alice' }),
        createMockIssue({ id: 'issue-2', assignee: 'bob' }),
        createMockIssue({ id: 'issue-3', assignee: 'bob' }),
        createMockIssue({ id: 'issue-4', assignee: 'bob' }),
        createMockIssue({ id: 'issue-5', assignee: 'charlie' }),
        createMockIssue({ id: 'issue-6', assignee: 'charlie' }),
      ];

      const { container } = render(
        <SwimLaneBoard
          issues={issues}
          groupBy="assignee"
          statuses={defaultStatuses}
          sortLanesBy="count"
        />
      );

      // Select only lane title h3 elements (those inside header elements)
      const laneHeadings = container.querySelectorAll('header h3');
      const titles = Array.from(laneHeadings).map((h) => h.textContent);

      // bob has 3, charlie has 2, alice has 1
      expect(titles[0]).toBe('bob');
      expect(titles[1]).toBe('charlie');
      expect(titles[2]).toBe('alice');
    });
  });

  describe('edge cases', () => {
    it('renders empty board with no issues', () => {
      render(<SwimLaneBoard issues={[]} groupBy="assignee" statuses={defaultStatuses} />);

      // Should render the board container but with no lanes
      expect(screen.getByTestId('swim-lane-board')).toBeInTheDocument();
      expect(screen.queryByRole('heading', { level: 3 })).not.toBeInTheDocument();
    });

    it('handles issues appearing in multiple label lanes', () => {
      const issues = [
        createMockIssue({
          id: 'multi-label',
          title: 'Multi Label Issue',
          labels: ['frontend', 'urgent'],
          status: 'open',
        }),
        createMockIssue({
          id: 'single-label',
          title: 'Single Label Issue',
          labels: ['backend'],
          status: 'open',
        }),
      ];

      render(<SwimLaneBoard issues={issues} groupBy="label" statuses={defaultStatuses} />);

      // Multi-label issue should appear in both frontend and urgent lanes
      const frontendLane = screen.getByTestId('swim-lane-lane-label-frontend');
      const urgentLane = screen.getByTestId('swim-lane-lane-label-urgent');

      expect(frontendLane).toBeInTheDocument();
      expect(urgentLane).toBeInTheDocument();

      // Count the Multi Label Issue cards - should be 2 (one in each lane)
      const multiLabelCards = screen.getAllByText('Multi Label Issue');
      expect(multiLabelCards).toHaveLength(2);
    });

    it('handles large number of issues', () => {
      const issues = Array.from({ length: 100 }, (_, i) =>
        createMockIssue({
          id: `issue-${i}`,
          title: `Issue ${i}`,
          assignee: `user-${i % 5}`,
          status: ['open', 'in_progress', 'closed'][i % 3] as Status,
        })
      );

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // Should render without crashing
      expect(screen.getByTestId('swim-lane-board')).toBeInTheDocument();

      // Should have 5 lanes (user-0 through user-4)
      expect(screen.getByRole('heading', { name: 'user-0' })).toBeInTheDocument();
      expect(screen.getByRole('heading', { name: 'user-4' })).toBeInTheDocument();
    });
  });

  describe('DndContext integration', () => {
    it('wraps lanes in DndContext for drag and drop', () => {
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' })];

      render(<SwimLaneBoard issues={issues} groupBy="assignee" statuses={defaultStatuses} />);

      // The draggable wrapper should have role="button" and aria-roledescription="draggable"
      const draggable = document.querySelector('[aria-roledescription="draggable"]');
      expect(draggable).toBeInTheDocument();
    });

    it('accepts onDragEnd prop without error', () => {
      const handleDragEnd = vi.fn();
      const issues = [createMockIssue({ id: 'issue-1', assignee: 'alice', status: 'open' })];

      expect(() => {
        render(
          <SwimLaneBoard
            issues={issues}
            groupBy="assignee"
            statuses={defaultStatuses}
            onDragEnd={handleDragEnd}
          />
        );
      }).not.toThrow();

      expect(screen.getByTestId('swim-lane-board')).toBeInTheDocument();
    });
  });
});
