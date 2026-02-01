/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useSelection } from './useSelection'

// Test data
interface TestItem {
  id: string
  name: string
}

const testItems: TestItem[] = [
  { id: '1', name: 'Alpha' },
  { id: '2', name: 'Beta' },
  { id: '3', name: 'Charlie' },
]

describe('useSelection', () => {
  describe('initial state', () => {
    it('starts with empty selection by default', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      expect(result.current.selectedIds.size).toBe(0)
      expect(result.current.isAllSelected).toBe(false)
      expect(result.current.isPartiallySelected).toBe(false)
      expect(result.current.selectedCount).toBe(0)
    })

    it('respects initialSelection as Set', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: new Set(['1', '2']),
        })
      )
      expect(result.current.selectedIds.size).toBe(2)
      expect(result.current.selectedIds.has('1')).toBe(true)
      expect(result.current.selectedIds.has('2')).toBe(true)
    })

    it('respects initialSelection as array', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '3'],
        })
      )
      expect(result.current.selectedIds.size).toBe(2)
      expect(result.current.selectedIds.has('1')).toBe(true)
      expect(result.current.selectedIds.has('3')).toBe(true)
    })

    it('handles undefined initialSelection', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: undefined,
        })
      )
      expect(result.current.selectedIds.size).toBe(0)
    })
  })

  describe('toggleSelection', () => {
    it('adds item when selected=true', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.toggleSelection('1', true)
      })
      expect(result.current.selectedIds.has('1')).toBe(true)
      expect(result.current.selectedCount).toBe(1)
    })

    it('removes item when selected=false', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2'],
        })
      )
      act(() => {
        result.current.toggleSelection('1', false)
      })
      expect(result.current.selectedIds.has('1')).toBe(false)
      expect(result.current.selectedIds.has('2')).toBe(true)
      expect(result.current.selectedCount).toBe(1)
    })

    it('works with item not in visible list', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.toggleSelection('999', true)
      })
      expect(result.current.selectedIds.has('999')).toBe(true)
      expect(result.current.selectedCount).toBe(1)
    })

    it('calls onSelectionChange callback', () => {
      const onChange = vi.fn()
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          onSelectionChange: onChange,
        })
      )
      act(() => {
        result.current.toggleSelection('1', true)
      })
      expect(onChange).toHaveBeenCalledTimes(1)
      expect(onChange).toHaveBeenCalledWith(new Set(['1']))
    })
  })

  describe('selectAll', () => {
    it('selects all visible items', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.selectAll()
      })
      expect(result.current.selectedIds.size).toBe(3)
      expect(result.current.isAllSelected).toBe(true)
    })

    it('preserves existing selections outside visible items', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems.slice(0, 2), // Only 1 and 2 visible
          initialSelection: ['3'], // 3 is selected but not visible
        })
      )
      act(() => {
        result.current.selectAll()
      })
      expect(result.current.selectedIds.has('1')).toBe(true)
      expect(result.current.selectedIds.has('2')).toBe(true)
      expect(result.current.selectedIds.has('3')).toBe(true)
      expect(result.current.selectedCount).toBe(3)
    })

    it('works with empty visible items', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: [] })
      )
      act(() => {
        result.current.selectAll()
      })
      expect(result.current.selectedIds.size).toBe(0)
    })

    it('calls onSelectionChange', () => {
      const onChange = vi.fn()
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          onSelectionChange: onChange,
        })
      )
      act(() => {
        result.current.selectAll()
      })
      expect(onChange).toHaveBeenCalledWith(new Set(['1', '2', '3']))
    })
  })

  describe('deselectAll', () => {
    it('clears all selections', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2', '3'],
        })
      )
      act(() => {
        result.current.deselectAll()
      })
      expect(result.current.selectedIds.size).toBe(0)
      expect(result.current.isAllSelected).toBe(false)
    })

    it('calls onSelectionChange', () => {
      const onChange = vi.fn()
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1'],
          onSelectionChange: onChange,
        })
      )
      onChange.mockClear()
      act(() => {
        result.current.deselectAll()
      })
      expect(onChange).toHaveBeenCalledWith(new Set())
    })
  })

  describe('clearSelection', () => {
    it('clears all selections (alias for deselectAll)', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2'],
        })
      )
      act(() => {
        result.current.clearSelection()
      })
      expect(result.current.selectedIds.size).toBe(0)
    })
  })

  describe('toggleAll', () => {
    it('selects all when none selected', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.toggleAll()
      })
      expect(result.current.isAllSelected).toBe(true)
    })

    it('selects all when partially selected', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1'],
        })
      )
      expect(result.current.isPartiallySelected).toBe(true)
      act(() => {
        result.current.toggleAll()
      })
      expect(result.current.isAllSelected).toBe(true)
    })

    it('deselects all when all selected', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2', '3'],
        })
      )
      expect(result.current.isAllSelected).toBe(true)
      act(() => {
        result.current.toggleAll()
      })
      expect(result.current.selectedIds.size).toBe(0)
    })
  })

  describe('pruneSelection', () => {
    it('removes IDs not in visible items', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2', '3'],
        })
      )
      act(() => {
        // Filter to only items 1 and 2
        result.current.pruneSelection([{ id: '1' }, { id: '2' }])
      })
      expect(result.current.selectedIds.size).toBe(2)
      expect(result.current.selectedIds.has('3')).toBe(false)
    })

    it('does not update state if no pruning needed', () => {
      const onChange = vi.fn()
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1'],
          onSelectionChange: onChange,
        })
      )
      onChange.mockClear()
      act(() => {
        result.current.pruneSelection(testItems)
      })
      expect(onChange).not.toHaveBeenCalled()
    })

    it('works with empty selection', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.pruneSelection([{ id: '1' }])
      })
      expect(result.current.selectedIds.size).toBe(0)
    })

    it('calls onSelectionChange when pruning occurs', () => {
      const onChange = vi.fn()
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2', '3'],
          onSelectionChange: onChange,
        })
      )
      onChange.mockClear()
      act(() => {
        result.current.pruneSelection([{ id: '1' }])
      })
      expect(onChange).toHaveBeenCalledWith(new Set(['1']))
    })
  })

  describe('computed state', () => {
    it('isPartiallySelected is true when some but not all selected', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '2'],
        })
      )
      expect(result.current.isPartiallySelected).toBe(true)
      expect(result.current.isAllSelected).toBe(false)
    })

    it('handles empty visibleItems', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: [],
          initialSelection: ['1'],
        })
      )
      expect(result.current.isAllSelected).toBe(false)
      expect(result.current.isPartiallySelected).toBe(false)
      expect(result.current.selectedCount).toBe(1)
    })

    it('isAllSelected is true when all visible items selected', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems.slice(0, 2), // Only 1 and 2 visible
          initialSelection: ['1', '2'],
        })
      )
      expect(result.current.isAllSelected).toBe(true)
      expect(result.current.isPartiallySelected).toBe(false)
    })

    it('selectedCount reflects total selection size', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems.slice(0, 2), // Only 1 and 2 visible
          initialSelection: ['1', '2', '3'], // 3 is not visible but selected
        })
      )
      expect(result.current.selectedCount).toBe(3)
    })
  })

  describe('stability', () => {
    it('returns stable function references', () => {
      const { result, rerender } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      const refs1 = {
        toggleSelection: result.current.toggleSelection,
        selectAll: result.current.selectAll,
        deselectAll: result.current.deselectAll,
        clearSelection: result.current.clearSelection,
      }
      rerender()
      expect(result.current.toggleSelection).toBe(refs1.toggleSelection)
      expect(result.current.deselectAll).toBe(refs1.deselectAll)
      expect(result.current.clearSelection).toBe(refs1.clearSelection)
    })

    it('toggleAll reference updates when isAllSelected changes', () => {
      const { result, rerender } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      const toggleAll1 = result.current.toggleAll

      act(() => {
        result.current.selectAll()
      })

      rerender()

      // toggleAll should have a new reference when isAllSelected changes
      // because it depends on selectionStats.isAllSelected
      expect(result.current.toggleAll).not.toBe(toggleAll1)
    })

    it('selectAll reference updates when visibleItems changes', () => {
      const { result, rerender } = renderHook(
        ({ items }) => useSelection({ visibleItems: items }),
        { initialProps: { items: testItems } }
      )
      const selectAll1 = result.current.selectAll

      rerender({ items: testItems.slice(0, 2) })

      // selectAll should have a new reference when visibleItems changes
      expect(result.current.selectAll).not.toBe(selectAll1)
    })
  })

  describe('edge cases', () => {
    it('handles duplicate IDs in initial selection array', () => {
      const { result } = renderHook(() =>
        useSelection({
          visibleItems: testItems,
          initialSelection: ['1', '1', '2'],
        })
      )
      expect(result.current.selectedIds.size).toBe(2)
    })

    it('handles toggling same item multiple times', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.toggleSelection('1', true)
      })
      act(() => {
        result.current.toggleSelection('1', true)
      })
      expect(result.current.selectedIds.size).toBe(1)

      act(() => {
        result.current.toggleSelection('1', false)
      })
      act(() => {
        result.current.toggleSelection('1', false)
      })
      expect(result.current.selectedIds.size).toBe(0)
    })

    it('handles rapid selection changes', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      act(() => {
        result.current.toggleSelection('1', true)
        result.current.toggleSelection('2', true)
        result.current.toggleSelection('1', false)
        result.current.toggleSelection('3', true)
      })
      expect(result.current.selectedIds.has('1')).toBe(false)
      expect(result.current.selectedIds.has('2')).toBe(true)
      expect(result.current.selectedIds.has('3')).toBe(true)
    })

    it('returns new Set instance on each change', () => {
      const { result } = renderHook(() =>
        useSelection({ visibleItems: testItems })
      )
      const set1 = result.current.selectedIds

      act(() => {
        result.current.toggleSelection('1', true)
      })

      const set2 = result.current.selectedIds
      expect(set1).not.toBe(set2)
    })
  })
})
