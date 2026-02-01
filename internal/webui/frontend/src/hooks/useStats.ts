/**
 * useStats - React hook for fetching and managing project statistics.
 * Provides issue counts (open, in progress, ready, closed) for project health display.
 */

import { useState, useEffect, useCallback, useRef } from 'react'
import { getStats } from '@/api/issues'
import type { Statistics } from '@/types'

/**
 * Options for the useStats hook.
 */
export interface UseStatsOptions {
  /** Poll interval in ms (default: 30000) */
  pollInterval?: number
  /** Whether to fetch (default: true) */
  enabled?: boolean
}

/**
 * Return type for the useStats hook.
 */
export interface UseStatsResult {
  /** Statistics data, null if not yet loaded */
  data: Statistics | null
  /** Whether a fetch is currently in progress */
  loading: boolean
  /** Error from the last fetch attempt, null if successful */
  error: Error | null
  /** Function to manually trigger a refetch */
  refetch: () => Promise<void>
}

/**
 * React hook for fetching project statistics with optional polling.
 *
 * @param options - Configuration options for the hook
 * @returns Object with data, loading, error states and refetch function
 *
 * @example
 * ```tsx
 * function StatsDisplay() {
 *   const { data, loading, error, refetch } = useStats({
 *     pollInterval: 30000, // Poll every 30 seconds
 *   })
 *
 *   if (loading && !data) return <Loading />
 *   if (error) return <Error message={error.message} onRetry={refetch} />
 *
 *   return <StatsHeader stats={data} />
 * }
 * ```
 */
export function useStats(options?: UseStatsOptions): UseStatsResult {
  const { pollInterval = 30000, enabled = true } = options ?? {}

  const [data, setData] = useState<Statistics | null>(null)
  const [loading, setLoading] = useState<boolean>(false)
  const [error, setError] = useState<Error | null>(null)

  // Track if a fetch is in progress to prevent overlapping requests
  const fetchInProgressRef = useRef<boolean>(false)

  // Track if the component is mounted for cleanup
  const mountedRef = useRef<boolean>(true)

  // Stable fetch function using useCallback
  const fetchData = useCallback(async () => {
    // Skip if already fetching (prevents stacking poll requests)
    if (fetchInProgressRef.current) {
      return
    }

    fetchInProgressRef.current = true
    setLoading(true)

    try {
      const result = await getStats()

      // Only update state if still mounted
      if (mountedRef.current) {
        setData(result)
        setError(null)
      }
    } catch (err) {
      // Only update state if still mounted
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)))
        // Keep stale data on error
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false)
      }
      fetchInProgressRef.current = false
    }
  }, [])

  // Refetch function exposed to consumers
  const refetch = useCallback(async () => {
    await fetchData()
  }, [fetchData])

  // Initial fetch and polling setup
  useEffect(() => {
    mountedRef.current = true

    // Don't fetch if disabled
    if (!enabled) {
      return
    }

    // Initial fetch
    fetchData()

    // Setup polling if interval is specified
    let intervalId: ReturnType<typeof setInterval> | null = null
    if (pollInterval && pollInterval > 0) {
      intervalId = setInterval(() => {
        fetchData()
      }, pollInterval)
    }

    // Cleanup
    return () => {
      mountedRef.current = false
      if (intervalId) {
        clearInterval(intervalId)
      }
    }
  }, [enabled, pollInterval, fetchData])

  return {
    data,
    loading,
    error,
    refetch,
  }
}
