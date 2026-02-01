/**
 * useSort - React hook for managing sort state and sorting data.
 * Provides memoized sorting with type-safe comparisons.
 */

import { useState, useMemo, useCallback } from 'react'
import type { ColumnDef } from '@/components/table/columns'
import { getCellValue } from '@/components/table/columns'

/**
 * Sort direction type.
 */
export type SortDirection = 'asc' | 'desc'

/**
 * Current sort state.
 */
export interface SortState {
  /** Column key currently being sorted, or null if unsorted */
  key: string | null
  /** Sort direction */
  direction: SortDirection
}

/**
 * Options for the useSort hook.
 */
export interface UseSortOptions<T> {
  /** Array of items to sort */
  data: T[]
  /** Column definitions with sortable flags and accessors */
  columns: ColumnDef<T>[]
  /** Initial sort column key (optional) */
  initialKey?: string | null
  /** Initial sort direction (optional, defaults to 'asc') */
  initialDirection?: SortDirection
}

/**
 * Return type for the useSort hook.
 */
export interface UseSortReturn<T> {
  /** Sorted data array */
  sortedData: T[]
  /** Current sort state */
  sortState: SortState
  /** Handler to toggle sort on a column */
  handleSort: (columnId: string) => void
  /** Handler to clear sorting */
  clearSort: () => void
}

/**
 * Check if a value is a valid ISO date string.
 */
function isDateString(value: unknown): value is string {
  if (typeof value !== 'string') return false
  // Quick check for ISO date format (YYYY-MM-DD)
  if (!/^\d{4}-\d{2}-\d{2}/.test(value)) return false
  const date = new Date(value)
  return !isNaN(date.getTime())
}

/**
 * Compare two values for sorting.
 * Handles strings, numbers, dates, and null/undefined values.
 */
function compareValues(a: unknown, b: unknown, direction: SortDirection): number {
  // Handle null/undefined - always sort to end
  if (a == null && b == null) return 0
  if (a == null) return 1
  if (b == null) return -1

  let comparison = 0

  if (typeof a === 'string' && typeof b === 'string') {
    // Check for date strings first
    if (isDateString(a) && isDateString(b)) {
      comparison = new Date(a).getTime() - new Date(b).getTime()
    } else {
      // Case-insensitive string comparison
      comparison = a.localeCompare(b, undefined, { sensitivity: 'base' })
    }
  } else if (typeof a === 'number' && typeof b === 'number') {
    comparison = a - b
  } else if (typeof a === 'boolean' && typeof b === 'boolean') {
    // true before false in ascending order
    comparison = a === b ? 0 : a ? -1 : 1
  } else {
    // Fallback to string comparison
    comparison = String(a).localeCompare(String(b), undefined, { sensitivity: 'base' })
  }

  return direction === 'desc' ? -comparison : comparison
}

/**
 * React hook for managing sort state and sorting data.
 *
 * @example
 * ```tsx
 * function MyTable() {
 *   const { sortedData, sortState, handleSort } = useSort({
 *     data: issues,
 *     columns: columns,
 *     initialKey: 'priority',
 *     initialDirection: 'asc',
 *   })
 *
 *   return (
 *     <table>
 *       <thead>
 *         <tr>
 *           {columns.map(col => (
 *             <th key={col.id} onClick={() => handleSort(col.id)}>
 *               {col.header}
 *               {sortState.key === col.id && (sortState.direction === 'asc' ? '↑' : '↓')}
 *             </th>
 *           ))}
 *         </tr>
 *       </thead>
 *       <tbody>
 *         {sortedData.map(item => (
 *           // ...
 *         ))}
 *       </tbody>
 *     </table>
 *   )
 * }
 * ```
 */
export function useSort<T>(options: UseSortOptions<T>): UseSortReturn<T> {
  const { data, columns, initialKey = null, initialDirection = 'asc' } = options

  // Validate initialKey exists in columns
  const validInitialKey = useMemo(() => {
    if (initialKey == null) return null
    const column = columns.find((col) => col.id === initialKey)
    return column?.sortable ? initialKey : null
  }, [initialKey, columns])

  // Sort state
  const [sortState, setSortState] = useState<SortState>({
    key: validInitialKey,
    direction: initialDirection,
  })

  // Memoized sorted data
  // Note: Column lookup is inlined to avoid unnecessary re-sorts when columns
  // array reference changes but content is the same.
  const sortedData = useMemo(() => {
    // Return original order if not sorting
    if (sortState.key == null) {
      return data
    }

    // Find the column to sort by
    const sortColumn = columns.find((col) => col.id === sortState.key)
    if (!sortColumn) {
      return data
    }

    // Create a copy to avoid mutating original array
    return [...data].sort((a, b) => {
      const valueA = getCellValue(a, sortColumn)
      const valueB = getCellValue(b, sortColumn)
      return compareValues(valueA, valueB, sortState.direction)
    })
  }, [data, sortState.key, sortState.direction, columns])

  // Handle sort toggle
  const handleSort = useCallback(
    (columnId: string) => {
      // Find the column
      const column = columns.find((col) => col.id === columnId)

      // Ignore if column not found or not sortable
      if (!column?.sortable) {
        return
      }

      setSortState((prev) => {
        // If clicking a different column, sort ascending
        if (prev.key !== columnId) {
          return { key: columnId, direction: 'asc' }
        }

        // If currently ascending, switch to descending
        if (prev.direction === 'asc') {
          return { key: columnId, direction: 'desc' }
        }

        // If currently descending, clear sort
        return { key: null, direction: 'asc' }
      })
    },
    [columns]
  )

  // Clear sort
  const clearSort = useCallback(() => {
    setSortState({ key: null, direction: 'asc' })
  }, [])

  return {
    sortedData,
    sortState,
    handleSort,
    clearSort,
  }
}
