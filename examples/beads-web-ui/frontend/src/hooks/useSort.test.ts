/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect } from 'vitest';

import type { ColumnDef } from '@/components/table/columns';
import type { Issue } from '@/types';

import { useSort, SortDirection as _SortDirection } from './useSort';

// Test data types
interface SimpleRow {
  id: string;
  name: string;
  value: number;
  date?: string;
  active?: boolean;
}

// Test fixtures
const simpleColumns: ColumnDef<SimpleRow>[] = [
  { id: 'name', header: 'Name', accessor: 'name', sortable: true },
  { id: 'value', header: 'Value', accessor: 'value', sortable: true },
  { id: 'date', header: 'Date', accessor: 'date', sortable: true },
  { id: 'active', header: 'Active', accessor: 'active', sortable: true },
  { id: 'id', header: 'ID', accessor: 'id', sortable: false },
];

const simpleData: SimpleRow[] = [
  { id: '1', name: 'Charlie', value: 30, date: '2024-03-01', active: true },
  { id: '2', name: 'alpha', value: 10, date: '2024-01-15', active: false },
  { id: '3', name: 'Beta', value: 20, date: '2024-02-20', active: true },
];

const issueColumns: ColumnDef<Issue>[] = [
  { id: 'id', header: 'ID', accessor: 'id', sortable: true },
  { id: 'title', header: 'Title', accessor: 'title', sortable: true },
  { id: 'priority', header: 'Priority', accessor: 'priority', sortable: true },
  { id: 'status', header: 'Status', accessor: 'status', sortable: true },
  { id: 'updated_at', header: 'Updated', accessor: 'updated_at', sortable: true },
];

const testIssues: Issue[] = [
  {
    id: 'bd-001',
    title: 'Alpha',
    priority: 2,
    status: 'open',
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-15T00:00:00Z',
  },
  {
    id: 'bd-002',
    title: 'beta',
    priority: 0,
    status: 'in_progress',
    created_at: '2024-01-02T00:00:00Z',
    updated_at: '2024-01-10T00:00:00Z',
  },
  {
    id: 'bd-003',
    title: 'Charlie',
    priority: 1,
    status: 'closed',
    created_at: '2024-01-03T00:00:00Z',
    updated_at: '2024-01-20T00:00:00Z',
  },
];

