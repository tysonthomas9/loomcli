/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from '@testing-library/react';
import { describe, it, expect, vi } from 'vitest';

import type { Issue, Priority } from '@/types';

import { useFilteredSelection } from './useFilteredSelection';

// Helper to create test issues
function createIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: overrides.id ?? 'test-id',
    title: overrides.title ?? 'Test Issue',
    priority: overrides.priority ?? 2,
    created_at: '2024-01-01T00:00:00Z',
    updated_at: '2024-01-01T00:00:00Z',
    ...overrides,
  };
}

// Test data
const testIssues: Issue[] = [
  createIssue({ id: '1', title: 'Alpha', priority: 0 as Priority }),
  createIssue({ id: '2', title: 'Beta', priority: 1 as Priority }),
  createIssue({ id: '3', title: 'Charlie', priority: 1 as Priority }),
  createIssue({ id: '4', title: 'Delta', priority: 2 as Priority }),
];

describe('useFilteredSelection', () => {
  describe('auto-prune behavior', () => {
    it('removes hidden issues from selection when filter changes', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['1', '2', '3', '4'],
          }),
        { initialProps: { filterOptions: {} } }
      );

      // Initially all 4 are selected
      expect(result.current.selection.selectedIds.size).toBe(4);

      // Apply priority filter for P1 only
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Issues 1 (P0) and 4 (P2) should be auto-deselected
      expect(result.current.selection.selectedIds.has('1')).toBe(false);
      expect(result.current.selection.selectedIds.has('2')).toBe(true);
      expect(result.current.selection.selectedIds.has('3')).toBe(true);
      expect(result.current.selection.selectedIds.has('4')).toBe(false);
      expect(result.current.selection.selectedIds.size).toBe(2);
    });

    it('does not remove selection when filter does not affect selected items', () => {
      const onChange = vi.fn();
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['2', '3'], // Only P1 items selected
            onSelectionChange: onChange,
          }),
        { initialProps: { filterOptions: {} } }
      );

      // Initial selection
      expect(result.current.selection.selectedIds.size).toBe(2);
      onChange.mockClear();

      // Apply priority filter for P1 only - selected items stay visible
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Selection should remain unchanged
      expect(result.current.selection.selectedIds.has('2')).toBe(true);
      expect(result.current.selection.selectedIds.has('3')).toBe(true);
      expect(result.current.selection.selectedIds.size).toBe(2);
    });

    it('clears entire selection when filter hides all selected items', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['1'], // Only P0 item selected
          }),
        { initialProps: { filterOptions: {} } }
      );

      expect(result.current.selection.selectedIds.size).toBe(1);

      // Apply priority filter for P1 only - hides the selected P0 item
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Selection should be empty
      expect(result.current.selection.selectedIds.size).toBe(0);
    });

    it('does not prune when clearing filter', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['2', '3'],
          }),
        { initialProps: { filterOptions: { priority: 1 as Priority } } }
      );

      expect(result.current.selection.selectedIds.size).toBe(2);

      // Clear filter - all issues now visible
      rerender({ filterOptions: {} });

      // Selection should remain unchanged (prune only removes, never adds)
      expect(result.current.selection.selectedIds.has('2')).toBe(true);
      expect(result.current.selection.selectedIds.has('3')).toBe(true);
      expect(result.current.selection.selectedIds.size).toBe(2);
    });
  });

  describe('autoPrune option', () => {
    it('does not prune selection when autoPrune=false', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['1', '2', '3', '4'],
            autoPrune: false,
          }),
        { initialProps: { filterOptions: {} } }
      );

      expect(result.current.selection.selectedIds.size).toBe(4);

      // Apply priority filter for P1 only
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Selection should NOT be pruned because autoPrune=false
      expect(result.current.selection.selectedIds.size).toBe(4);
    });

    it('defaults autoPrune to true', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['1', '2', '3', '4'],
            // autoPrune not specified - should default to true
          }),
        { initialProps: { filterOptions: {} } }
      );

      expect(result.current.selection.selectedIds.size).toBe(4);

      // Apply priority filter
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Should be pruned (default autoPrune=true)
      expect(result.current.selection.selectedIds.size).toBe(2);
    });
  });

  describe('hook composition', () => {
    it('returns filteredIssues matching useIssueFilter output', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: { priority: 1 as Priority },
        })
      );

      // Only P1 issues should be in filteredIssues
      expect(result.current.filteredIssues.length).toBe(2);
      expect(result.current.filteredIssues.map((i) => i.id)).toEqual(['2', '3']);
    });

    it('returns correct filter metadata', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: { priority: 1 as Priority },
        })
      );

      expect(result.current.filterMeta.count).toBe(2);
      expect(result.current.filterMeta.totalCount).toBe(4);
      expect(result.current.filterMeta.hasActiveFilters).toBe(true);
      expect(result.current.filterMeta.activeFilters).toContain('priority');
    });

    it('returns hasActiveFilters=false when no filters applied', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
        })
      );

      expect(result.current.filterMeta.hasActiveFilters).toBe(false);
      expect(result.current.filterMeta.activeFilters).toEqual([]);
    });

    it('returns selection state matching useSelection', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
          initialSelection: ['1', '2'],
        })
      );

      expect(result.current.selection.selectedIds.size).toBe(2);
      expect(result.current.selection.isAllSelected).toBe(false);
      expect(result.current.selection.isPartiallySelected).toBe(true);
      expect(result.current.selection.selectedCount).toBe(2);
    });

    it('onSelectionChange callback works', () => {
      const onChange = vi.fn();
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
          onSelectionChange: onChange,
        })
      );

      act(() => {
        result.current.selection.toggleSelection('1', true);
      });

      expect(onChange).toHaveBeenCalledWith(new Set(['1']));
    });
  });

  describe('edge cases', () => {
    it('handles empty issues array', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: [],
          filterOptions: {},
        })
      );

      expect(result.current.filteredIssues).toEqual([]);
      expect(result.current.filterMeta.count).toBe(0);
      expect(result.current.filterMeta.totalCount).toBe(0);
      expect(result.current.selection.selectedIds.size).toBe(0);
    });

    it('handles empty initial selection', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
          initialSelection: [],
        })
      );

      expect(result.current.selection.selectedIds.size).toBe(0);
    });

    it('handles filter that hides all issues', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: { priority: 4 as Priority }, // No P4 issues exist
          initialSelection: ['1', '2'],
        })
      );

      // All issues filtered out
      expect(result.current.filteredIssues.length).toBe(0);
      // Selection should be empty (pruned)
      expect(result.current.selection.selectedIds.size).toBe(0);
    });

    it('selection actions work correctly', () => {
      const { result } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
        })
      );

      // Test selectAll
      act(() => {
        result.current.selection.selectAll();
      });
      expect(result.current.selection.isAllSelected).toBe(true);
      expect(result.current.selection.selectedIds.size).toBe(4);

      // Test clearSelection
      act(() => {
        result.current.selection.clearSelection();
      });
      expect(result.current.selection.selectedIds.size).toBe(0);

      // Test toggleSelection
      act(() => {
        result.current.selection.toggleSelection('1', true);
      });
      expect(result.current.selection.selectedIds.has('1')).toBe(true);
    });
  });

  describe('integration', () => {
    it('full scenario: select items, apply filter, verify auto-deselect', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
          }),
        { initialProps: { filterOptions: {} } }
      );

      // Initially no selection
      expect(result.current.selection.selectedIds.size).toBe(0);

      // Select all items
      act(() => {
        result.current.selection.selectAll();
      });
      expect(result.current.selection.selectedIds.size).toBe(4);

      // Apply priority filter for P1
      rerender({ filterOptions: { priority: 1 as Priority } });

      // Should only have 2 items selected now (the P1 items)
      expect(result.current.selection.selectedIds.size).toBe(2);
      expect(result.current.selection.selectedIds.has('2')).toBe(true);
      expect(result.current.selection.selectedIds.has('3')).toBe(true);

      // filteredIssues should also only show P1 items
      expect(result.current.filteredIssues.length).toBe(2);

      // Clear filter
      rerender({ filterOptions: {} });

      // Selection should still be just 2 (pruning doesn't restore)
      expect(result.current.selection.selectedIds.size).toBe(2);
      // But filteredIssues shows all again
      expect(result.current.filteredIssues.length).toBe(4);
    });

    it('search filter also triggers auto-prune', () => {
      const { result, rerender } = renderHook(
        ({ filterOptions }) =>
          useFilteredSelection({
            issues: testIssues,
            filterOptions,
            initialSelection: ['1', '2', '3', '4'],
          }),
        { initialProps: { filterOptions: {} } }
      );

      expect(result.current.selection.selectedIds.size).toBe(4);

      // Apply search filter
      rerender({ filterOptions: { searchTerm: 'Alpha' } });

      // Only issue 1 matches 'Alpha'
      expect(result.current.filteredIssues.length).toBe(1);
      expect(result.current.selection.selectedIds.size).toBe(1);
      expect(result.current.selection.selectedIds.has('1')).toBe(true);
    });
  });

  describe('stability', () => {
    it('returns stable selection function references', () => {
      const { result, rerender } = renderHook(() =>
        useFilteredSelection({
          issues: testIssues,
          filterOptions: {},
        })
      );

      const refs1 = {
        toggleSelection: result.current.selection.toggleSelection,
        selectAll: result.current.selection.selectAll,
        deselectAll: result.current.selection.deselectAll,
        clearSelection: result.current.selection.clearSelection,
      };

      rerender();

      // Most functions should remain stable
      expect(result.current.selection.toggleSelection).toBe(refs1.toggleSelection);
      expect(result.current.selection.deselectAll).toBe(refs1.deselectAll);
      expect(result.current.selection.clearSelection).toBe(refs1.clearSelection);
    });
  });
});
