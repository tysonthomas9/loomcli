/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for IssueRow component.
 * Tests rendering, selection state, click handling, keyboard navigation,
 * and cell content rendering.
 */

import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import '@testing-library/jest-dom';

import { IssueRow, IssueRowProps } from '../IssueRow';
import { DEFAULT_ISSUE_COLUMNS, ColumnDef } from '../columns';
import type { Issue, Status } from '@/types';

// Helper to create mock issues for testing
function createMockIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: 'bd-abc',
    title: 'Test Issue',
    priority: 2,
    status: 'open',
    issue_type: 'task',
    assignee: 'test-user',
    created_at: '2024-01-15T10:30:00Z',
    updated_at: '2024-01-15T12:00:00Z',
    ...overrides,
  };
}

// Helper to render IssueRow wrapped in a table
function renderIssueRow(props: IssueRowProps<Issue>) {
  return render(
    <table>
      <tbody>
        <IssueRow {...props} />
      </tbody>
    </table>
  );
}

// ============= Rendering Tests =============

describe('IssueRow', () => {
  describe('rendering', () => {
    it('renders tr element with correct data-testid', () => {
      const issue = createMockIssue({ id: 'bd-test-123' });
      renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      const row = screen.getByTestId('issue-row-bd-test-123');
      expect(row).toBeInTheDocument();
      expect(row.tagName).toBe('TR');
    });

    it('renders correct number of td cells based on columns', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      const cells = container.querySelectorAll('td');
      expect(cells).toHaveLength(DEFAULT_ISSUE_COLUMNS.length);
    });

    it('renders fewer cells with custom column subset', () => {
      const issue = createMockIssue();
      const customColumns: ColumnDef<Issue>[] = [
        { id: 'id', header: 'ID', accessor: 'id' },
        { id: 'title', header: 'Title', accessor: 'title' },
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const cells = container.querySelectorAll('td');
      expect(cells).toHaveLength(2);
    });

    it('renders cell content correctly for each column type', () => {
      const issue = createMockIssue({
        id: 'bd-xyz',
        title: 'My Title',
        priority: 1,
        status: 'in_progress',
        issue_type: 'bug',
        assignee: 'alice',
      });
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      // Check ID cell
      expect(container.querySelector('[data-column="id"]')).toHaveTextContent(
        'bd-xyz'
      );

      // Check priority cell
      expect(
        container.querySelector('[data-column="priority"]')
      ).toHaveTextContent('P1');

      // Check title cell
      expect(container.querySelector('[data-column="title"]')).toHaveTextContent(
        'My Title'
      );

      // Check status cell
      expect(
        container.querySelector('[data-column="status"]')
      ).toHaveTextContent('In progress');

      // Check issue_type cell
      expect(
        container.querySelector('[data-column="issue_type"]')
      ).toHaveTextContent('Bug');

      // Check assignee cell
      expect(
        container.querySelector('[data-column="assignee"]')
      ).toHaveTextContent('alice');
    });

    it('applies text alignment from column definition', () => {
      const issue = createMockIssue();
      const customColumns: ColumnDef<Issue>[] = [
        { id: 'id', header: 'ID', accessor: 'id', align: 'left' },
        { id: 'priority', header: 'Priority', accessor: 'priority', align: 'center' },
        { id: 'updated_at', header: 'Updated', accessor: 'updated_at', align: 'right' },
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const idCell = container.querySelector('[data-column="id"]');
      const priorityCell = container.querySelector('[data-column="priority"]');
      const updatedCell = container.querySelector('[data-column="updated_at"]');

      expect(idCell).toHaveStyle({ textAlign: 'left' });
      expect(priorityCell).toHaveStyle({ textAlign: 'center' });
      expect(updatedCell).toHaveStyle({ textAlign: 'right' });
    });

    it('uses default left alignment when not specified', () => {
      const issue = createMockIssue();
      const customColumns: ColumnDef<Issue>[] = [
        { id: 'id', header: 'ID', accessor: 'id' }, // no align specified
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const cell = container.querySelector('[data-column="id"]');
      expect(cell).toHaveStyle({ textAlign: 'left' });
    });

    it('applies base row class', () => {
      const issue = createMockIssue();
      const row = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      }).container.querySelector('tr');

      expect(row).toHaveClass('issue-table__row');
    });

    it('applies custom className when provided', () => {
      const issue = createMockIssue();
      const row = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        className: 'my-custom-row',
      }).container.querySelector('tr');

      expect(row).toHaveClass('my-custom-row');
    });
  });

  // ============= Selection State Tests =============

  describe('selection state', () => {
    it('has no selected class when isSelected is false', () => {
      const issue = createMockIssue();
      const row = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: false,
      }).container.querySelector('tr');

      expect(row).not.toHaveClass('issue-table__row--selected');
    });

    it('has no selected class when isSelected is undefined', () => {
      const issue = createMockIssue();
      const row = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      }).container.querySelector('tr');

      expect(row).not.toHaveClass('issue-table__row--selected');
    });

    it('has selected class when isSelected is true', () => {
      const issue = createMockIssue();
      const row = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: true,
      }).container.querySelector('tr');

      expect(row).toHaveClass('issue-table__row--selected');
    });

    it('aria-selected is not set when row is not clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: true, // Selection is set but row is not clickable
        isClickable: false,
      });

      // aria-selected is not set when row is not clickable (for proper ARIA semantics)
      expect(container.querySelector('tr')).not.toHaveAttribute('aria-selected');
    });

    it('aria-selected is false when isSelected is false and row is clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: false,
        isClickable: true,
        onClick: vi.fn(),
      });

      expect(container.querySelector('tr')).toHaveAttribute(
        'aria-selected',
        'false'
      );
    });

    it('aria-selected is true when isSelected is true and row is clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: true,
        isClickable: true,
        onClick: vi.fn(),
      });

      expect(container.querySelector('tr')).toHaveAttribute(
        'aria-selected',
        'true'
      );
    });
  });

  // ============= Click Handling Tests =============

  describe('click handling', () => {
    it('clickable class applied when isClickable is true', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: vi.fn(),
      });

      expect(container.querySelector('tr')).toHaveClass(
        'issue-table__row--clickable'
      );
    });

    it('no clickable class when isClickable is false', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: false,
      });

      expect(container.querySelector('tr')).not.toHaveClass(
        'issue-table__row--clickable'
      );
    });

    it('no clickable class when isClickable is undefined', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      expect(container.querySelector('tr')).not.toHaveClass(
        'issue-table__row--clickable'
      );
    });

    it('onClick called with issue data when row clicked', () => {
      const issue = createMockIssue({ id: 'bd-click-test' });
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: handleClick,
      });

      fireEvent.click(container.querySelector('tr')!);

      expect(handleClick).toHaveBeenCalledTimes(1);
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it('onClick not called when isClickable is false', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: false,
        onClick: handleClick,
      });

      fireEvent.click(container.querySelector('tr')!);

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('onClick not called when isClickable is undefined', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        onClick: handleClick,
      });

      fireEvent.click(container.querySelector('tr')!);

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('row has tabIndex when clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: vi.fn(),
      });

      expect(container.querySelector('tr')).toHaveAttribute('tabIndex', '0');
    });

    it('row has no tabIndex when not clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: false,
      });

      expect(container.querySelector('tr')).not.toHaveAttribute('tabIndex');
    });

    it('row does not override role when clickable (preserves table row semantics)', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: vi.fn(),
      });

      // We no longer set role="button" to preserve table row semantics for accessibility
      expect(container.querySelector('tr')).not.toHaveAttribute('role');
    });
  });

  // ============= Keyboard Tests =============

  describe('keyboard navigation', () => {
    it('Enter key triggers onClick', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: handleClick,
      });

      fireEvent.keyDown(container.querySelector('tr')!, { key: 'Enter' });

      expect(handleClick).toHaveBeenCalledTimes(1);
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it('Space key triggers onClick', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: handleClick,
      });

      fireEvent.keyDown(container.querySelector('tr')!, { key: ' ' });

      expect(handleClick).toHaveBeenCalledTimes(1);
      expect(handleClick).toHaveBeenCalledWith(issue);
    });

    it('other keys do not trigger onClick', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        onClick: handleClick,
      });

      fireEvent.keyDown(container.querySelector('tr')!, { key: 'Tab' });
      fireEvent.keyDown(container.querySelector('tr')!, { key: 'Escape' });
      fireEvent.keyDown(container.querySelector('tr')!, { key: 'ArrowDown' });
      fireEvent.keyDown(container.querySelector('tr')!, { key: 'a' });

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('keyboard events ignored when not clickable', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: false,
        onClick: handleClick,
      });

      fireEvent.keyDown(container.querySelector('tr')!, { key: 'Enter' });
      fireEvent.keyDown(container.querySelector('tr')!, { key: ' ' });

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('keyboard events ignored when onClick is undefined', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isClickable: true,
        // onClick not provided
      });

      // Should not throw
      fireEvent.keyDown(container.querySelector('tr')!, { key: 'Enter' });
      fireEvent.keyDown(container.querySelector('tr')!, { key: ' ' });
    });
  });

  // ============= Cell Content Tests =============

  describe('cell content', () => {
    describe('priority badge', () => {
      it.each([0, 1, 2, 3, 4] as const)(
        'priority %d has correct class priority-%d',
        (priority) => {
          const issue = createMockIssue({ priority });
          const { container } = renderIssueRow({
            issue,
            columns: DEFAULT_ISSUE_COLUMNS,
          });

          const priorityCell = container.querySelector('[data-column="priority"]');
          const priorityBadge = priorityCell?.querySelector(
            '.issue-table__priority'
          );

          expect(priorityBadge).toHaveClass(`priority-${priority}`);
        }
      );

      it('displays priority value as P0-P4', () => {
        const issue = createMockIssue({ priority: 3 });
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const priorityCell = container.querySelector('[data-column="priority"]');
        expect(priorityCell).toHaveTextContent('P3');
      });
    });

    describe('status badge', () => {
      const statusTests: { status: Status; expectedClass: string; expectedText: string }[] = [
        { status: 'open', expectedClass: 'status-open', expectedText: 'Open' },
        {
          status: 'in_progress',
          expectedClass: 'status-in-progress',
          expectedText: 'In progress',
        },
        { status: 'blocked', expectedClass: 'status-blocked', expectedText: 'Blocked' },
        { status: 'closed', expectedClass: 'status-closed', expectedText: 'Closed' },
        { status: 'deferred', expectedClass: 'status-deferred', expectedText: 'Deferred' },
      ];

      it.each(statusTests)(
        'status $status has correct class $expectedClass',
        ({ status, expectedClass }) => {
          const issue = createMockIssue({ status });
          const { container } = renderIssueRow({
            issue,
            columns: DEFAULT_ISSUE_COLUMNS,
          });

          const statusCell = container.querySelector('[data-column="status"]');
          const statusBadge = statusCell?.querySelector('.issue-table__status');

          expect(statusBadge).toHaveClass(expectedClass);
        }
      );

      it.each(statusTests)(
        'status $status displays text $expectedText',
        ({ status, expectedText }) => {
          const issue = createMockIssue({ status });
          const { container } = renderIssueRow({
            issue,
            columns: DEFAULT_ISSUE_COLUMNS,
          });

          const statusCell = container.querySelector('[data-column="status"]');
          expect(statusCell).toHaveTextContent(expectedText);
        }
      );
    });

    describe('ID cell', () => {
      it('ID displayed with issue-table__id class', () => {
        const issue = createMockIssue({ id: 'bd-test-id' });
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const idCell = container.querySelector('[data-column="id"]');
        const idSpan = idCell?.querySelector('.issue-table__id');

        expect(idSpan).toBeInTheDocument();
        expect(idSpan).toHaveTextContent('bd-test-id');
      });
    });

    describe('missing fields', () => {
      it('missing status shows "-" placeholder', () => {
        const issue: Issue = {
          id: 'bd-no-status',
          title: 'No Status Issue',
          priority: 2,
          created_at: '2024-01-15T10:30:00Z',
          updated_at: '2024-01-15T12:00:00Z',
          // status is omitted
        };
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const statusCell = container.querySelector('[data-column="status"]');
        expect(statusCell).toHaveTextContent('-');
      });

      it('missing assignee shows "-" placeholder', () => {
        const issue: Issue = {
          id: 'bd-no-assignee',
          title: 'No Assignee Issue',
          priority: 2,
          created_at: '2024-01-15T10:30:00Z',
          updated_at: '2024-01-15T12:00:00Z',
          // assignee is omitted
        };
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const assigneeCell = container.querySelector('[data-column="assignee"]');
        expect(assigneeCell).toHaveTextContent('-');
      });

      it('missing issue_type shows "-" placeholder', () => {
        const issue: Issue = {
          id: 'bd-no-type',
          title: 'No Type Issue',
          priority: 2,
          created_at: '2024-01-15T10:30:00Z',
          updated_at: '2024-01-15T12:00:00Z',
          // issue_type is omitted
        };
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const typeCell = container.querySelector('[data-column="issue_type"]');
        expect(typeCell).toHaveTextContent('-');
      });
    });

    describe('title cell', () => {
      it('title has issue-table__title class', () => {
        const issue = createMockIssue({ title: 'My Test Title' });
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const titleCell = container.querySelector('[data-column="title"]');
        const titleSpan = titleCell?.querySelector('.issue-table__title');

        expect(titleSpan).toBeInTheDocument();
        expect(titleSpan).toHaveTextContent('My Test Title');
      });

      it('title has title attribute for truncation tooltip', () => {
        const longTitle = 'This is a very long title that might be truncated';
        const issue = createMockIssue({ title: longTitle });
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const titleCell = container.querySelector('[data-column="title"]');
        const titleSpan = titleCell?.querySelector('.issue-table__title');

        expect(titleSpan).toHaveAttribute('title', longTitle);
      });
    });

    describe('date cell', () => {
      it('date has issue-table__date class', () => {
        const issue = createMockIssue();
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const dateCell = container.querySelector('[data-column="updated_at"]');
        const dateSpan = dateCell?.querySelector('.issue-table__date');

        expect(dateSpan).toBeInTheDocument();
      });

      it('recent date shows relative time', () => {
        // Use a date 5 minutes ago
        const fiveMinutesAgo = new Date(Date.now() - 5 * 60 * 1000).toISOString();
        const issue = createMockIssue({ updated_at: fiveMinutesAgo });
        const { container } = renderIssueRow({
          issue,
          columns: DEFAULT_ISSUE_COLUMNS,
        });

        const dateCell = container.querySelector('[data-column="updated_at"]');
        // Should show "Xm ago"
        expect(dateCell?.textContent).toMatch(/\d+m ago/);
      });
    });
  });

  // ============= Custom Accessor Tests =============

  describe('custom column accessors', () => {
    it('supports function accessor', () => {
      const issue = createMockIssue({ id: 'bd-001', title: 'Test' });
      const customColumns: ColumnDef<Issue>[] = [
        {
          id: 'combined',
          header: 'Combined',
          accessor: (i) => `${i.id} - ${i.title}`,
        },
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const cell = container.querySelector('[data-column="combined"]');
      expect(cell).toHaveTextContent('bd-001 - Test');
    });

    it('renders unknown column id with stringified value', () => {
      const issue = createMockIssue();
      const customColumns: ColumnDef<Issue>[] = [
        {
          id: 'custom_field',
          header: 'Custom',
          accessor: () => 'custom value',
        },
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const cell = container.querySelector('[data-column="custom_field"]');
      expect(cell).toHaveTextContent('custom value');
    });

    it('renders null/undefined custom accessor value as "-"', () => {
      const issue = createMockIssue();
      const customColumns: ColumnDef<Issue>[] = [
        {
          id: 'nullable',
          header: 'Nullable',
          accessor: () => null,
        },
      ];
      const { container } = renderIssueRow({
        issue,
        columns: customColumns,
      });

      const cell = container.querySelector('[data-column="nullable"]');
      expect(cell).toHaveTextContent('-');
    });
  });

  // ============= Combined State Tests =============

  describe('combined states', () => {
    it('can be both selected and clickable', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: true,
        isClickable: true,
        onClick: vi.fn(),
      });

      const row = container.querySelector('tr');
      expect(row).toHaveClass('issue-table__row--selected');
      expect(row).toHaveClass('issue-table__row--clickable');
    });

    it('preserves all classes including custom className', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        isSelected: true,
        isClickable: true,
        className: 'extra-class another-class',
        onClick: vi.fn(),
      });

      const row = container.querySelector('tr');
      expect(row).toHaveClass('issue-table__row');
      expect(row).toHaveClass('issue-table__row--selected');
      expect(row).toHaveClass('issue-table__row--clickable');
      expect(row).toHaveClass('extra-class');
      expect(row).toHaveClass('another-class');
    });
  });

  // ============= Edge Cases =============

  describe('edge cases', () => {
    it('handles empty column array', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: [],
      });

      const cells = container.querySelectorAll('td');
      expect(cells).toHaveLength(0);
      expect(container.querySelector('tr')).toBeInTheDocument();
    });

    it('handles issue with all optional fields missing', () => {
      const minimalIssue: Issue = {
        id: 'bd-minimal',
        title: 'Minimal',
        priority: 0,
        created_at: '2024-01-01T00:00:00Z',
        updated_at: '2024-01-01T00:00:00Z',
      };
      const { container } = renderIssueRow({
        issue: minimalIssue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      // Should render without errors
      expect(container.querySelector('tr')).toBeInTheDocument();
      expect(container.querySelector('[data-column="status"]')).toHaveTextContent('-');
      expect(container.querySelector('[data-column="assignee"]')).toHaveTextContent('-');
      expect(container.querySelector('[data-column="issue_type"]')).toHaveTextContent('-');
    });

    it('handles very long issue IDs', () => {
      const longId = 'bd-' + 'a'.repeat(100);
      const issue = createMockIssue({ id: longId });
      renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      expect(screen.getByTestId(`issue-row-${longId}`)).toBeInTheDocument();
    });

    it('handles special characters in title', () => {
      const specialTitle = '<script>alert("xss")</script> & "quotes" \'apostrophe\'';
      const issue = createMockIssue({ title: specialTitle });
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      const titleCell = container.querySelector('[data-column="title"]');
      // React should escape HTML entities
      expect(titleCell).toHaveTextContent(specialTitle);
    });
  });

  // ============= Checkbox Tests =============

  describe('checkbox functionality', () => {
    it('does not render checkbox when showCheckbox is false', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: false,
      });

      expect(container.querySelector('.issue-table__checkbox')).not.toBeInTheDocument();
    });

    it('does not render checkbox when showCheckbox is undefined', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
      });

      expect(container.querySelector('.issue-table__checkbox')).not.toBeInTheDocument();
    });

    it('renders checkbox when showCheckbox is true', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
      });

      expect(container.querySelector('.issue-table__checkbox')).toBeInTheDocument();
    });

    it('checkbox is checked when isSelected is true', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        isSelected: true,
      });

      const checkbox = container.querySelector('.issue-table__checkbox') as HTMLInputElement;
      expect(checkbox.checked).toBe(true);
    });

    it('checkbox is unchecked when isSelected is false', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        isSelected: false,
      });

      const checkbox = container.querySelector('.issue-table__checkbox') as HTMLInputElement;
      expect(checkbox.checked).toBe(false);
    });

    it('onSelectionChange called with issue id and true when checked', () => {
      const issue = createMockIssue({ id: 'bd-check-test' });
      const handleSelectionChange = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        isSelected: false,
        onSelectionChange: handleSelectionChange,
      });

      const checkbox = container.querySelector('.issue-table__checkbox') as HTMLInputElement;
      fireEvent.click(checkbox);

      expect(handleSelectionChange).toHaveBeenCalledTimes(1);
      expect(handleSelectionChange).toHaveBeenCalledWith('bd-check-test', true);
    });

    it('onSelectionChange called with issue id and false when unchecked', () => {
      const issue = createMockIssue({ id: 'bd-uncheck-test' });
      const handleSelectionChange = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        isSelected: true,
        onSelectionChange: handleSelectionChange,
      });

      const checkbox = container.querySelector('.issue-table__checkbox') as HTMLInputElement;
      fireEvent.click(checkbox);

      expect(handleSelectionChange).toHaveBeenCalledTimes(1);
      expect(handleSelectionChange).toHaveBeenCalledWith('bd-uncheck-test', false);
    });

    it('checkbox click does not trigger row onClick', () => {
      const issue = createMockIssue();
      const handleClick = vi.fn();
      const handleSelectionChange = vi.fn();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        isClickable: true,
        onClick: handleClick,
        onSelectionChange: handleSelectionChange,
      });

      // Click the checkbox cell
      const checkboxCell = container.querySelector('.issue-table__cell--checkbox');
      fireEvent.click(checkboxCell!);

      expect(handleClick).not.toHaveBeenCalled();
    });

    it('checkbox has correct aria-label', () => {
      const issue = createMockIssue({ id: 'bd-aria-test' });
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
      });

      const checkbox = container.querySelector('.issue-table__checkbox');
      expect(checkbox).toHaveAttribute('aria-label', 'Select issue bd-aria-test');
    });

    it('checkbox cell is first in row before other columns', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
      });

      const cells = container.querySelectorAll('td');
      expect(cells[0]).toHaveClass('issue-table__cell--checkbox');
    });

    it('total cells equals columns + 1 when checkbox shown', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
      });

      const cells = container.querySelectorAll('td');
      expect(cells).toHaveLength(DEFAULT_ISSUE_COLUMNS.length + 1);
    });

    it('checkbox renders without onSelectionChange (optional chaining)', () => {
      const issue = createMockIssue();
      const { container } = renderIssueRow({
        issue,
        columns: DEFAULT_ISSUE_COLUMNS,
        showCheckbox: true,
        // onSelectionChange not provided
      });

      const checkbox = container.querySelector('.issue-table__checkbox') as HTMLInputElement;
      // Should not throw when clicking
      fireEvent.click(checkbox);
      expect(checkbox).toBeInTheDocument();
    });
  });
});
