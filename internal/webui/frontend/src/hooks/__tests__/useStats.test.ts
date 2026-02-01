/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act, waitFor } from '@testing-library/react'
import { useStats } from '../useStats'
import type { Statistics } from '@/types'

// Mock the getStats API function
vi.mock('@/api/issues', () => ({
  getStats: vi.fn(),
}))

// Import the mocked function for test manipulation
import { getStats } from '@/api/issues'
const mockGetStats = vi.mocked(getStats)

/**
 * Helper to create mock Statistics data.
 */
function createMockStats(overrides?: Partial<Statistics>): Statistics {
  return {
    total_issues: 100,
    open_issues: 25,
    in_progress_issues: 10,
    closed_issues: 50,
    blocked_issues: 5,
    deferred_issues: 3,
    ready_issues: 7,
    tombstone_issues: 0,
    pinned_issues: 2,
    epics_eligible_for_closure: 1,
    average_lead_time_hours: 48.5,
    ...overrides,
  }
}

/**
 * Helper to flush pending promises when using fake timers.
 */
async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve()
  })
}

describe('useStats', () => {
  beforeEach(() => {
    mockGetStats.mockReset()
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  describe('initial state', () => {
    it('returns { data: null, loading: false, error: null } initially when enabled=false', () => {
      const { result } = renderHook(() => useStats({ enabled: false }))

      expect(result.current.data).toBeNull()
      expect(result.current.loading).toBe(false)
      expect(result.current.error).toBeNull()
    })

    it('returns expected shape with all properties', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() => useStats())

      expect(result.current).toHaveProperty('data')
      expect(result.current).toHaveProperty('loading')
      expect(result.current).toHaveProperty('error')
      expect(result.current).toHaveProperty('refetch')
      expect(typeof result.current.refetch).toBe('function')

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })
    })
  })

  describe('loading state', () => {
    it('sets loading=true during fetch', async () => {
      mockGetStats.mockImplementation(() => new Promise(() => {})) // Never resolves

      const { result } = renderHook(() => useStats())

      expect(result.current.loading).toBe(true)
      expect(result.current.data).toBeNull()
      expect(result.current.error).toBeNull()
    })

    it('sets loading to false after successful fetch', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() => useStats())

      expect(result.current.loading).toBe(true)

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(result.current.data).not.toBeNull()
    })
  })

  describe('successful fetch', () => {
    it('sets data on successful fetch', async () => {
      const mockStats = createMockStats({ open_issues: 42 })
      mockGetStats.mockResolvedValue(mockStats)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(result.current.data).toEqual(mockStats)
      expect(result.current.error).toBeNull()
      expect(mockGetStats).toHaveBeenCalledTimes(1)
    })

    it('clears previous error on successful fetch', async () => {
      // First fetch fails
      mockGetStats.mockRejectedValueOnce(new Error('Network error'))

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.error).not.toBeNull()
      })

      // Second fetch succeeds
      const mockStats = createMockStats()
      mockGetStats.mockResolvedValueOnce(mockStats)

      await act(async () => {
        await result.current.refetch()
      })

      expect(result.current.data).toEqual(mockStats)
      expect(result.current.error).toBeNull()
    })
  })

  describe('error handling', () => {
    it('sets error on failed fetch (network error)', async () => {
      const networkError = new Error('Network error: Failed to fetch')
      mockGetStats.mockRejectedValue(networkError)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(result.current.error).toEqual(networkError)
      expect(result.current.data).toBeNull()
    })

    it('converts non-Error objects to Error instances', async () => {
      mockGetStats.mockRejectedValue('String error message')

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(result.current.error).toBeInstanceOf(Error)
      expect(result.current.error?.message).toBe('String error message')
    })

    it('preserves stale data on subsequent error', async () => {
      // First fetch succeeds
      const mockStats = createMockStats({ open_issues: 15 })
      mockGetStats.mockResolvedValueOnce(mockStats)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.data).toEqual(mockStats)
      })

      // Second fetch fails
      mockGetStats.mockRejectedValueOnce(new Error('Temporary failure'))

      await act(async () => {
        await result.current.refetch()
      })

      // Data should still be preserved (stale data)
      expect(result.current.data).toEqual(mockStats)
      expect(result.current.error).toBeInstanceOf(Error)
    })
  })

  describe('polling', () => {
    beforeEach(() => {
      vi.useFakeTimers()
    })

    afterEach(() => {
      vi.useRealTimers()
    })

    it('polls at specified interval when pollInterval is set', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      renderHook(() => useStats({ pollInterval: 5000 }))

      // Initial fetch
      expect(mockGetStats).toHaveBeenCalledTimes(1)
      await flushPromises()

      // Advance time to trigger first poll
      await act(async () => {
        vi.advanceTimersByTime(5000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(2)

      // Advance time to trigger second poll
      await act(async () => {
        vi.advanceTimersByTime(5000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(3)
    })

    it('does not poll when pollInterval is 0', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      renderHook(() => useStats({ pollInterval: 0 }))

      expect(mockGetStats).toHaveBeenCalledTimes(1)
      await flushPromises()

      // Advance time significantly
      await act(async () => {
        vi.advanceTimersByTime(60000)
      })
      await flushPromises()

      // Should still be just 1 call
      expect(mockGetStats).toHaveBeenCalledTimes(1)
    })

    it('uses default pollInterval of 30000ms', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      renderHook(() => useStats())

      expect(mockGetStats).toHaveBeenCalledTimes(1)
      await flushPromises()

      // Advance 29 seconds - should not poll yet
      await act(async () => {
        vi.advanceTimersByTime(29000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(1)

      // Advance 1 more second (total 30s) - should poll
      await act(async () => {
        vi.advanceTimersByTime(1000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(2)
    })
  })

  describe('enabled option', () => {
    it('stops polling when enabled=false', async () => {
      vi.useFakeTimers()

      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() =>
        useStats({ pollInterval: 5000, enabled: false })
      )

      // Should not fetch when disabled
      expect(mockGetStats).not.toHaveBeenCalled()
      expect(result.current.data).toBeNull()
      expect(result.current.loading).toBe(false)

      // Advance time - still no calls
      await act(async () => {
        vi.advanceTimersByTime(10000)
      })
      await flushPromises()

      expect(mockGetStats).not.toHaveBeenCalled()

      vi.useRealTimers()
    })

    it('starts fetching when enabled changes from false to true', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result, rerender } = renderHook(
        ({ enabled }) => useStats({ pollInterval: 5000, enabled }),
        { initialProps: { enabled: false } }
      )

      expect(mockGetStats).not.toHaveBeenCalled()

      // Enable the hook
      rerender({ enabled: true })

      await waitFor(() => {
        expect(mockGetStats).toHaveBeenCalledTimes(1)
      })

      expect(result.current.data).not.toBeNull()
    })

    it('stops fetching when enabled changes from true to false', async () => {
      vi.useFakeTimers()

      mockGetStats.mockResolvedValue(createMockStats())

      const { rerender } = renderHook(
        ({ enabled }) => useStats({ pollInterval: 5000, enabled }),
        { initialProps: { enabled: true } }
      )

      expect(mockGetStats).toHaveBeenCalledTimes(1)
      await flushPromises()

      // Disable the hook
      rerender({ enabled: false })

      // Advance time - no additional calls should happen
      await act(async () => {
        vi.advanceTimersByTime(10000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(1)

      vi.useRealTimers()
    })
  })

  describe('cleanup', () => {
    beforeEach(() => {
      vi.useFakeTimers()
    })

    afterEach(() => {
      vi.useRealTimers()
    })

    it('cleans up interval on unmount', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { unmount } = renderHook(() => useStats({ pollInterval: 5000 }))

      expect(mockGetStats).toHaveBeenCalledTimes(1)
      await flushPromises()

      // Unmount the hook
      unmount()

      // Advance time - no additional calls should happen
      await act(async () => {
        vi.advanceTimersByTime(10000)
      })
      await flushPromises()

      expect(mockGetStats).toHaveBeenCalledTimes(1)
    })

    it('does not update state after unmount', async () => {
      let resolvePromise: (value: Statistics) => void
      const slowPromise = new Promise<Statistics>((resolve) => {
        resolvePromise = resolve
      })
      mockGetStats.mockReturnValue(slowPromise)

      const { result, unmount } = renderHook(() => useStats())

      expect(result.current.loading).toBe(true)

      // Unmount while fetch is in progress
      unmount()

      // Resolve the promise after unmount
      await act(async () => {
        resolvePromise!(createMockStats())
      })

      // No errors should occur (React would warn about state update on unmounted component)
    })

    it('does not update state after unmount with error', async () => {
      mockGetStats.mockRejectedValue(new Error('Network error'))

      const { result, unmount } = renderHook(() => useStats())

      expect(result.current.loading).toBe(true)

      // Unmount while fetch is in progress
      unmount()

      await flushPromises()

      // No errors should occur (the hook guards against this)
    })
  })

  describe('refetch function', () => {
    it('refetch function triggers new fetch', async () => {
      const mockStats1 = createMockStats({ open_issues: 5 })
      const mockStats2 = createMockStats({ open_issues: 15 })

      mockGetStats.mockResolvedValueOnce(mockStats1).mockResolvedValueOnce(mockStats2)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.data?.open_issues).toBe(5)
      })

      expect(mockGetStats).toHaveBeenCalledTimes(1)

      // Trigger refetch
      await act(async () => {
        await result.current.refetch()
      })

      expect(mockGetStats).toHaveBeenCalledTimes(2)
      expect(result.current.data?.open_issues).toBe(15)
    })

    it('refetch function is stable across re-renders', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result, rerender } = renderHook(() => useStats())

      const refetch1 = result.current.refetch

      rerender()

      expect(result.current.refetch).toBe(refetch1)

      // Wait for fetch to complete
      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })
    })

    it('refetch updates data with new values', async () => {
      const initialStats = createMockStats({ open_issues: 10 })
      const updatedStats = createMockStats({ open_issues: 20 })

      mockGetStats.mockResolvedValueOnce(initialStats)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.data).toEqual(initialStats)
      })

      mockGetStats.mockResolvedValueOnce(updatedStats)

      await act(async () => {
        await result.current.refetch()
      })

      expect(result.current.data).toEqual(updatedStats)
    })
  })

  describe('concurrent fetch prevention', () => {
    beforeEach(() => {
      vi.useFakeTimers()
    })

    afterEach(() => {
      vi.useRealTimers()
    })

    it('prevents overlapping requests', async () => {
      let resolveFirst: (value: Statistics) => void
      const firstPromise = new Promise<Statistics>((resolve) => {
        resolveFirst = resolve
      })
      mockGetStats.mockReturnValueOnce(firstPromise)
      mockGetStats.mockResolvedValue(createMockStats())

      renderHook(() => useStats({ pollInterval: 1000 }))

      // Initial fetch starts
      expect(mockGetStats).toHaveBeenCalledTimes(1)

      // Advance time - poll should trigger but be skipped since fetch in progress
      await act(async () => {
        vi.advanceTimersByTime(1000)
      })

      // Still just 1 call because fetch is in progress
      expect(mockGetStats).toHaveBeenCalledTimes(1)

      // Resolve first request
      await act(async () => {
        resolveFirst!(createMockStats())
      })

      // Advance to next poll
      await act(async () => {
        vi.advanceTimersByTime(1000)
      })
      await flushPromises()

      // Now second fetch should happen
      expect(mockGetStats).toHaveBeenCalledTimes(2)
    })
  })

  describe('default options', () => {
    it('enabled defaults to true', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.data).not.toBeNull()
      })

      expect(mockGetStats).toHaveBeenCalled()
    })
  })

  describe('edge cases', () => {
    it('handles undefined options', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() => useStats(undefined))

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(mockGetStats).toHaveBeenCalled()
    })

    it('handles empty options object', async () => {
      mockGetStats.mockResolvedValue(createMockStats())

      const { result } = renderHook(() => useStats({}))

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(mockGetStats).toHaveBeenCalled()
    })

    it('handles empty response', async () => {
      const emptyStats = createMockStats({
        total_issues: 0,
        open_issues: 0,
        in_progress_issues: 0,
        closed_issues: 0,
        blocked_issues: 0,
        deferred_issues: 0,
        ready_issues: 0,
      })
      mockGetStats.mockResolvedValue(emptyStats)

      const { result } = renderHook(() => useStats())

      await waitFor(() => {
        expect(result.current.loading).toBe(false)
      })

      expect(result.current.data).toEqual(emptyStats)
      expect(result.current.error).toBeNull()
    })
  })
})
