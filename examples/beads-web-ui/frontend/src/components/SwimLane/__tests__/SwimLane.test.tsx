/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SwimLane component.
 */

import { DndContext } from '@dnd-kit/core';
import '@testing-library/jest-dom';
import { render, screen, within, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi, beforeEach } from 'vitest';

import type { BlockedInfo, KanbanColumnConfig } from '@/components/KanbanBoard';
import type { Issue, Status } from '@/types';

import { SwimLane } from '../SwimLane';

/**
 * Helper to render SwimLane within a DndContext for droppable tests.
 */
function renderWithDndContext(ui: React.ReactNode) {
  return render(<DndContext>{ui}</DndContext>);
}

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
 * Create multiple mock issues with specified statuses.
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

/**
 * Helper to convert statuses to column configs for testing.
 * Handles undefined status as 'open' for backward compatibility.
 */
function statusesToColumns(statuses: Status[]): KanbanColumnConfig[] {
  return statuses.map((s) => ({
    id: s,
    label: s.replace(/_/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase()),
    filter: (issue: Issue) =>
      s === 'open' ? issue.status === s || issue.status === undefined : issue.status === s,
    targetStatus: s,
  }));
}

/**
 * Default columns for testing (3-status layout for backward compatibility).
 */
const defaultColumns = statusesToColumns(['open', 'in_progress', 'closed']);

