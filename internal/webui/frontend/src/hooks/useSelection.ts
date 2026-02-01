/**
 * useSelection - React hook for managing multi-select state.
 * Provides memoized selection with type-safe operations.
 */

import { useState, useMemo, useCallback, useRef, useEffect } from 'react'

/**
 * Options for the useSelection hook.
 */
export interface UseSelectionOptions {
  /** Array of items currently visible (filtered/displayed) */
  visibleItems: { id: string }[]
  /** Initial set of selected IDs (optional) */
  initialSelection?: Set<string> | string[]
  /** Callback when selection changes (optional) */
  onSelectionChange?: (selectedIds: Set<string>) => void
}

/**
 * Return type for the useSelection hook.
 */
export interface UseSelectionReturn {
  /** Set of currently selected item IDs */
  selectedIds: Set<string>
  /** Whether all visible items are selected */
  isAllSelected: boolean
  /** Whether some (but not all) visible items are selected */
  isPartiallySelected: boolean
  /** Number of selected items */
  selectedCount: number
  /** Toggle selection for a single item */
  toggleSelection: (itemId: string, selected: boolean) => void
  /** Select all visible items */
  selectAll: () => void
  /** Deselect all items */
  deselectAll: () => void
  /** Toggle between select all and deselect all */
  toggleAll: () => void
  /** Remove items from selection that are not in the provided array */
  pruneSelection: (visibleIds: { id: string }[]) => void
  /** Clear the entire selection */
  clearSelection: () => void
}

/**
 * React hook for managing multi-select state.
 *
 * @example
 * ```tsx
 * function MyTable() {
 *   const { selectedIds, toggleSelection, isAllSelected, toggleAll } = useSelection({
 *     visibleItems: issues,
 *   })
 *
 *   return (
 *     <table>
 *       <thead>
 *         <tr>
 *           <th>
 *             <input
 *               type="checkbox"
 *               checked={isAllSelected}
 *               onChange={toggleAll}
 *             />
 *           </th>
 *           ...
 *         </tr>
 *       </thead>
 *       <tbody>
 *         {issues.map(issue => (
 *           <tr key={issue.id}>
 *             <td>
 *               <input
 *                 type="checkbox"
 *                 checked={selectedIds.has(issue.id)}
 *                 onChange={(e) => toggleSelection(issue.id, e.target.checked)}
 *               />
 *             </td>
 *             ...
 *           </tr>
 *         ))}
 *       </tbody>
 *     </table>
 *   )
 * }
 * ```
 */
export function useSelection(options: UseSelectionOptions): UseSelectionReturn {
  const { visibleItems, initialSelection, onSelectionChange } = options

  // Convert initial selection to Set if array provided
  const [selectedIds, setSelectedIds] = useState<Set<string>>(() => {
    if (!initialSelection) return new Set()
    if (initialSelection instanceof Set) return new Set(initialSelection)
    return new Set(initialSelection)
  })

  // Store callback in ref to avoid stale closures (following useMutationHandler pattern)
  const onSelectionChangeRef = useRef(onSelectionChange)

  // Update ref when callback changes
  useEffect(() => {
    onSelectionChangeRef.current = onSelectionChange
  }, [onSelectionChange])

  // Notify parent of changes
  const notifyChange = useCallback((newSelection: Set<string>) => {
    onSelectionChangeRef.current?.(newSelection)
  }, [])

  // Compute selection statistics
  const selectionStats = useMemo(() => {
    if (visibleItems.length === 0) {
      return {
        isAllSelected: false,
        isPartiallySelected: false,
        selectedCount: selectedIds.size,
      }
    }

    let selectedVisibleCount = 0
    for (const item of visibleItems) {
      if (selectedIds.has(item.id)) {
        selectedVisibleCount++
      }
    }

    const isAllSelected = selectedVisibleCount === visibleItems.length
    const isPartiallySelected = selectedVisibleCount > 0 && !isAllSelected

    return {
      isAllSelected,
      isPartiallySelected,
      selectedCount: selectedIds.size,
    }
  }, [visibleItems, selectedIds])

  // Toggle single item selection
  const toggleSelection = useCallback(
    (itemId: string, selected: boolean) => {
      setSelectedIds((prev) => {
        const next = new Set(prev)
        if (selected) {
          next.add(itemId)
        } else {
          next.delete(itemId)
        }
        notifyChange(next)
        return next
      })
    },
    [notifyChange]
  )

  // Select all visible items
  const selectAll = useCallback(() => {
    setSelectedIds((prev) => {
      const next = new Set(prev)
      for (const item of visibleItems) {
        next.add(item.id)
      }
      notifyChange(next)
      return next
    })
  }, [visibleItems, notifyChange])

  // Deselect all items
  const deselectAll = useCallback(() => {
    setSelectedIds(() => {
      const next = new Set<string>()
      notifyChange(next)
      return next
    })
  }, [notifyChange])

  // Toggle all - select all if not all selected, otherwise deselect all
  const toggleAll = useCallback(() => {
    if (selectionStats.isAllSelected) {
      deselectAll()
    } else {
      selectAll()
    }
  }, [selectionStats.isAllSelected, selectAll, deselectAll])

  // Prune selection to only include IDs in the provided array
  const pruneSelection = useCallback(
    (visibleIds: { id: string }[]) => {
      const visibleSet = new Set(visibleIds.map((item) => item.id))
      setSelectedIds((prev) => {
        const next = new Set<string>()
        for (const id of prev) {
          if (visibleSet.has(id)) {
            next.add(id)
          }
        }
        // Only update if actually changed
        if (next.size !== prev.size) {
          notifyChange(next)
          return next
        }
        return prev
      })
    },
    [notifyChange]
  )

  // Clear all selection
  const clearSelection = useCallback(() => {
    deselectAll()
  }, [deselectAll])

  return {
    selectedIds,
    isAllSelected: selectionStats.isAllSelected,
    isPartiallySelected: selectionStats.isPartiallySelected,
    selectedCount: selectionStats.selectedCount,
    toggleSelection,
    selectAll,
    deselectAll,
    toggleAll,
    pruneSelection,
    clearSelection,
  }
}
