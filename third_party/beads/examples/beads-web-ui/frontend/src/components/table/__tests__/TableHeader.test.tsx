/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for TableHeader component.
 * Tests rendering, sort indicators, click handling, keyboard navigation,
 * and accessibility attributes.
 */

import { render, screen, fireEvent } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';
import '@testing-library/jest-dom';

import { ColumnDef } from '../columns';
import { TableHeader, SortState, SortDirection } from '../TableHeader';

// Helper type for test columns
interface TestItem {
  id: string;
  name: string;
  value: number;
}

// Helper to create sortable test columns
function createTestColumns(overrides: Partial<ColumnDef<TestItem>>[] = []): ColumnDef<TestItem>[] {
  const defaults: ColumnDef<TestItem>[] = [
    { id: 'id', header: 'ID', accessor: 'id', sortable: true },
    { id: 'name', header: 'Name', accessor: 'name', sortable: true },
    { id: 'value', header: 'Value', accessor: 'value', sortable: false },
  ];
  return defaults.map((col, i) => ({ ...col, ...overrides[i] }));
}

// Helper to render TableHeader wrapped in a table
function renderTableHeader(
  columns: ColumnDef<TestItem>[],
  sortState: SortState,
  onSort: (columnId: string) => void
) {
  return render(
    <table>
      <TableHeader columns={columns} sortState={sortState} onSort={onSort} />
    </table>
  );
}

// ============= Rendering Tests =============

