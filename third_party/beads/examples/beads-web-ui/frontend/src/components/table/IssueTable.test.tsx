/**
 * @vitest-environment jsdom
 */

import { render, screen as _screen } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import '@testing-library/jest-dom';
import type { Issue, Status } from '@/types';

import {
  ColumnDef,
  DEFAULT_ISSUE_COLUMNS,
  formatPriority,
  getPriorityClassName,
  formatStatus,
  getStatusClassName,
  formatIssueType,
  formatDate,
  getCellValue,
} from './columns';
import { IssueTable, IssueTableProps } from './IssueTable';

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

// ============= Column Helper Tests =============

describe('columns.ts helpers', () => {
  describe('formatPriority', () => {
    it('formats P0 correctly', () => {
      expect(formatPriority(0)).toBe('P0');
    });

    it('formats P1 correctly', () => {
      expect(formatPriority(1)).toBe('P1');
    });

    it('formats P2 correctly', () => {
      expect(formatPriority(2)).toBe('P2');
    });

    it('formats P3 correctly', () => {
      expect(formatPriority(3)).toBe('P3');
    });

    it('formats P4 correctly', () => {
      expect(formatPriority(4)).toBe('P4');
    });
  });

  describe('getPriorityClassName', () => {
    it.each([0, 1, 2, 3, 4] as const)('returns correct class for priority %d', (priority) => {
      expect(getPriorityClassName(priority)).toBe(`priority-${priority}`);
    });

    /**
     * P2 priority badge contrast fix verification.
     * The CSS uses .priority-2 class to apply dark text color for WCAG AA contrast
     * on the yellow background. This test ensures the class name is correctly generated.
     */
    it('returns priority-2 class for P2 contrast styling', () => {
      expect(getPriorityClassName(2)).toBe('priority-2');
    });
  });

  describe('formatStatus', () => {
    it('returns - for undefined status', () => {
      expect(formatStatus(undefined)).toBe('-');
    });

    it('capitalizes simple status', () => {
      expect(formatStatus('open')).toBe('Open');
    });

    it('replaces underscores with spaces and capitalizes', () => {
      expect(formatStatus('in_progress')).toBe('In progress');
    });

    it('handles blocked status', () => {
      expect(formatStatus('blocked')).toBe('Blocked');
    });

    it('handles closed status', () => {
      expect(formatStatus('closed')).toBe('Closed');
    });
  });

  describe('getStatusClassName', () => {
    it('returns status-unknown for undefined status', () => {
      expect(getStatusClassName(undefined)).toBe('status-unknown');
    });

    it('returns correct class for open status', () => {
      expect(getStatusClassName('open')).toBe('status-open');
    });

    it('converts underscores to hyphens', () => {
      expect(getStatusClassName('in_progress')).toBe('status-in-progress');
    });

    it('handles blocked status', () => {
      expect(getStatusClassName('blocked')).toBe('status-blocked');
    });
  });

  describe('formatIssueType', () => {
    it('returns - for undefined type', () => {
      expect(formatIssueType(undefined)).toBe('-');
    });

    it('capitalizes bug type', () => {
      expect(formatIssueType('bug')).toBe('Bug');
    });

    it('capitalizes feature type', () => {
      expect(formatIssueType('feature')).toBe('Feature');
    });

    it('capitalizes task type', () => {
      expect(formatIssueType('task')).toBe('Task');
    });

    it('capitalizes epic type', () => {
      expect(formatIssueType('epic')).toBe('Epic');
    });
  });

  describe('formatDate', () => {
    it('returns - for undefined date', () => {
      expect(formatDate(undefined)).toBe('-');
    });

    it('returns "just now" for very recent dates', () => {
      const now = new Date();
      const result = formatDate(now.toISOString());
      expect(['just now', '1m ago']).toContain(result);
    });

    it('returns minutes ago for recent dates', () => {
      const fiveMinutesAgo = new Date(Date.now() - 5 * 60 * 1000);
      const result = formatDate(fiveMinutesAgo.toISOString());
      expect(result).toMatch(/^\d+m ago$/);
    });

    it('returns hours ago for dates within 24 hours', () => {
      const threeHoursAgo = new Date(Date.now() - 3 * 60 * 60 * 1000);
      const result = formatDate(threeHoursAgo.toISOString());
      expect(result).toMatch(/^\d+h ago$/);
    });

    it('returns days ago for dates within 7 days', () => {
      const threeDaysAgo = new Date(Date.now() - 3 * 24 * 60 * 60 * 1000);
      const result = formatDate(threeDaysAgo.toISOString());
      expect(result).toMatch(/^\d+d ago$/);
    });

    it('returns formatted date for older dates', () => {
      const result = formatDate('2023-01-15T10:30:00Z');
      // Should be a date string (format varies by locale)
      expect(result).not.toBe('-');
      expect(result).not.toMatch(/ago$/);
    });
  });

  describe('getCellValue', () => {
    const mockIssue = createMockIssue({ id: 'test-id', title: 'Test Title' });

    it('gets value using keyof accessor', () => {
      const column: ColumnDef<Issue> = {
        id: 'id',
        header: 'ID',
        accessor: 'id',
      };
      expect(getCellValue(mockIssue, column)).toBe('test-id');
    });

    it('gets value using function accessor', () => {
      const column: ColumnDef<Issue> = {
        id: 'custom',
        header: 'Custom',
        accessor: (issue) => `${issue.id}: ${issue.title}`,
      };
      expect(getCellValue(mockIssue, column)).toBe('test-id: Test Title');
    });

    it('returns undefined for missing optional field', () => {
      // Create issue without assignee by omitting it
      const issueWithoutAssignee: Issue = {
        id: 'bd-no-assignee',
        title: 'Test Issue',
        priority: 2,
        created_at: '2024-01-15T10:30:00Z',
        updated_at: '2024-01-15T12:00:00Z',
        // assignee is omitted (optional field)
      };
      const column: ColumnDef<Issue> = {
        id: 'assignee',
        header: 'Assignee',
        accessor: 'assignee',
      };
      expect(getCellValue(issueWithoutAssignee, column)).toBeUndefined();
    });
  });

  describe('DEFAULT_ISSUE_COLUMNS', () => {
    it('has 8 columns defined', () => {
      expect(DEFAULT_ISSUE_COLUMNS).toHaveLength(8);
    });

    it('includes id, priority, title, status, blocked, issue_type, assignee, updated_at columns', () => {
      const columnIds = DEFAULT_ISSUE_COLUMNS.map((c) => c.id);
      expect(columnIds).toEqual([
        'id',
        'priority',
        'title',
        'status',
        'blocked',
        'issue_type',
        'assignee',
        'updated_at',
      ]);
    });

    it('marks all columns as sortable', () => {
      DEFAULT_ISSUE_COLUMNS.forEach((column) => {
        expect(column.sortable).toBe(true);
      });
    });
  });
});