describe('useSort', () => {
  describe('initial state', () => {
    it('defaults to unsorted when no initialKey provided', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortState.direction).toBe('asc');
      // Data should be in original order
      expect(result.current.sortedData).toEqual(simpleData);
    });

    it('respects initialKey when provided', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
        })
      );

      expect(result.current.sortState.key).toBe('name');
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('respects initialDirection when provided', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
          initialDirection: 'desc',
        })
      );

      expect(result.current.sortState.key).toBe('name');
      expect(result.current.sortState.direction).toBe('desc');
    });

    it('treats invalid initialKey as null', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'nonexistent',
        })
      );

      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortedData).toEqual(simpleData);
    });

    it('treats non-sortable initialKey as null', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'id', // id is not sortable
        })
      );

      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortedData).toEqual(simpleData);
    });
  });

  describe('handleSort behavior', () => {
    it('first click sorts ascending', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      act(() => {
        result.current.handleSort('name');
      });

      expect(result.current.sortState.key).toBe('name');
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('second click sorts descending', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
          initialDirection: 'asc',
        })
      );

      act(() => {
        result.current.handleSort('name');
      });

      expect(result.current.sortState.key).toBe('name');
      expect(result.current.sortState.direction).toBe('desc');
    });

    it('third click clears sort', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
          initialDirection: 'desc',
        })
      );

      act(() => {
        result.current.handleSort('name');
      });

      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('clicking different column switches to that column ascending', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
          initialDirection: 'desc',
        })
      );

      act(() => {
        result.current.handleSort('value');
      });

      expect(result.current.sortState.key).toBe('value');
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('ignores non-sortable column', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
        })
      );

      act(() => {
        result.current.handleSort('id'); // id is not sortable
      });

      // State should remain unchanged
      expect(result.current.sortState.key).toBe('name');
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('returns stable function reference', () => {
      const { result, rerender } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      const handleSort1 = result.current.handleSort;

      rerender();

      const handleSort2 = result.current.handleSort;

      expect(handleSort1).toBe(handleSort2);
    });
  });

  describe('clearSort', () => {
    it('clears sortKey to null', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
          initialDirection: 'desc',
        })
      );

      act(() => {
        result.current.clearSort();
      });

      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortState.direction).toBe('asc');
    });

    it('returns stable function reference', () => {
      const { result, rerender } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      const clearSort1 = result.current.clearSort;

      rerender();

      const clearSort2 = result.current.clearSort;

      expect(clearSort1).toBe(clearSort2);
    });
  });

  describe('sorting correctness', () => {
    describe('string sorting', () => {
      it('sorts strings alphabetically ascending (case-insensitive)', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'name',
            initialDirection: 'asc',
          })
        );

        const names = result.current.sortedData.map((r) => r.name);
        expect(names).toEqual(['alpha', 'Beta', 'Charlie']);
      });

      it('sorts strings alphabetically descending', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'name',
            initialDirection: 'desc',
          })
        );

        const names = result.current.sortedData.map((r) => r.name);
        expect(names).toEqual(['Charlie', 'Beta', 'alpha']);
      });
    });

    describe('number sorting', () => {
      it('sorts numbers ascending', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'value',
            initialDirection: 'asc',
          })
        );

        const values = result.current.sortedData.map((r) => r.value);
        expect(values).toEqual([10, 20, 30]);
      });

      it('sorts numbers descending', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'value',
            initialDirection: 'desc',
          })
        );

        const values = result.current.sortedData.map((r) => r.value);
        expect(values).toEqual([30, 20, 10]);
      });
    });

    describe('date sorting', () => {
      it('sorts date strings chronologically ascending', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'date',
            initialDirection: 'asc',
          })
        );

        const dates = result.current.sortedData.map((r) => r.date);
        expect(dates).toEqual(['2024-01-15', '2024-02-20', '2024-03-01']);
      });

      it('sorts date strings chronologically descending', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'date',
            initialDirection: 'desc',
          })
        );

        const dates = result.current.sortedData.map((r) => r.date);
        expect(dates).toEqual(['2024-03-01', '2024-02-20', '2024-01-15']);
      });

      it('sorts ISO datetime strings', () => {
        const { result } = renderHook(() =>
          useSort({
            data: testIssues,
            columns: issueColumns,
            initialKey: 'updated_at',
            initialDirection: 'asc',
          })
        );

        const dates = result.current.sortedData.map((r) => r.updated_at);
        expect(dates).toEqual([
          '2024-01-10T00:00:00Z',
          '2024-01-15T00:00:00Z',
          '2024-01-20T00:00:00Z',
        ]);
      });
    });

    describe('null/undefined handling', () => {
      it('sorts undefined values to end (ascending)', () => {
        const dataWithUndefined: SimpleRow[] = [
          { id: '1', name: 'Alpha', value: 10 },
          { id: '2', name: 'Beta', value: 20, date: undefined },
          { id: '3', name: 'Charlie', value: 30, date: '2024-01-01' },
        ];

        const { result } = renderHook(() =>
          useSort({
            data: dataWithUndefined,
            columns: simpleColumns,
            initialKey: 'date',
            initialDirection: 'asc',
          })
        );

        const dates = result.current.sortedData.map((r) => r.date);
        // Undefined/missing values go to the end
        expect(dates).toEqual(['2024-01-01', undefined, undefined]);
      });

      it('sorts undefined values to end (descending)', () => {
        const dataWithUndefined: SimpleRow[] = [
          { id: '1', name: 'Alpha', value: 10 },
          { id: '2', name: 'Beta', value: 20, date: '2024-02-01' },
          { id: '3', name: 'Charlie', value: 30, date: '2024-01-01' },
        ];

        const { result } = renderHook(() =>
          useSort({
            data: dataWithUndefined,
            columns: simpleColumns,
            initialKey: 'date',
            initialDirection: 'desc',
          })
        );

        const dates = result.current.sortedData.map((r) => r.date);
        expect(dates).toEqual(['2024-02-01', '2024-01-01', undefined]);
      });

      it('handles all null/undefined values', () => {
        const dataAllUndefined: SimpleRow[] = [
          { id: '1', name: 'Alpha', value: 10 },
          { id: '2', name: 'Beta', value: 20 },
          { id: '3', name: 'Charlie', value: 30 },
        ];

        const { result } = renderHook(() =>
          useSort({
            data: dataAllUndefined,
            columns: simpleColumns,
            initialKey: 'date',
            initialDirection: 'asc',
          })
        );

        // Should maintain original order when all values are undefined
        const ids = result.current.sortedData.map((r) => r.id);
        expect(ids).toEqual(['1', '2', '3']);
      });
    });

    describe('boolean sorting', () => {
      it('sorts booleans (true before false in ascending)', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'active',
            initialDirection: 'asc',
          })
        );

        const actives = result.current.sortedData.map((r) => r.active);
        expect(actives).toEqual([true, true, false]);
      });

      it('sorts booleans (false before true in descending)', () => {
        const { result } = renderHook(() =>
          useSort({
            data: simpleData,
            columns: simpleColumns,
            initialKey: 'active',
            initialDirection: 'desc',
          })
        );

        const actives = result.current.sortedData.map((r) => r.active);
        expect(actives).toEqual([false, true, true]);
      });
    });
  });

  describe('edge cases', () => {
    it('returns empty array for empty data', () => {
      const { result } = renderHook(() =>
        useSort({
          data: [],
          columns: simpleColumns,
          initialKey: 'name',
        })
      );

      expect(result.current.sortedData).toEqual([]);
    });

    it('handles empty columns array', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: [],
        })
      );

      // No sortable columns, so data should be unchanged
      expect(result.current.sortedData).toEqual(simpleData);

      // handleSort should do nothing
      act(() => {
        result.current.handleSort('name');
      });

      expect(result.current.sortState.key).toBeNull();
    });

    it('unsorted state returns original order', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      expect(result.current.sortedData).toEqual(simpleData);
      expect(result.current.sortedData).toBe(simpleData);
    });

    it('handles single item array', () => {
      const singleItem: SimpleRow[] = [{ id: '1', name: 'Only', value: 42 }];

      const { result } = renderHook(() =>
        useSort({
          data: singleItem,
          columns: simpleColumns,
          initialKey: 'name',
        })
      );

      expect(result.current.sortedData).toHaveLength(1);
      expect(result.current.sortedData[0].name).toBe('Only');
    });

    it('does not mutate original data array', () => {
      const originalData = [...simpleData];

      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'value',
        })
      );

      // Sorted data should be different from original
      expect(result.current.sortedData).not.toBe(simpleData);
      // Original data should be unchanged
      expect(simpleData).toEqual(originalData);
    });
  });

  describe('memoization', () => {
    it('returns same array reference when inputs unchanged', () => {
      const { result, rerender } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
          initialKey: 'name',
        })
      );

      const sortedData1 = result.current.sortedData;

      rerender();

      const sortedData2 = result.current.sortedData;

      expect(sortedData1).toBe(sortedData2);
    });

    it('returns new array reference when data changes', () => {
      const { result, rerender } = renderHook(
        ({ data }) =>
          useSort({
            data,
            columns: simpleColumns,
            initialKey: 'name',
          }),
        { initialProps: { data: simpleData } }
      );

      const sortedData1 = result.current.sortedData;

      const newData: SimpleRow[] = [{ id: '4', name: 'Delta', value: 40 }, ...simpleData];

      rerender({ data: newData });

      const sortedData2 = result.current.sortedData;

      expect(sortedData1).not.toBe(sortedData2);
    });

    it('returns new array reference when sort state changes', () => {
      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: simpleColumns,
        })
      );

      const sortedData1 = result.current.sortedData;

      act(() => {
        result.current.handleSort('name');
      });

      const sortedData2 = result.current.sortedData;

      expect(sortedData1).not.toBe(sortedData2);
    });
  });

  describe('accessor tests', () => {
    it('uses keyof accessor correctly', () => {
      const { result } = renderHook(() =>
        useSort({
          data: testIssues,
          columns: issueColumns,
          initialKey: 'priority',
          initialDirection: 'asc',
        })
      );

      const priorities = result.current.sortedData.map((r) => r.priority);
      expect(priorities).toEqual([0, 1, 2]);
    });

    it('uses function accessor correctly', () => {
      const columnsWithFunctionAccessor: ColumnDef<SimpleRow>[] = [
        {
          id: 'computed',
          header: 'Computed',
          accessor: (row) => row.value * 2,
          sortable: true,
        },
      ];

      const { result } = renderHook(() =>
        useSort({
          data: simpleData,
          columns: columnsWithFunctionAccessor,
          initialKey: 'computed',
          initialDirection: 'asc',
        })
      );

      const values = result.current.sortedData.map((r) => r.value);
      // Original values 30, 10, 20 -> computed 60, 20, 40 -> sorted by computed: 20, 40, 60 -> original: 10, 20, 30
      expect(values).toEqual([10, 20, 30]);
    });
  });

  describe('Issue type integration', () => {
    it('works with Issue[] type', () => {
      const { result } = renderHook(() =>
        useSort({
          data: testIssues,
          columns: issueColumns,
          initialKey: 'title',
          initialDirection: 'asc',
        })
      );

      const titles = result.current.sortedData.map((r) => r.title);
      // Case-insensitive: Alpha, beta, Charlie
      expect(titles).toEqual(['Alpha', 'beta', 'Charlie']);
    });

    it('sorts priorities numerically (P0 before P1)', () => {
      const { result } = renderHook(() =>
        useSort({
          data: testIssues,
          columns: issueColumns,
          initialKey: 'priority',
          initialDirection: 'asc',
        })
      );

      const priorities = result.current.sortedData.map((r) => r.priority);
      expect(priorities).toEqual([0, 1, 2]);
    });

    it('full sort cycle on Issues', () => {
      const { result } = renderHook(() =>
        useSort({
          data: testIssues,
          columns: issueColumns,
        })
      );

      // Initial: unsorted
      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortedData).toEqual(testIssues);

      // Click priority: ascending
      act(() => {
        result.current.handleSort('priority');
      });
      expect(result.current.sortState.key).toBe('priority');
      expect(result.current.sortState.direction).toBe('asc');
      expect(result.current.sortedData.map((r) => r.priority)).toEqual([0, 1, 2]);

      // Click priority again: descending
      act(() => {
        result.current.handleSort('priority');
      });
      expect(result.current.sortState.direction).toBe('desc');
      expect(result.current.sortedData.map((r) => r.priority)).toEqual([2, 1, 0]);

      // Click priority again: clear
      act(() => {
        result.current.handleSort('priority');
      });
      expect(result.current.sortState.key).toBeNull();
      expect(result.current.sortedData).toEqual(testIssues);
    });
  });
});
