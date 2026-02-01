/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { useFallbackPolling } from './useFallbackPolling';
import type { ConnectionState } from '../api/sse';

describe('useFallbackPolling', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // ============ Initialization tests ============

  describe('initialization', () => {
    it('returns expected shape with all methods and state', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connected',
          onPoll,
        })
      );

      expect(result.current).toHaveProperty('isActive');
      expect(result.current).toHaveProperty('timeUntilActive');
      expect(result.current).toHaveProperty('pollNow');
      expect(result.current).toHaveProperty('stopPolling');
      expect(typeof result.current.pollNow).toBe('function');
      expect(typeof result.current.stopPolling).toBe('function');
    });

    it('isActive starts as false', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connected',
          onPoll,
        })
      );

      expect(result.current.isActive).toBe(false);
    });

    it('timeUntilActive starts as null when connected', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connected',
          onPoll,
        })
      );

      expect(result.current.timeUntilActive).toBeNull();
    });
  });

  // ============ State tracking tests ============

  describe('state tracking', () => {
    it('polling remains inactive when wsState is connected', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connected',
          onPoll,
          activationThreshold: 1000,
        })
      );

      expect(result.current.isActive).toBe(false);

      // Advance time well past threshold
      act(() => {
        vi.advanceTimersByTime(5000);
      });

      expect(result.current.isActive).toBe(false);
      expect(onPoll).not.toHaveBeenCalled();
    });

    it('polling remains inactive when wsState is disconnected', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'disconnected',
          onPoll,
          activationThreshold: 1000,
        })
      );

      expect(result.current.isActive).toBe(false);

      // Advance time well past threshold
      act(() => {
        vi.advanceTimersByTime(5000);
      });

      expect(result.current.isActive).toBe(false);
      expect(onPoll).not.toHaveBeenCalled();
    });

    it('polling remains inactive when wsState is connecting', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connecting',
          onPoll,
          activationThreshold: 1000,
        })
      );

      expect(result.current.isActive).toBe(false);

      // Advance time well past threshold
      act(() => {
        vi.advanceTimersByTime(5000);
      });

      expect(result.current.isActive).toBe(false);
      expect(onPoll).not.toHaveBeenCalled();
    });

    it('isActive becomes true after threshold in reconnecting state', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
        })
      );

      expect(result.current.isActive).toBe(false);

      // Advance time just before threshold
      act(() => {
        vi.advanceTimersByTime(900);
      });

      expect(result.current.isActive).toBe(false);

      // Advance past threshold
      act(() => {
        vi.advanceTimersByTime(200);
      });

      expect(result.current.isActive).toBe(true);
    });

    it('isActive becomes false when wsState becomes connected', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 1000,
          }),
        { initialProps: { wsState: 'reconnecting' as ConnectionState } }
      );

      // Activate polling
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(result.current.isActive).toBe(true);

      // WebSocket reconnects
      rerender({ wsState: 'connected' });

      expect(result.current.isActive).toBe(false);
    });

    it('isActive becomes false when wsState becomes disconnected', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 1000,
          }),
        { initialProps: { wsState: 'reconnecting' as ConnectionState } }
      );

      // Activate polling
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(result.current.isActive).toBe(true);

      // Manual disconnect
      rerender({ wsState: 'disconnected' });

      expect(result.current.isActive).toBe(false);
    });
  });

  // ============ Threshold timer tests ============

  describe('threshold timer', () => {
    it('timer starts when entering reconnecting state', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 5000,
          }),
        { initialProps: { wsState: 'connected' as ConnectionState } }
      );

      expect(result.current.timeUntilActive).toBeNull();

      // Enter reconnecting state
      rerender({ wsState: 'reconnecting' });

      // Should have started countdown
      expect(result.current.timeUntilActive).toBe(5000);
    });

    it('timer cancels when leaving reconnecting state before threshold', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 5000,
          }),
        { initialProps: { wsState: 'reconnecting' as ConnectionState } }
      );

      // Start countdown
      expect(result.current.timeUntilActive).toBe(5000);

      act(() => {
        vi.advanceTimersByTime(2000);
      });

      // WebSocket recovers before threshold
      rerender({ wsState: 'connected' });

      // Timer should be cleared
      expect(result.current.timeUntilActive).toBeNull();
      expect(result.current.isActive).toBe(false);
    });

    it('timeUntilActive reflects remaining time', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 5000,
        })
      );

      expect(result.current.timeUntilActive).toBe(5000);

      // Advance by 1 second
      act(() => {
        vi.advanceTimersByTime(1000);
      });

      expect(result.current.timeUntilActive).toBe(4000);

      // Advance by another 2 seconds
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      expect(result.current.timeUntilActive).toBe(2000);
    });

    it('custom activationThreshold is respected', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 10000,
        })
      );

      // Should not activate before custom threshold
      act(() => {
        vi.advanceTimersByTime(5000);
      });

      expect(result.current.isActive).toBe(false);

      // Should activate after custom threshold
      act(() => {
        vi.advanceTimersByTime(5100);
      });

      expect(result.current.isActive).toBe(true);
    });
  });

  // ============ Polling interval tests ============

  describe('polling interval', () => {
    it('onPoll called immediately on activation', () => {
      const onPoll = vi.fn();
      renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 5000,
        })
      );

      expect(onPoll).not.toHaveBeenCalled();

      // Activate polling
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(onPoll).toHaveBeenCalledTimes(1);
    });

    it('onPoll callback is called at pollInterval', async () => {
      const onPoll = vi.fn().mockResolvedValue(undefined);
      renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 2000,
        })
      );

      // Activate polling
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // First poll interval
      await act(async () => {
        vi.advanceTimersByTime(2000);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(2);

      // Second poll interval
      await act(async () => {
        vi.advanceTimersByTime(2000);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(3);
    });

    it('custom pollInterval is respected', async () => {
      const onPoll = vi.fn().mockResolvedValue(undefined);
      renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 3000,
        })
      );

      // Activate polling
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // Advance less than poll interval
      await act(async () => {
        vi.advanceTimersByTime(2500);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // Advance past poll interval
      await act(async () => {
        vi.advanceTimersByTime(600);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(2);
    });

    it('polling stops when isActive becomes false', () => {
      const onPoll = vi.fn();
      const { rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 1000,
            pollInterval: 2000,
          }),
        { initialProps: { wsState: 'reconnecting' as ConnectionState } }
      );

      // Activate polling
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // WebSocket recovers
      rerender({ wsState: 'connected' });

      // Advance time - no more polls should happen
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(onPoll).toHaveBeenCalledTimes(1);
    });
  });

  // ============ Manual control tests ============

  describe('manual controls', () => {
    it('pollNow() calls onPoll when active', async () => {
      const onPoll = vi.fn().mockResolvedValue(undefined);
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 30000,
        })
      );

      // Activate polling
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // Manually trigger poll
      await act(async () => {
        result.current.pollNow();
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(2);
    });

    it('pollNow() is no-op when not active', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'connected',
          onPoll,
          activationThreshold: 1000,
        })
      );

      expect(result.current.isActive).toBe(false);

      // Try to manually trigger poll
      act(() => {
        result.current.pollNow();
      });

      expect(onPoll).not.toHaveBeenCalled();
    });

    it('stopPolling() stops active polling', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 2000,
        })
      );

      // Activate polling
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(result.current.isActive).toBe(true);
      expect(onPoll).toHaveBeenCalledTimes(1);

      // Stop polling
      act(() => {
        result.current.stopPolling();
      });

      expect(result.current.isActive).toBe(false);

      // Advance time - no more polls
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(onPoll).toHaveBeenCalledTimes(1);
    });

    it('stopPolling() during countdown prevents activation', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 5000,
          pollInterval: 2000,
        })
      );

      // Verify countdown started
      expect(result.current.timeUntilActive).toBe(5000);

      // Wait part of the threshold
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      expect(result.current.isActive).toBe(false);
      expect(result.current.timeUntilActive).toBe(3000);

      // Stop polling during countdown
      act(() => {
        result.current.stopPolling();
      });

      // Countdown should be cleared
      expect(result.current.timeUntilActive).toBeNull();
      expect(result.current.isActive).toBe(false);

      // Advance past original threshold - polling should NOT activate
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(result.current.isActive).toBe(false);
      expect(onPoll).not.toHaveBeenCalled();
    });
  });

  // ============ Cleanup tests ============

  describe('cleanup', () => {
    it('timers cleared on unmount', () => {
      const clearTimeoutSpy = vi.spyOn(global, 'clearTimeout');
      const clearIntervalSpy = vi.spyOn(global, 'clearInterval');

      const onPoll = vi.fn();
      const { unmount } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 5000,
        })
      );

      // Unmount before threshold
      unmount();

      // Timers should have been cleared
      expect(clearTimeoutSpy).toHaveBeenCalled();
      expect(clearIntervalSpy).toHaveBeenCalled();

      clearTimeoutSpy.mockRestore();
      clearIntervalSpy.mockRestore();
    });

    it('no state updates after unmount', () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});

      const onPoll = vi.fn();
      const { unmount } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
        })
      );

      // Unmount
      unmount();

      // Advance time past threshold - should not cause state update errors
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      // No React state update warnings should occur
      // (In strict mode, React would warn about state updates after unmount)
      expect(consoleErrorSpy).not.toHaveBeenCalled();

      consoleErrorSpy.mockRestore();
    });

    it('timers cleared when enabled becomes false', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ enabled }) =>
          useFallbackPolling({
            wsState: 'reconnecting',
            onPoll,
            activationThreshold: 5000,
            enabled,
          }),
        { initialProps: { enabled: true } }
      );

      expect(result.current.timeUntilActive).toBe(5000);

      // Disable polling
      rerender({ enabled: false });

      expect(result.current.timeUntilActive).toBeNull();
      expect(result.current.isActive).toBe(false);

      // Advance time - nothing should happen
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(onPoll).not.toHaveBeenCalled();
    });
  });

  // ============ Error handling tests ============

  describe('error handling', () => {
    it('onPoll error does not crash the hook', async () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const onPoll = vi.fn().mockRejectedValue(new Error('Poll failed'));

      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 2000,
        })
      );

      // Activate polling - this triggers immediate poll that will fail
      await act(async () => {
        vi.advanceTimersByTime(1100);
        // Allow promise rejection to be handled
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);
      expect(consoleErrorSpy).toHaveBeenCalledWith(
        '[useFallbackPolling] Poll error:',
        expect.any(Error)
      );

      // Hook should still be active
      expect(result.current.isActive).toBe(true);

      consoleErrorSpy.mockRestore();
    });

    it('onPoll error does not stop subsequent polls', async () => {
      const consoleErrorSpy = vi.spyOn(console, 'error').mockImplementation(() => {});
      const onPoll = vi
        .fn()
        .mockRejectedValueOnce(new Error('First poll failed'))
        .mockResolvedValueOnce(undefined)
        .mockResolvedValueOnce(undefined);

      renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          pollInterval: 2000,
        })
      );

      // Activate polling - first poll fails
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      // Second poll should still happen
      await act(async () => {
        vi.advanceTimersByTime(2000);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(2);

      // Third poll should still happen
      await act(async () => {
        vi.advanceTimersByTime(2000);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(3);

      consoleErrorSpy.mockRestore();
    });
  });

  // ============ Enabled flag tests ============

  describe('enabled flag', () => {
    it('enabled=false disables all behavior', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 1000,
          enabled: false,
        })
      );

      expect(result.current.isActive).toBe(false);
      expect(result.current.timeUntilActive).toBeNull();

      // Advance time well past threshold
      act(() => {
        vi.advanceTimersByTime(10000);
      });

      expect(result.current.isActive).toBe(false);
      expect(onPoll).not.toHaveBeenCalled();
    });

    it('enabling after disable resumes normal behavior', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ enabled }) =>
          useFallbackPolling({
            wsState: 'reconnecting',
            onPoll,
            activationThreshold: 1000,
            enabled,
          }),
        { initialProps: { enabled: false } }
      );

      expect(result.current.isActive).toBe(false);

      // Enable polling
      rerender({ enabled: true });

      // Should start countdown
      expect(result.current.timeUntilActive).toBe(1000);

      // Activate
      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(result.current.isActive).toBe(true);
      expect(onPoll).toHaveBeenCalled();
    });
  });

  // ============ Callback ref updates ============

  describe('callback ref updates', () => {
    it('callback ref updates do not cause re-polling', async () => {
      const onPoll1 = vi.fn().mockResolvedValue(undefined);
      const onPoll2 = vi.fn().mockResolvedValue(undefined);

      const { rerender } = renderHook(
        ({ onPoll }) =>
          useFallbackPolling({
            wsState: 'reconnecting',
            onPoll,
            activationThreshold: 1000,
            pollInterval: 5000,
          }),
        { initialProps: { onPoll: onPoll1 } }
      );

      // Activate polling
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll1).toHaveBeenCalledTimes(1);

      // Update callback
      rerender({ onPoll: onPoll2 });

      // No additional poll should have been triggered
      expect(onPoll1).toHaveBeenCalledTimes(1);
      expect(onPoll2).toHaveBeenCalledTimes(0);

      // Next poll should use new callback
      await act(async () => {
        vi.advanceTimersByTime(5000);
        await Promise.resolve();
      });

      expect(onPoll1).toHaveBeenCalledTimes(1);
      expect(onPoll2).toHaveBeenCalledTimes(1);
    });
  });

  // ============ Edge cases ============

  describe('edge cases', () => {
    it('initial mount in reconnecting state starts countdown immediately', () => {
      const onPoll = vi.fn();
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
          activationThreshold: 5000,
        })
      );

      // Should have started countdown immediately
      expect(result.current.timeUntilActive).toBe(5000);
    });

    it('wsState flicker between states does not cause issues', () => {
      const onPoll = vi.fn();
      const { result, rerender } = renderHook(
        ({ wsState }) =>
          useFallbackPolling({
            wsState,
            onPoll,
            activationThreshold: 5000,
          }),
        { initialProps: { wsState: 'reconnecting' as ConnectionState } }
      );

      // Start countdown
      act(() => {
        vi.advanceTimersByTime(2000);
      });

      // Brief recovery
      rerender({ wsState: 'connected' });
      expect(result.current.timeUntilActive).toBeNull();

      // Immediately back to reconnecting
      rerender({ wsState: 'reconnecting' });
      expect(result.current.timeUntilActive).toBe(5000);

      // Full countdown should be required
      act(() => {
        vi.advanceTimersByTime(4000);
      });

      expect(result.current.isActive).toBe(false);

      act(() => {
        vi.advanceTimersByTime(1100);
      });

      expect(result.current.isActive).toBe(true);
    });

    it('uses default values when not provided', async () => {
      const onPoll = vi.fn().mockResolvedValue(undefined);
      const { result } = renderHook(() =>
        useFallbackPolling({
          wsState: 'reconnecting',
          onPoll,
        })
      );

      // Default activationThreshold is 30000ms
      expect(result.current.timeUntilActive).toBe(30000);

      // Not active yet after 29 seconds
      await act(async () => {
        vi.advanceTimersByTime(29000);
        await Promise.resolve();
      });

      expect(result.current.isActive).toBe(false);

      // Active after 30 seconds
      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(result.current.isActive).toBe(true);
      expect(onPoll).toHaveBeenCalledTimes(1);

      // Default pollInterval is 30000ms
      await act(async () => {
        vi.advanceTimersByTime(29000);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(1);

      await act(async () => {
        vi.advanceTimersByTime(1100);
        await Promise.resolve();
      });

      expect(onPoll).toHaveBeenCalledTimes(2);
    });
  });
});