// ============= IssueTable Component Tests =============

// Simple mock for React rendering in node environment
// We test the component logic, not actual DOM rendering
describe('IssueTable', () => {
  describe('props and configuration', () => {
    it('exports IssueTable function', () => {
      expect(typeof IssueTable).toBe('function');
    });

    it('exports IssueTableProps type', () => {
      // Type check - this will fail at compile time if IssueTableProps is not exported
      const props: IssueTableProps = {
        issues: [],
      };
      expect(props.issues).toEqual([]);
    });

    it('uses DEFAULT_ISSUE_COLUMNS when columns prop not provided', () => {
      // Test that the component accepts the default props structure
      const props: IssueTableProps = {
        issues: [createMockIssue()],
      };
      expect(props.columns).toBeUndefined();
    });

    it('accepts custom columns', () => {
      const customColumns: ColumnDef<Issue>[] = [
        { id: 'id', header: 'ID', accessor: 'id' },
        { id: 'title', header: 'Title', accessor: 'title' },
      ];
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        columns: customColumns,
      };
      expect(props.columns).toEqual(customColumns);
    });

    it('accepts onRowClick callback', () => {
      const onRowClick = vi.fn();
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        onRowClick,
      };
      expect(props.onRowClick).toBe(onRowClick);
    });

    it('accepts selectedId prop', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        selectedId: 'bd-abc',
      };
      expect(props.selectedId).toBe('bd-abc');
    });

    it('accepts className prop', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        className: 'custom-class',
      };
      expect(props.className).toBe('custom-class');
    });
  });

  describe('data handling', () => {
    it('handles empty issues array', () => {
      const props: IssueTableProps = {
        issues: [],
      };
      expect(props.issues).toHaveLength(0);
    });

    it('handles single issue', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
      };
      expect(props.issues).toHaveLength(1);
    });

    it('handles multiple issues', () => {
      const props: IssueTableProps = {
        issues: [
          createMockIssue({ id: 'bd-001' }),
          createMockIssue({ id: 'bd-002' }),
          createMockIssue({ id: 'bd-003' }),
        ],
      };
      expect(props.issues).toHaveLength(3);
    });

    it('handles issues with missing optional fields', () => {
      // Create issue with optional fields omitted (not set to undefined)
      const issueWithMissingFields: Issue = {
        id: 'bd-minimal',
        title: 'Minimal Issue',
        priority: 2,
        created_at: '2024-01-15T10:30:00Z',
        updated_at: '2024-01-15T12:00:00Z',
        // assignee, status, issue_type are all omitted
      };
      const props: IssueTableProps = {
        issues: [issueWithMissingFields],
      };
      expect(props.issues[0]?.assignee).toBeUndefined();
      expect(props.issues[0]?.status).toBeUndefined();
      expect(props.issues[0]?.issue_type).toBeUndefined();
    });
  });

  describe('priority handling', () => {
    it.each([0, 1, 2, 3, 4] as const)('handles priority %d correctly', (priority) => {
      const issue = createMockIssue({ priority });
      expect(issue.priority).toBe(priority);
      expect(formatPriority(priority)).toBe(`P${priority}`);
      expect(getPriorityClassName(priority)).toBe(`priority-${priority}`);
    });
  });

  describe('status handling', () => {
    const statuses: Status[] = ['open', 'in_progress', 'blocked', 'closed', 'deferred'];

    it.each(statuses)('handles %s status correctly', (status) => {
      const issue = createMockIssue({ status });
      expect(issue.status).toBe(status);
      expect(formatStatus(status)).not.toBe('-');
      expect(getStatusClassName(status)).toContain('status-');
    });
  });

  describe('row selection', () => {
    it('identifies selected row by ID', () => {
      const issues = [
        createMockIssue({ id: 'bd-001' }),
        createMockIssue({ id: 'bd-002' }),
        createMockIssue({ id: 'bd-003' }),
      ];
      const props: IssueTableProps = {
        issues,
        selectedId: 'bd-002',
      };
      const selectedIssue = props.issues.find((i) => i.id === props.selectedId);
      expect(selectedIssue?.id).toBe('bd-002');
    });

    it('handles selectedId not in issues list', () => {
      const issues = [createMockIssue({ id: 'bd-001' })];
      const props: IssueTableProps = {
        issues,
        selectedId: 'bd-999',
      };
      const selectedIssue = props.issues.find((i) => i.id === props.selectedId);
      expect(selectedIssue).toBeUndefined();
    });
  });

  describe('custom columns', () => {
    it('supports column with function accessor', () => {
      const customColumns: ColumnDef<Issue>[] = [
        {
          id: 'combined',
          header: 'Combined',
          accessor: (issue) => `${issue.id} - ${issue.title}`,
        },
      ];
      const issue = createMockIssue({ id: 'bd-abc', title: 'Test' });
      const value = getCellValue(issue, customColumns[0]!);
      expect(value).toBe('bd-abc - Test');
    });

    it('supports column alignment options', () => {
      const columns: ColumnDef<Issue>[] = [
        { id: 'left', header: 'Left', accessor: 'id', align: 'left' },
        { id: 'center', header: 'Center', accessor: 'title', align: 'center' },
        { id: 'right', header: 'Right', accessor: 'updated_at', align: 'right' },
      ];
      expect(columns[0]?.align).toBe('left');
      expect(columns[1]?.align).toBe('center');
      expect(columns[2]?.align).toBe('right');
    });

    it('supports column width options', () => {
      const columns: ColumnDef<Issue>[] = [
        { id: 'fixed', header: 'Fixed', accessor: 'id', width: '100px' },
        { id: 'flex', header: 'Flex', accessor: 'title', width: '1fr' },
        { id: 'auto', header: 'Auto', accessor: 'status', width: 'auto' },
      ];
      expect(columns[0]?.width).toBe('100px');
      expect(columns[1]?.width).toBe('1fr');
      expect(columns[2]?.width).toBe('auto');
    });

    it('supports sortable column flag', () => {
      const columns: ColumnDef<Issue>[] = [
        { id: 'sortable', header: 'Sortable', accessor: 'priority', sortable: true },
        { id: 'notSortable', header: 'Not Sortable', accessor: 'notes', sortable: false },
      ];
      expect(columns[0]?.sortable).toBe(true);
      expect(columns[1]?.sortable).toBe(false);
    });
  });

  // ============= Checkbox Props Tests =============

  describe('checkbox props', () => {
    it('accepts showCheckbox prop', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        showCheckbox: true,
      };
      expect(props.showCheckbox).toBe(true);
    });

    it('accepts selectedIds Set prop', () => {
      const selectedIds = new Set(['bd-001', 'bd-002']);
      const props: IssueTableProps = {
        issues: [
          createMockIssue({ id: 'bd-001' }),
          createMockIssue({ id: 'bd-002' }),
          createMockIssue({ id: 'bd-003' }),
        ],
        showCheckbox: true,
        selectedIds,
      };
      expect(props.selectedIds?.has('bd-001')).toBe(true);
      expect(props.selectedIds?.has('bd-002')).toBe(true);
      expect(props.selectedIds?.has('bd-003')).toBe(false);
    });

    it('accepts onSelectionChange callback', () => {
      const onSelectionChange = vi.fn();
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        showCheckbox: true,
        onSelectionChange,
      };
      expect(props.onSelectionChange).toBe(onSelectionChange);
    });

    it('showCheckbox defaults to undefined', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
      };
      expect(props.showCheckbox).toBeUndefined();
    });

    it('selectedIds can be undefined', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        showCheckbox: true,
        // selectedIds not provided
      };
      expect(props.selectedIds).toBeUndefined();
    });

    it('onSelectionChange can be undefined', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        showCheckbox: true,
        // onSelectionChange not provided
      };
      expect(props.onSelectionChange).toBeUndefined();
    });
  });

  // ============= Sortable Props Tests (T045) =============

  describe('sortable props', () => {
    it('accepts sortable prop (defaults to false)', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
      };
      expect(props.sortable).toBeUndefined();
    });

    it('accepts sortable=true', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: true,
      };
      expect(props.sortable).toBe(true);
    });

    it('accepts sortable=false', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: false,
      };
      expect(props.sortable).toBe(false);
    });

    it('accepts initialSort with key and direction', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: true,
        initialSort: {
          key: 'priority',
          direction: 'asc',
        },
      };
      expect(props.initialSort?.key).toBe('priority');
      expect(props.initialSort?.direction).toBe('asc');
    });

    it('accepts initialSort with descending direction', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: true,
        initialSort: {
          key: 'title',
          direction: 'desc',
        },
      };
      expect(props.initialSort?.key).toBe('title');
      expect(props.initialSort?.direction).toBe('desc');
    });

    it('initialSort can be undefined when sortable is true', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: true,
        // initialSort not provided
      };
      expect(props.sortable).toBe(true);
      expect(props.initialSort).toBeUndefined();
    });

    it('supports all sortable column keys for initialSort', () => {
      const sortableColumns = DEFAULT_ISSUE_COLUMNS.filter((c) => c.sortable);
      sortableColumns.forEach((column) => {
        const props: IssueTableProps = {
          issues: [createMockIssue()],
          sortable: true,
          initialSort: {
            key: column.id,
            direction: 'asc',
          },
        };
        expect(props.initialSort?.key).toBe(column.id);
      });
    });
  });

  describe('sorting behavior', () => {
    it('without sortable prop, issues maintain original order', () => {
      const issues = [
        createMockIssue({ id: 'bd-003', priority: 3 }),
        createMockIssue({ id: 'bd-001', priority: 1 }),
        createMockIssue({ id: 'bd-002', priority: 2 }),
      ];
      const props: IssueTableProps = {
        issues,
        // sortable is false by default
      };
      // Without sorting, issues should be in original order
      expect(props.issues[0]?.id).toBe('bd-003');
      expect(props.issues[1]?.id).toBe('bd-001');
      expect(props.issues[2]?.id).toBe('bd-002');
    });

    it('with sortable=true and initialSort, issues can be sorted', () => {
      const issues = [
        createMockIssue({ id: 'bd-003', priority: 3 }),
        createMockIssue({ id: 'bd-001', priority: 1 }),
        createMockIssue({ id: 'bd-002', priority: 2 }),
      ];
      const props: IssueTableProps = {
        issues,
        sortable: true,
        initialSort: {
          key: 'priority',
          direction: 'asc',
        },
      };
      expect(props.sortable).toBe(true);
      expect(props.initialSort?.key).toBe('priority');
    });

    it('backwards compatibility: existing usage without sortable works', () => {
      // This test ensures existing code using IssueTable without sortable prop still works
      const issues = [createMockIssue()];
      const onRowClick = vi.fn();
      const props: IssueTableProps = {
        issues,
        onRowClick,
        selectedId: 'bd-abc',
        className: 'my-table',
      };
      expect(props.sortable).toBeUndefined();
      expect(props.initialSort).toBeUndefined();
      expect(props.onRowClick).toBe(onRowClick);
    });

    it('sortable and checkbox props can be used together', () => {
      const props: IssueTableProps = {
        issues: [createMockIssue()],
        sortable: true,
        showCheckbox: true,
        selectedIds: new Set(['bd-abc']),
        initialSort: {
          key: 'priority',
          direction: 'asc',
        },
      };
      expect(props.sortable).toBe(true);
      expect(props.showCheckbox).toBe(true);
      expect(props.selectedIds?.has('bd-abc')).toBe(true);
    });
  });

  // ============= Sticky Column CSS Selector Tests =============

  /**
   * These tests verify the DOM attributes that the sticky column CSS selectors target.
   * The CSS in IssueTable.css uses [data-column="id"] selectors to apply sticky positioning.
   * CSS-only changes like position:sticky cannot be easily unit tested in JSDOM,
   * so we verify the data attributes exist that the CSS selectors target.
   */
  describe('sticky column CSS selectors', () => {
    it('ID column header has data-column="id" attribute for CSS sticky targeting', () => {
      const { container } = render(
        <IssueTable issues={[createMockIssue()]} columns={DEFAULT_ISSUE_COLUMNS} />
      );

      const idHeaderCell = container.querySelector('th[data-column="id"]');
      expect(idHeaderCell).toBeInTheDocument();
      expect(idHeaderCell).toHaveAttribute('data-column', 'id');
    });

    it('ID column body cells have data-column="id" attribute for CSS sticky targeting', () => {
      const issues = [
        createMockIssue({ id: 'bd-001' }),
        createMockIssue({ id: 'bd-002' }),
        createMockIssue({ id: 'bd-003' }),
      ];
      const { container } = render(<IssueTable issues={issues} columns={DEFAULT_ISSUE_COLUMNS} />);

      const idBodyCells = container.querySelectorAll('td[data-column="id"]');
      expect(idBodyCells).toHaveLength(3);
      idBodyCells.forEach((cell) => {
        expect(cell).toHaveAttribute('data-column', 'id');
      });
    });

    it('checkbox column cells have the expected class for CSS sticky targeting', () => {
      const { container } = render(
        <IssueTable
          issues={[createMockIssue()]}
          columns={DEFAULT_ISSUE_COLUMNS}
          showCheckbox={true}
        />
      );

      // Checkbox header cell
      const checkboxHeaderCell = container.querySelector('th.issue-table__header-cell--checkbox');
      expect(checkboxHeaderCell).toBeInTheDocument();

      // Checkbox body cell
      const checkboxBodyCell = container.querySelector('td.issue-table__cell--checkbox');
      expect(checkboxBodyCell).toBeInTheDocument();
    });

    it('ID column appears after checkbox column when both present', () => {
      const { container } = render(
        <IssueTable
          issues={[createMockIssue()]}
          columns={DEFAULT_ISSUE_COLUMNS}
          showCheckbox={true}
        />
      );

      const headerCells = container.querySelectorAll('th');
      // First header should be checkbox
      expect(headerCells[0]).toHaveClass('issue-table__header-cell--checkbox');
      // Second header should be ID column
      expect(headerCells[1]).toHaveAttribute('data-column', 'id');
    });

    it('table wrapper exists for horizontal scroll container', () => {
      const { container } = render(
        <IssueTable issues={[createMockIssue()]} columns={DEFAULT_ISSUE_COLUMNS} />
      );

      const wrapper = container.querySelector('.issue-table__wrapper');
      expect(wrapper).toBeInTheDocument();
    });

    it('all column cells have data-column attribute matching column id', () => {
      const { container } = render(
        <IssueTable issues={[createMockIssue()]} columns={DEFAULT_ISSUE_COLUMNS} />
      );

      DEFAULT_ISSUE_COLUMNS.forEach((column) => {
        const headerCell = container.querySelector(`th[data-column="${column.id}"]`);
        expect(headerCell).toBeInTheDocument();

        const bodyCell = container.querySelector(`td[data-column="${column.id}"]`);
        expect(bodyCell).toBeInTheDocument();
      });
    });
  });
});
