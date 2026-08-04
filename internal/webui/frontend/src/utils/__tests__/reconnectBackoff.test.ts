/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for reconnectBackoff utility.
 */

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import {
  calculateBackoffDelay,
  startAutoReconnect,
  DEFAULT_RECONNECT_CONFIG,
  type ReconnectConfig,
  type ReconnectState,
} from "../reconnectBackoff";

describe("calculateBackoffDelay", () => {
  it("returns correct base delay for attempt 0 (~1000ms ±25%)", () => {
    // With default jitterFactor=0.5, range is [1-0.25, 1+0.25] = [0.75, 1.25]
    // So for attempt 0: baseDelay * 2^0 = 1000, jittered to [750, 1250]
    for (let i = 0; i < 50; i++) {
      const delay = calculateBackoffDelay(0);
      expect(delay).toBeGreaterThanOrEqual(750);
      expect(delay).toBeLessThanOrEqual(1250);
    }
  });

  it("delay doubles each attempt: attempt 1 ~2000ms, attempt 2 ~4000ms", () => {
    for (let i = 0; i < 50; i++) {
      const delay1 = calculateBackoffDelay(1);
      expect(delay1).toBeGreaterThanOrEqual(1500);
      expect(delay1).toBeLessThanOrEqual(2500);

      const delay2 = calculateBackoffDelay(2);
      expect(delay2).toBeGreaterThanOrEqual(3000);
      expect(delay2).toBeLessThanOrEqual(5000);
    }
  });

  it("delay is capped at maxDelay (30000ms) for high attempt numbers", () => {
    // 2^20 * 1000 = 1048576000, but capped at 30000
    // Jittered: [30000*0.75, 30000*1.25] = [22500, 37500]
    for (let i = 0; i < 50; i++) {
      const delay = calculateBackoffDelay(20);
      expect(delay).toBeGreaterThanOrEqual(22500);
      expect(delay).toBeLessThanOrEqual(37500);
    }
  });

  it("jitter stays within expected bounds over many iterations", () => {
    const config: ReconnectConfig = {
      ...DEFAULT_RECONNECT_CONFIG,
      jitterFactor: 0.5,
    };

    let min = Infinity;
    let max = -Infinity;

    for (let i = 0; i < 1000; i++) {
      const delay = calculateBackoffDelay(0, config);
      min = Math.min(min, delay);
      max = Math.max(max, delay);
    }

    // With 1000 iterations, we should see values near both edges
    // Base is 1000, jitter range is [750, 1250]
    expect(min).toBeGreaterThanOrEqual(750);
    expect(max).toBeLessThanOrEqual(1250);
    // Ensure we're actually getting spread (not just returning the same value)
    expect(max - min).toBeGreaterThan(100);
  });

  it("custom config values work", () => {
    const config: ReconnectConfig = {
      baseDelay: 500,
      maxDelay: 5000,
      maxAttempts: 5,
      jitterFactor: 0, // No jitter — exact values
    };

    expect(calculateBackoffDelay(0, config)).toBe(500);
    expect(calculateBackoffDelay(1, config)).toBe(1000);
    expect(calculateBackoffDelay(2, config)).toBe(2000);
    expect(calculateBackoffDelay(3, config)).toBe(4000);
    // 500 * 2^4 = 8000, capped at 5000
    expect(calculateBackoffDelay(4, config)).toBe(5000);
  });
});