describe('SwimLane', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('rendering', () => {
    it('renders lane header with correct title', () => {
      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Epic: User Authentication"
          issues={[]}
          columns={defaultColumns}
        />
      );

      expect(
        screen.getByRole('heading', { name: 'Epic: User Authentication' })
      ).toBeInTheDocument();
    });

    it('shows correct issue count', () => {
      const issues = createMockIssues(['open', 'open', 'in_progress', 'closed']);

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={issues} columns={defaultColumns} />
      );

      // The count badge should show 4 issues total
      expect(screen.getByLabelText('4 issues')).toBeInTheDocument();
      expect(screen.getByText('4')).toBeInTheDocument();
    });

    it('renders all status columns', () => {
      const columns = statusesToColumns(['open', 'in_progress', 'closed', 'blocked']);

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={columns} />
      );

      // Each column should be present
      expect(screen.getByRole('region', { name: 'Open issues' })).toBeInTheDocument();
      expect(screen.getByRole('region', { name: 'In Progress issues' })).toBeInTheDocument();
      expect(screen.getByRole('region', { name: 'Closed issues' })).toBeInTheDocument();
      expect(screen.getByRole('region', { name: 'Blocked issues' })).toBeInTheDocument();
    });

    it('groups issues by status correctly', () => {
      const issues = [
        createMockIssue({ id: 'open-1', title: 'Open Issue 1', status: 'open' }),
        createMockIssue({ id: 'progress-1', title: 'Progress Issue 1', status: 'in_progress' }),
        createMockIssue({ id: 'open-2', title: 'Open Issue 2', status: 'open' }),
        createMockIssue({ id: 'closed-1', title: 'Closed Issue 1', status: 'closed' }),
      ];

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={issues} columns={defaultColumns} />
      );

      // Verify issues are in correct columns
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByText('Open Issue 1')).toBeInTheDocument();
      expect(within(openColumn).getByText('Open Issue 2')).toBeInTheDocument();

      const progressColumn = screen.getByRole('region', { name: 'In Progress issues' });
      expect(within(progressColumn).getByText('Progress Issue 1')).toBeInTheDocument();

      const closedColumn = screen.getByRole('region', { name: 'Closed issues' });
      expect(within(closedColumn).getByText('Closed Issue 1')).toBeInTheDocument();
    });
  });

  describe('collapse toggle', () => {
    it('calls onToggleCollapse when toggle clicked', () => {
      const handleToggleCollapse = vi.fn();

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          onToggleCollapse={handleToggleCollapse}
        />
      );

      const toggleButton = screen.getByTestId('collapse-toggle');
      fireEvent.click(toggleButton);

      expect(handleToggleCollapse).toHaveBeenCalledTimes(1);
    });

    it('has correct aria-expanded attribute when expanded', () => {
      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          isCollapsed={false}
        />
      );

      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'true');
      expect(toggleButton).toHaveAttribute('aria-label', 'Collapse Test Lane');
    });

    it('has correct aria-expanded attribute when collapsed', () => {
      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          isCollapsed={true}
        />
      );

      const toggleButton = screen.getByTestId('collapse-toggle');
      expect(toggleButton).toHaveAttribute('aria-expanded', 'false');
      expect(toggleButton).toHaveAttribute('aria-label', 'Expand Test Lane');
    });

    it('content hidden with data-collapsed when collapsed', () => {
      const { container } = renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          isCollapsed={true}
        />
      );

      // Check the section has data-collapsed="true"
      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-collapsed', 'true');

      // Check the content area has data-collapsed="true"
      const contentArea = container.querySelector('[data-collapsed="true"]');
      expect(contentArea).toBeInTheDocument();
    });

    it('content visible when not collapsed', () => {
      const { container } = renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          isCollapsed={false}
        />
      );

      // Check the section has data-collapsed="false"
      const section = container.querySelector('section');
      expect(section).toHaveAttribute('data-collapsed', 'false');
    });
  });

  describe('issue click callback', () => {
    it('calls onIssueClick when card clicked', () => {
      const handleIssueClick = vi.fn();
      const issue = createMockIssue({ id: 'click-test', title: 'Clickable Issue' });

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[issue]}
          columns={defaultColumns}
          onIssueClick={handleIssueClick}
        />
      );

      // When onIssueClick is provided, IssueCard has role="button"
      const card = screen.getByRole('button', { name: /Issue: Clickable Issue/i });
      fireEvent.click(card);

      expect(handleIssueClick).toHaveBeenCalledTimes(1);
      expect(handleIssueClick).toHaveBeenCalledWith(
        expect.objectContaining({ id: 'click-test', title: 'Clickable Issue' })
      );
    });

    it('clicking different cards calls onIssueClick with correct issue', () => {
      const handleIssueClick = vi.fn();
      const issues = [
        createMockIssue({ id: 'issue-a', title: 'Issue A', status: 'open' }),
        createMockIssue({ id: 'issue-b', title: 'Issue B', status: 'in_progress' }),
      ];

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          onIssueClick={handleIssueClick}
        />
      );

      const cardA = screen.getByRole('button', { name: /Issue: Issue A/i });
      const cardB = screen.getByRole('button', { name: /Issue: Issue B/i });

      fireEvent.click(cardB);
      expect(handleIssueClick).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-b' }));

      fireEvent.click(cardA);
      expect(handleIssueClick).toHaveBeenLastCalledWith(expect.objectContaining({ id: 'issue-a' }));

      expect(handleIssueClick).toHaveBeenCalledTimes(2);
    });
  });

  describe('blocked issues filtering', () => {
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

    it('shows all issues including blocked when showBlocked=true (default)', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          blockedIssues={blockedIssues}
        />
      );

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.getByText('Blocked Issue')).toBeInTheDocument();
    });

    it('filters blocked issues when showBlocked=false', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-issue']);

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          blockedIssues={blockedIssues}
          showBlocked={false}
        />
      );

      expect(screen.getByText('Normal Issue')).toBeInTheDocument();
      expect(screen.queryByText('Blocked Issue')).not.toBeInTheDocument();
    });

    it('issue count reflects filtered issues', () => {
      const issues = [
        createMockIssue({ id: 'normal-1', title: 'Normal 1', status: 'open' }),
        createMockIssue({ id: 'normal-2', title: 'Normal 2', status: 'open' }),
        createMockIssue({ id: 'blocked-1', title: 'Blocked 1', status: 'open' }),
        createMockIssue({ id: 'blocked-2', title: 'Blocked 2', status: 'in_progress' }),
      ];
      const blockedIssues = createBlockedIssuesMap(['blocked-1', 'blocked-2']);

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          blockedIssues={blockedIssues}
          showBlocked={false}
        />
      );

      // The lane header count should show 2 (total non-blocked issues)
      // Get the header within the swim lane to check the total count
      const swimLane = screen.getByTestId('swim-lane-test-lane');
      const header = swimLane.querySelector('header');
      expect(header).toBeInTheDocument();

      // The count badge in the lane header shows total filtered issues
      const laneCount = within(header!).getByLabelText('2 issues');
      expect(laneCount).toBeInTheDocument();
    });

    it('shows all issues when showBlocked=false but blockedIssues is undefined', () => {
      const issues = [
        createMockIssue({ id: 'issue-1', title: 'Issue 1', status: 'open' }),
        createMockIssue({ id: 'issue-2', title: 'Issue 2', status: 'open' }),
      ];

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          showBlocked={false}
        />
      );

      expect(screen.getByText('Issue 1')).toBeInTheDocument();
      expect(screen.getByText('Issue 2')).toBeInTheDocument();
    });
  });

  describe('props', () => {
    it('applies custom className', () => {
      const { container } = renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[]}
          columns={defaultColumns}
          className="custom-lane-class"
        />
      );

      const section = container.querySelector('section');
      expect(section).toHaveClass('custom-lane-class');
    });

    it('has correct data-testid based on id prop', () => {
      renderWithDndContext(
        <SwimLane
          id="epic-auth"
          title="Epic: Authentication"
          issues={[]}
          columns={defaultColumns}
        />
      );

      expect(screen.getByTestId('swim-lane-epic-auth')).toBeInTheDocument();
    });

    it('renders with aria-labelledby pointing to header', () => {
      const { container } = renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={defaultColumns} />
      );

      const section = container.querySelector('section');
      const headerId = section?.getAttribute('aria-labelledby');
      expect(headerId).toBe('lane-header-test-lane');

      // The header element should have this id
      const header = document.getElementById('lane-header-test-lane');
      expect(header).toBeInTheDocument();
    });
  });

  describe('edge cases', () => {
    it('renders empty columns with EmptyColumn component', () => {
      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={defaultColumns} />
      );

      // Each empty column should show EmptyColumn message
      expect(screen.getByText('No open issues')).toBeInTheDocument();
      expect(screen.getByText('No issues in progress')).toBeInTheDocument();
      expect(screen.getByText('No closed issues')).toBeInTheDocument();
    });

    it('issues without status default to open', () => {
      const issueWithoutStatus = createMockIssue({
        id: 'no-status-issue',
        title: 'No Status Issue',
      });
      delete (issueWithoutStatus as Partial<Issue>).status;

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[issueWithoutStatus]}
          columns={defaultColumns}
        />
      );

      // Issue should appear in Open column
      const openColumn = screen.getByRole('region', { name: 'Open issues' });
      expect(within(openColumn).getByText('No Status Issue')).toBeInTheDocument();
    });

    it('issues with unknown status are not displayed if status not in columns', () => {
      const issueWithUnknownStatus = createMockIssue({
        id: 'unknown-status',
        title: 'Unknown Status Issue',
        status: 'unknown_status' as Status,
      });

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={[issueWithUnknownStatus]}
          columns={defaultColumns}
        />
      );

      // Issue should not appear since 'unknown_status' is not in columns
      expect(screen.queryByText('Unknown Status Issue')).not.toBeInTheDocument();
    });

    it('handles large number of issues', () => {
      const manyIssues = Array.from({ length: 50 }, (_, i) =>
        createMockIssue({
          id: `issue-${i}`,
          title: `Issue ${i}`,
          status: ['open', 'in_progress', 'closed'][i % 3] as Status,
        })
      );

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={manyIssues} columns={defaultColumns} />
      );

      // Should render without crashing
      expect(screen.getByLabelText('50 issues')).toBeInTheDocument();
      expect(screen.getByText('50')).toBeInTheDocument();
    });

    it('isCollapsed disables droppable on columns', () => {
      const issues = [createMockIssue({ id: 'open-1', title: 'Open Issue', status: 'open' })];

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          isCollapsed={true}
        />
      );

      // The lane content should still render but with aria-hidden
      const content = screen
        .getByRole('heading', { name: 'Test Lane' })
        .closest('section')
        ?.querySelector('[aria-hidden="true"]');
      expect(content).toBeInTheDocument();
    });
  });

  describe('droppable functionality', () => {
    it('each column has a droppable zone', () => {
      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={defaultColumns} />
      );

      // Check droppable zones exist for each column
      defaultColumns.forEach((col) => {
        const droppable = document.querySelector(`[data-droppable-id="${col.id}"]`);
        expect(droppable).toBeInTheDocument();
      });
    });

    it('works with multiple SwimLanes in same DndContext', () => {
      const twoColumns = statusesToColumns(['open', 'closed']);
      render(
        <DndContext>
          <SwimLane id="lane-1" title="Lane 1" issues={[]} columns={twoColumns} />
          <SwimLane id="lane-2" title="Lane 2" issues={[]} columns={twoColumns} />
        </DndContext>
      );

      // Both lanes should render
      expect(screen.getByText('Lane 1')).toBeInTheDocument();
      expect(screen.getByText('Lane 2')).toBeInTheDocument();
    });
  });

  describe('backlog column (Pending→Backlog rename)', () => {
    it('passes columnType="backlog" to StatusColumn for backlog column', () => {
      const columns: KanbanColumnConfig[] = [
        ...statusesToColumns(['open', 'in_progress']),
        {
          id: 'backlog',
          label: 'Backlog',
          filter: (issue: Issue) => issue.status === 'blocked' || issue.status === 'deferred',
          targetStatus: 'blocked',
        },
      ];

      const { container } = renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={columns} />
      );

      // The backlog column should have data-column-type="backlog"
      const backlogSection = container.querySelector('[data-column-type="backlog"]');
      expect(backlogSection).toBeInTheDocument();
    });

    it('passes isBacklog=true to DraggableIssueCard in backlog column', () => {
      const blockedIssue = createMockIssue({
        id: 'blocked-1',
        title: 'Blocked Issue',
        status: 'blocked',
      });

      const columns: KanbanColumnConfig[] = [
        ...statusesToColumns(['open', 'in_progress']),
        {
          id: 'backlog',
          label: 'Backlog',
          filter: (issue: Issue) => issue.status === 'blocked' || issue.status === 'deferred',
          targetStatus: 'blocked',
        },
      ];

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[blockedIssue]} columns={columns} />
      );

      // IssueCard with isBacklog=true sets data-in-backlog="true"
      const card = screen.getByText('Blocked Issue').closest('[data-in-backlog="true"]');
      expect(card).toBeInTheDocument();
    });

    it('does not pass isBacklog to cards in non-backlog columns', () => {
      const openIssue = createMockIssue({
        id: 'open-1',
        title: 'Open Issue',
        status: 'open',
      });

      const columns: KanbanColumnConfig[] = [
        ...statusesToColumns(['open', 'in_progress']),
        {
          id: 'backlog',
          label: 'Backlog',
          filter: (issue: Issue) => issue.status === 'blocked' || issue.status === 'deferred',
          targetStatus: 'blocked',
        },
      ];

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[openIssue]} columns={columns} />
      );

      // Card in open column should not have data-in-backlog
      const card = screen.getByText('Open Issue').closest('article');
      expect(card).not.toHaveAttribute('data-in-backlog');
    });

    it('renders EmptyColumn with backlog status for empty backlog column', () => {
      const columns: KanbanColumnConfig[] = [
        ...statusesToColumns(['open']),
        {
          id: 'backlog',
          label: 'Backlog',
          filter: (issue: Issue) => issue.status === 'blocked' || issue.status === 'deferred',
          targetStatus: 'blocked',
        },
      ];

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={columns} />
      );

      // EmptyColumn with status="backlog" should show backlog-specific message
      expect(screen.getByText('No blocked or deferred issues')).toBeInTheDocument();
    });

    it('renders backlog column without emoji headerIcon', () => {
      const columns: KanbanColumnConfig[] = [
        {
          id: 'backlog',
          label: 'Backlog',
          filter: (issue: Issue) => issue.status === 'blocked' || issue.status === 'deferred',
          targetStatus: 'blocked',
        },
      ];

      renderWithDndContext(
        <SwimLane id="test-lane" title="Test Lane" issues={[]} columns={columns} />
      );

      // Backlog column should render with its label but no emoji icon
      expect(screen.getByText('Backlog')).toBeInTheDocument();
      expect(screen.queryByText('⏳')).not.toBeInTheDocument();
    });
  });

  describe('blocked badge display', () => {
    it('passes blockedInfo to DraggableIssueCard for blocked issues', () => {
      const issues = [
        createMockIssue({ id: 'blocked-issue', title: 'Blocked Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, BlockedInfo>([
        [
          'blocked-issue',
          { blockedByCount: 3, blockedBy: ['blocker-1', 'blocker-2', 'blocker-3'] },
        ],
      ]);

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          blockedIssues={blockedIssues}
        />
      );

      // The BlockedBadge should be rendered within the card
      expect(screen.getByLabelText('Blocked by 3 issues')).toBeInTheDocument();
    });

    it('does not pass blockedInfo for non-blocked issues', () => {
      const issues = [
        createMockIssue({ id: 'normal-issue', title: 'Normal Issue', status: 'open' }),
      ];
      const blockedIssues = new Map<string, BlockedInfo>([
        ['other-issue', { blockedByCount: 1, blockedBy: ['blocker-1'] }],
      ]);

      renderWithDndContext(
        <SwimLane
          id="test-lane"
          title="Test Lane"
          issues={issues}
          columns={defaultColumns}
          blockedIssues={blockedIssues}
        />
      );

      // No BlockedBadge should be rendered
      expect(screen.queryByLabelText(/Blocked by/)).not.toBeInTheDocument();
    });
  });
});
