/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useDebounce } from './useDebounce'

describe('useDebounce', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns initial value immediately', () => {
    const { result } = renderHook(() => useDebounce('initial', 500))

    expect(result.current).toBe('initial')
  })

  it('updates value after delay', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'initial', delay: 500 } }
    )

    // Verify initial value
    expect(result.current).toBe('initial')

    // Update the value
    rerender({ value: 'updated', delay: 500 })

    // Value should still be the initial one immediately after change
    expect(result.current).toBe('initial')

    // Advance time by delay amount
    act(() => {
      vi.advanceTimersByTime(500)
    })

    // Now the value should be updated
    expect(result.current).toBe('updated')
  })

  it('resets timer when value changes during delay', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'a', delay: 500 } }
    )

    expect(result.current).toBe('a')

    // Change value after 200ms
    act(() => {
      vi.advanceTimersByTime(200)
    })
    rerender({ value: 'b', delay: 500 })

    // Value should still be 'a'
    expect(result.current).toBe('a')

    // Advance another 300ms (total 500ms since start, but only 300ms since last change)
    act(() => {
      vi.advanceTimersByTime(300)
    })

    // Should still be 'a' because the timer was reset
    expect(result.current).toBe('a')

    // Advance another 200ms (total 500ms since value 'b' was set)
    act(() => {
      vi.advanceTimersByTime(200)
    })

    // Now it should be 'b'
    expect(result.current).toBe('b')
  })

  it('handles rapid value changes - only final value is returned', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'a', delay: 300 } }
    )

    expect(result.current).toBe('a')

    // Simulate rapid typing
    act(() => {
      vi.advanceTimersByTime(100)
    })
    rerender({ value: 'ab', delay: 300 })

    act(() => {
      vi.advanceTimersByTime(100)
    })
    rerender({ value: 'abc', delay: 300 })

    act(() => {
      vi.advanceTimersByTime(100)
    })
    rerender({ value: 'abcd', delay: 300 })

    // Should still be initial value
    expect(result.current).toBe('a')

    // Advance full delay from last change
    act(() => {
      vi.advanceTimersByTime(300)
    })

    // Should be the final value
    expect(result.current).toBe('abcd')
  })

  it('handles zero delay correctly', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'initial', delay: 0 } }
    )

    expect(result.current).toBe('initial')

    rerender({ value: 'updated', delay: 0 })

    // With delay 0, setTimeout(fn, 0) still defers to next tick
    expect(result.current).toBe('initial')

    // Advance timers to execute the setTimeout(fn, 0)
    act(() => {
      vi.advanceTimersByTime(0)
    })

    expect(result.current).toBe('updated')
  })

  it('works with number type', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 42, delay: 200 } }
    )

    expect(result.current).toBe(42)

    rerender({ value: 100, delay: 200 })
    expect(result.current).toBe(42)

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toBe(100)
  })

  it('works with object type', () => {
    const initial = { name: 'Alice' }
    const updated = { name: 'Bob' }

    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: initial, delay: 200 } }
    )

    expect(result.current).toEqual({ name: 'Alice' })

    rerender({ value: updated, delay: 200 })
    expect(result.current).toEqual({ name: 'Alice' })

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toEqual({ name: 'Bob' })
  })

  it('works with array type', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: [1, 2, 3], delay: 200 } }
    )

    expect(result.current).toEqual([1, 2, 3])

    rerender({ value: [4, 5, 6], delay: 200 })

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toEqual([4, 5, 6])
  })

  it('cleans up timeout on unmount', () => {
    const clearTimeoutSpy = vi.spyOn(global, 'clearTimeout')

    const { unmount, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'initial', delay: 500 } }
    )

    // Trigger a value change to set up a pending timeout
    rerender({ value: 'updated', delay: 500 })

    // Unmount before timeout fires
    unmount()

    // clearTimeout should have been called during cleanup
    expect(clearTimeoutSpy).toHaveBeenCalled()

    clearTimeoutSpy.mockRestore()
  })

  it('handles delay change', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: 'test', delay: 500 } }
    )

    expect(result.current).toBe('test')

    // Change the delay value (this should reset the timer)
    rerender({ value: 'test', delay: 200 })

    // Advance by original delay minus some time
    act(() => {
      vi.advanceTimersByTime(200)
    })

    // Value remains the same since same value was passed
    expect(result.current).toBe('test')
  })

  it('handles null and undefined values', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: null as string | null, delay: 200 } }
    )

    expect(result.current).toBeNull()

    rerender({ value: 'not null', delay: 200 })

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toBe('not null')

    rerender({ value: null, delay: 200 })

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toBeNull()
  })

  it('handles boolean values', () => {
    const { result, rerender } = renderHook(
      ({ value, delay }) => useDebounce(value, delay),
      { initialProps: { value: false, delay: 200 } }
    )

    expect(result.current).toBe(false)

    rerender({ value: true, delay: 200 })

    act(() => {
      vi.advanceTimersByTime(200)
    })

    expect(result.current).toBe(true)
  })
})