describe('TableHeader', () => {
  describe('rendering', () => {
    it('renders thead element', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const thead = container.querySelector('thead');
      expect(thead).toBeInTheDocument();
      expect(thead).toHaveClass('issue-table__head');
    });

    it('renders all column headers', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const headerCells = container.querySelectorAll('th');
      expect(headerCells).toHaveLength(3);
    });

    it('renders header text correctly', () => {
      const columns = createTestColumns();
      renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      expect(screen.getByText('ID')).toBeInTheDocument();
      expect(screen.getByText('Name')).toBeInTheDocument();
      expect(screen.getByText('Value')).toBeInTheDocument();
    });

    it('renders with correct data-column attributes', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      expect(container.querySelector('[data-column="id"]')).toBeInTheDocument();
      expect(container.querySelector('[data-column="name"]')).toBeInTheDocument();
      expect(container.querySelector('[data-column="value"]')).toBeInTheDocument();
    });

    it('applies correct width from column definition', () => {
      const columns: ColumnDef<TestItem>[] = [
        { id: 'id', header: 'ID', accessor: 'id', width: '100px' },
        { id: 'name', header: 'Name', accessor: 'name', width: '1fr' },
      ];
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const idCell = container.querySelector('[data-column="id"]');
      const nameCell = container.querySelector('[data-column="name"]');

      expect(idCell).toHaveStyle({ width: '100px' });
      expect(nameCell).toHaveStyle({ width: '1fr' });
    });

    it('applies correct text alignment from column definition', () => {
      const columns: ColumnDef<TestItem>[] = [
        { id: 'id', header: 'ID', accessor: 'id', align: 'left' },
        { id: 'name', header: 'Name', accessor: 'name', align: 'center' },
        { id: 'value', header: 'Value', accessor: 'value', align: 'right' },
      ];
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      expect(container.querySelector('[data-column="id"]')).toHaveStyle({
        textAlign: 'left',
      });
      expect(container.querySelector('[data-column="name"]')).toHaveStyle({
        textAlign: 'center',
      });
      expect(container.querySelector('[data-column="value"]')).toHaveStyle({
        textAlign: 'right',
      });
    });

    it('defaults to left alignment when not specified', () => {
      const columns: ColumnDef<TestItem>[] = [{ id: 'id', header: 'ID', accessor: 'id' }];
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      expect(container.querySelector('[data-column="id"]')).toHaveStyle({
        textAlign: 'left',
      });
    });

    it('handles empty columns array', () => {
      const { container } = renderTableHeader([], { key: null, direction: 'asc' }, vi.fn());

      const thead = container.querySelector('thead');
      expect(thead).toBeInTheDocument();
      expect(container.querySelectorAll('th')).toHaveLength(0);
    });
  });

  // ============= Sort Indicator Tests =============

  describe('sort indicators', () => {
    it('shows ascending indicator when sorted ascending', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      const indicator = idHeader?.querySelector('.issue-table__sort-indicator');

      expect(indicator).toHaveClass('issue-table__sort-indicator--asc');
      expect(indicator).toHaveTextContent('▲');
    });

    it('shows descending indicator when sorted descending', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'desc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      const indicator = idHeader?.querySelector('.issue-table__sort-indicator');

      expect(indicator).toHaveClass('issue-table__sort-indicator--desc');
      expect(indicator).toHaveTextContent('▼');
    });

    it('shows neutral indicator for unsorted sortable columns', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      // Name column is sortable but not currently sorted
      const nameHeader = container.querySelector('[data-column="name"]');
      const indicator = nameHeader?.querySelector('.issue-table__sort-indicator');

      expect(indicator).not.toHaveClass('issue-table__sort-indicator--asc');
      expect(indicator).not.toHaveClass('issue-table__sort-indicator--desc');
      expect(indicator).toHaveTextContent('↕');
    });

    it('does not show indicator for non-sortable columns', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      // Value column is not sortable
      const valueHeader = container.querySelector('[data-column="value"]');
      const indicator = valueHeader?.querySelector('.issue-table__sort-indicator');

      expect(indicator).not.toBeInTheDocument();
    });

    it('no indicator has active class when sortState.key does not match any column', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(
        columns,
        { key: 'unknown', direction: 'asc' },
        vi.fn()
      );

      const indicators = container.querySelectorAll('.issue-table__sort-indicator');
      indicators.forEach((indicator) => {
        expect(indicator).not.toHaveClass('issue-table__sort-indicator--asc');
        expect(indicator).not.toHaveClass('issue-table__sort-indicator--desc');
      });
    });

    it('indicator has aria-hidden="true"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const indicator = container.querySelector('.issue-table__sort-indicator');
      expect(indicator).toHaveAttribute('aria-hidden', 'true');
    });
  });

  // ============= Click Handling Tests =============

  describe('click handling', () => {
    it('sortable header has cursor pointer class', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveClass('issue-table__header-cell--sortable');
    });

    it('non-sortable header does not have cursor pointer class', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const valueHeader = container.querySelector('[data-column="value"]');
      expect(valueHeader).not.toHaveClass('issue-table__header-cell--sortable');
    });

    it('click on sortable header calls onSort with columnId', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.click(idHeader!);

      expect(handleSort).toHaveBeenCalledTimes(1);
      expect(handleSort).toHaveBeenCalledWith('id');
    });

    it('click on non-sortable header does not call onSort', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const valueHeader = container.querySelector('[data-column="value"]');
      fireEvent.click(valueHeader!);

      expect(handleSort).not.toHaveBeenCalled();
    });

    it('sorted header has sorted class', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveClass('issue-table__header-cell--sorted');
    });

    it('unsorted header does not have sorted class', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const nameHeader = container.querySelector('[data-column="name"]');
      expect(nameHeader).not.toHaveClass('issue-table__header-cell--sorted');
    });
  });

  // ============= Keyboard Navigation Tests =============

  describe('keyboard navigation', () => {
    it('Enter key triggers onSort for sortable column', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.keyDown(idHeader!, { key: 'Enter' });

      expect(handleSort).toHaveBeenCalledTimes(1);
      expect(handleSort).toHaveBeenCalledWith('id');
    });

    it('Space key triggers onSort for sortable column', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.keyDown(idHeader!, { key: ' ' });

      expect(handleSort).toHaveBeenCalledTimes(1);
      expect(handleSort).toHaveBeenCalledWith('id');
    });

    it('other keys do not trigger onSort', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.keyDown(idHeader!, { key: 'Tab' });
      fireEvent.keyDown(idHeader!, { key: 'Escape' });
      fireEvent.keyDown(idHeader!, { key: 'ArrowDown' });
      fireEvent.keyDown(idHeader!, { key: 'a' });

      expect(handleSort).not.toHaveBeenCalled();
    });

    it('keyboard events on non-sortable column do not trigger onSort', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const valueHeader = container.querySelector('[data-column="value"]');
      fireEvent.keyDown(valueHeader!, { key: 'Enter' });
      fireEvent.keyDown(valueHeader!, { key: ' ' });

      expect(handleSort).not.toHaveBeenCalled();
    });

    it('sortable headers have tabIndex 0', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      const nameHeader = container.querySelector('[data-column="name"]');

      expect(idHeader).toHaveAttribute('tabIndex', '0');
      expect(nameHeader).toHaveAttribute('tabIndex', '0');
    });

    it('non-sortable headers do not have tabIndex', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const valueHeader = container.querySelector('[data-column="value"]');
      expect(valueHeader).not.toHaveAttribute('tabIndex');
    });
  });

  // ============= Accessibility Tests =============

  describe('accessibility', () => {
    it('sorted ascending column has aria-sort="ascending"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveAttribute('aria-sort', 'ascending');
    });

    it('sorted descending column has aria-sort="descending"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'desc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveAttribute('aria-sort', 'descending');
    });

    it('unsorted sortable column has aria-sort="none"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      // name is sortable but not currently sorted
      const nameHeader = container.querySelector('[data-column="name"]');
      expect(nameHeader).toHaveAttribute('aria-sort', 'none');
    });

    it('non-sortable column has no aria-sort', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const valueHeader = container.querySelector('[data-column="value"]');
      expect(valueHeader).not.toHaveAttribute('aria-sort');
    });

    it('sortable headers have role="button"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveAttribute('role', 'button');
    });

    it('non-sortable headers do not have role="button"', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const valueHeader = container.querySelector('[data-column="value"]');
      expect(valueHeader).not.toHaveAttribute('role');
    });

    it('sortable header has aria-label describing sort action', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveAttribute('aria-label', 'Sort by ID');
    });

    it('sorted ascending header has aria-label with current state', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'id', direction: 'asc' }, vi.fn());

      const idHeader = container.querySelector('[data-column="id"]');
      expect(idHeader).toHaveAttribute('aria-label', 'Sort by ID, currently sorted ascending');
    });

    it('sorted descending header has aria-label with current state', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: 'name', direction: 'desc' }, vi.fn());

      const nameHeader = container.querySelector('[data-column="name"]');
      expect(nameHeader).toHaveAttribute('aria-label', 'Sort by Name, currently sorted descending');
    });

    it('non-sortable header has no aria-label', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, vi.fn());

      const valueHeader = container.querySelector('[data-column="value"]');
      expect(valueHeader).not.toHaveAttribute('aria-label');
    });
  });

  // ============= Edge Cases =============

  describe('edge cases', () => {
    it('handles sortState.key not matching any column', () => {
      const columns = createTestColumns();
      const { container } = renderTableHeader(
        columns,
        { key: 'nonexistent', direction: 'asc' },
        vi.fn()
      );

      // Should not crash, no column should be marked as sorted
      const headers = container.querySelectorAll('.issue-table__header-cell');
      headers.forEach((header) => {
        expect(header).not.toHaveClass('issue-table__header-cell--sorted');
      });
    });

    it('handles all columns being non-sortable', () => {
      const columns: ColumnDef<TestItem>[] = [
        { id: 'id', header: 'ID', accessor: 'id', sortable: false },
        { id: 'name', header: 'Name', accessor: 'name', sortable: false },
      ];
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      // Click should not trigger anything
      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.click(idHeader!);

      expect(handleSort).not.toHaveBeenCalled();
      expect(container.querySelectorAll('.issue-table__sort-indicator')).toHaveLength(0);
    });

    it('handles column with undefined sortable (treated as false)', () => {
      const columns: ColumnDef<TestItem>[] = [
        { id: 'id', header: 'ID', accessor: 'id' }, // sortable not specified
      ];
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.click(idHeader!);

      expect(handleSort).not.toHaveBeenCalled();
      expect(idHeader).not.toHaveClass('issue-table__header-cell--sortable');
    });

    it('handles rapid repeated clicks', () => {
      const columns = createTestColumns();
      const handleSort = vi.fn();
      const { container } = renderTableHeader(columns, { key: null, direction: 'asc' }, handleSort);

      const idHeader = container.querySelector('[data-column="id"]');
      fireEvent.click(idHeader!);
      fireEvent.click(idHeader!);
      fireEvent.click(idHeader!);

      expect(handleSort).toHaveBeenCalledTimes(3);
    });
  });

  // ============= Type Export Tests =============

  describe('type exports', () => {
    it('SortDirection type accepts valid values', () => {
      const asc: SortDirection = 'asc';
      const desc: SortDirection = 'desc';
      expect(asc).toBe('asc');
      expect(desc).toBe('desc');
    });

    it('SortState interface has correct shape', () => {
      const state: SortState = { key: 'id', direction: 'asc' };
      expect(state.key).toBe('id');
      expect(state.direction).toBe('asc');

      const nullState: SortState = { key: null, direction: 'desc' };
      expect(nullState.key).toBeNull();
      expect(nullState.direction).toBe('desc');
    });
  });
});