describe("startAutoReconnect", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // Use zero jitter so delays are deterministic
  const deterministicConfig: ReconnectConfig = {
    baseDelay: 1000,
    maxDelay: 30000,
    maxAttempts: 10,
    jitterFactor: 0,
  };

  it("calls connectFn on first scheduled attempt", () => {
    const connectFn = vi.fn().mockReturnValue(true);
    const onStateChange = vi.fn();

    startAutoReconnect(connectFn, onStateChange, deterministicConfig);

    // connectFn should not have been called yet (it's scheduled via setTimeout)
    expect(connectFn).not.toHaveBeenCalled();

    // Advance by the delay for attempt 0: 1000ms (no jitter)
    vi.advanceTimersByTime(1000);

    expect(connectFn).toHaveBeenCalledTimes(1);
  });

  it("increments attempt counter on each failure", async () => {
    const connectFn = vi.fn().mockReturnValue(false);
    const onStateChange = vi.fn();

    startAutoReconnect(connectFn, onStateChange, deterministicConfig);

    // Attempt 0 scheduled: onStateChange called with attempt=0
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ attempt: 0, gaveUp: false }),
    );

    // Advance past attempt 0 delay (1000ms) — use async to flush microtasks
    await vi.advanceTimersByTimeAsync(1000);
    expect(connectFn).toHaveBeenCalledTimes(1);

    // After failure, attempt increments to 1 and scheduleNext is called again
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ attempt: 1, gaveUp: false }),
    );

    // Advance past attempt 1 delay (2000ms)
    await vi.advanceTimersByTimeAsync(2000);
    expect(connectFn).toHaveBeenCalledTimes(2);

    // After second failure, attempt increments to 2
    expect(onStateChange).toHaveBeenCalledWith(
      expect.objectContaining({ attempt: 2, gaveUp: false }),
    );
  });

  it("cancel function stops the loop (no more connectFn calls)", () => {
    const connectFn = vi.fn().mockReturnValue(false);
    const onStateChange = vi.fn();

    const cancel = startAutoReconnect(
      connectFn,
      onStateChange,
      deterministicConfig,
    );

    // Cancel before the first timer fires
    cancel();

    // Advance well past the first delay
    vi.advanceTimersByTime(50000);

    expect(connectFn).not.toHaveBeenCalled();
  });

  it("state callback receives correct attempt numbers and nextRetryAt", async () => {
    const connectFn = vi.fn().mockReturnValue(false);
    const onStateChange = vi.fn();

    // Set Date.now() to a known value
    vi.setSystemTime(new Date("2025-01-01T00:00:00.000Z"));

    startAutoReconnect(connectFn, onStateChange, deterministicConfig);

    // First call: attempt 0, nextRetryAt = now + 1000
    expect(onStateChange).toHaveBeenCalledTimes(1);
    const firstState: ReconnectState = onStateChange.mock.calls[0][0];
    expect(firstState.attempt).toBe(0);
    expect(firstState.nextRetryAt).toBe(Date.now() + 1000);
    expect(firstState.gaveUp).toBe(false);

    // Trigger attempt 0 — use async to flush microtasks
    await vi.advanceTimersByTimeAsync(1000);

    // Second call: attempt 1, nextRetryAt = now + 2000
    expect(onStateChange).toHaveBeenCalledTimes(2);
    const secondState: ReconnectState = onStateChange.mock.calls[1][0];
    expect(secondState.attempt).toBe(1);
    expect(secondState.nextRetryAt).toBe(Date.now() + 2000);
    expect(secondState.gaveUp).toBe(false);
  });

  it("stops after maxAttempts and reports gaveUp=true", async () => {
    const connectFn = vi.fn().mockReturnValue(false);
    const onStateChange = vi.fn();

    const config: ReconnectConfig = {
      baseDelay: 100,
      maxDelay: 1000,
      maxAttempts: 3,
      jitterFactor: 0,
    };

    startAutoReconnect(connectFn, onStateChange, config);

    // Attempt 0: delay 100ms
    await vi.advanceTimersByTimeAsync(100);
    expect(connectFn).toHaveBeenCalledTimes(1);

    // Attempt 1: delay 200ms
    await vi.advanceTimersByTimeAsync(200);
    expect(connectFn).toHaveBeenCalledTimes(2);

    // Attempt 2: delay 400ms
    await vi.advanceTimersByTimeAsync(400);
    expect(connectFn).toHaveBeenCalledTimes(3);

    // After 3 failures, attempt is now 3 which equals maxAttempts
    // onStateChange should have been called with gaveUp=true
    const lastCall =
      onStateChange.mock.calls[onStateChange.mock.calls.length - 1][0];
    expect(lastCall.attempt).toBe(3);
    expect(lastCall.nextRetryAt).toBeNull();
    expect(lastCall.gaveUp).toBe(true);

    // No more connectFn calls even after waiting
    await vi.advanceTimersByTimeAsync(100000);
    expect(connectFn).toHaveBeenCalledTimes(3);
  });

  it("does not call connectFn after cancel", async () => {
    const connectFn = vi.fn().mockReturnValue(false);
    const onStateChange = vi.fn();

    const cancel = startAutoReconnect(
      connectFn,
      onStateChange,
      deterministicConfig,
    );

    // Let the first attempt fire
    await vi.advanceTimersByTimeAsync(1000);
    expect(connectFn).toHaveBeenCalledTimes(1);

    // Cancel after first failure
    cancel();

    // Advance timers well past what any subsequent attempt would need
    await vi.advanceTimersByTimeAsync(100000);

    // connectFn should not have been called again
    expect(connectFn).toHaveBeenCalledTimes(1);
  });

  describe("async connectFn", () => {
    it("async connectFn that resolves true stops the loop", async () => {
      const connectFn = vi.fn().mockReturnValue(Promise.resolve(true));
      const onStateChange = vi.fn();

      startAutoReconnect(connectFn, onStateChange, deterministicConfig);

      // Advance past attempt 0 delay (1000ms) — flush microtasks too
      await vi.advanceTimersByTimeAsync(1000);
      expect(connectFn).toHaveBeenCalledTimes(1);

      // Since connectFn resolved true, no next attempt should be scheduled
      // onStateChange should only have been called once (for attempt 0)
      expect(onStateChange).toHaveBeenCalledTimes(1);
      expect(onStateChange).toHaveBeenCalledWith(
        expect.objectContaining({ attempt: 0, gaveUp: false }),
      );

      // Advance well past any subsequent delay — no more calls
      await vi.advanceTimersByTimeAsync(100000);
      expect(connectFn).toHaveBeenCalledTimes(1);
    });

    it("async connectFn that resolves false schedules next attempt", async () => {
      const connectFn = vi.fn().mockReturnValue(Promise.resolve(false));
      const onStateChange = vi.fn();

      startAutoReconnect(connectFn, onStateChange, deterministicConfig);

      // Initial scheduleNext call: onStateChange with attempt=0
      expect(onStateChange).toHaveBeenCalledTimes(1);
      expect(onStateChange).toHaveBeenCalledWith(
        expect.objectContaining({ attempt: 0, gaveUp: false }),
      );

      // Advance past attempt 0 delay (1000ms)
      await vi.advanceTimersByTimeAsync(1000);
      expect(connectFn).toHaveBeenCalledTimes(1);

      // After resolving false, attempt increments to 1 and scheduleNext fires
      expect(onStateChange).toHaveBeenCalledTimes(2);
      expect(onStateChange).toHaveBeenCalledWith(
        expect.objectContaining({ attempt: 1, gaveUp: false }),
      );

      // Advance past attempt 1 delay (2000ms)
      await vi.advanceTimersByTimeAsync(2000);
      expect(connectFn).toHaveBeenCalledTimes(2);

      // Attempt increments to 2
      expect(onStateChange).toHaveBeenCalledTimes(3);
      expect(onStateChange).toHaveBeenCalledWith(
        expect.objectContaining({ attempt: 2, gaveUp: false }),
      );
    });

    it("async connectFn waits for resolution before scheduling next", async () => {
      let resolveConnect!: (value: boolean) => void;
      const pendingPromise = new Promise<boolean>((resolve) => {
        resolveConnect = resolve;
      });
      const connectFn = vi.fn().mockReturnValue(pendingPromise);
      const onStateChange = vi.fn();

      startAutoReconnect(connectFn, onStateChange, deterministicConfig);

      // Advance past attempt 0 delay — connectFn is called
      await vi.advanceTimersByTimeAsync(1000);
      expect(connectFn).toHaveBeenCalledTimes(1);

      // onStateChange still only called once (attempt 0 scheduled)
      // because the promise hasn't resolved yet
      expect(onStateChange).toHaveBeenCalledTimes(1);

      // Advance more time — next attempt should NOT be scheduled yet
      await vi.advanceTimersByTimeAsync(5000);
      expect(connectFn).toHaveBeenCalledTimes(1);
      expect(onStateChange).toHaveBeenCalledTimes(1);

      // Now resolve the promise with false
      resolveConnect(false);
      // Flush microtasks
      await vi.advanceTimersByTimeAsync(0);

      // Now attempt 1 should be scheduled
      expect(onStateChange).toHaveBeenCalledTimes(2);
      expect(onStateChange).toHaveBeenCalledWith(
        expect.objectContaining({ attempt: 1, gaveUp: false }),
      );
    });

    it("cancel during pending async connectFn prevents further scheduling", async () => {
      let resolveConnect!: (value: boolean) => void;
      const pendingPromise = new Promise<boolean>((resolve) => {
        resolveConnect = resolve;
      });
      const connectFn = vi.fn().mockReturnValue(pendingPromise);
      const onStateChange = vi.fn();

      const cancel = startAutoReconnect(
        connectFn,
        onStateChange,
        deterministicConfig,
      );

      // Advance past attempt 0 delay — connectFn is called, promise pending
      await vi.advanceTimersByTimeAsync(1000);
      expect(connectFn).toHaveBeenCalledTimes(1);

      // Cancel while the promise is still pending
      cancel();

      // Now resolve the promise with false
      resolveConnect(false);
      // Flush microtasks
      await vi.advanceTimersByTimeAsync(0);

      // onStateChange should only have been called once (for the initial scheduleNext)
      // The cancelled check inside the .then() should prevent scheduling
      expect(onStateChange).toHaveBeenCalledTimes(1);

      // No further attempts
      await vi.advanceTimersByTimeAsync(100000);
      expect(connectFn).toHaveBeenCalledTimes(1);
    });
  });
});
