import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { startAutoReconnect, type ReconnectState } from "../reconnectBackoff";

const config = {
  baseDelay: 1000,
  maxDelay: 30000,
  maxAttempts: 10,
  jitterFactor: 0, // deterministic delays for the test
};

describe("startAutoReconnect attempt carry-over", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it("resumes from a carried initial attempt instead of restarting at 0", async () => {
    const states: ReconnectState[] = [];
    const cancel = startAutoReconnect(
      () => false,
      (state) => states.push(state),
      config,
      3,
    );

    expect(states[0]?.attempt).toBe(3);
    const scheduledDelay = (states[0]?.nextRetryAt ?? 0) - Date.now();
    // attempt 3 with jitterFactor 0 → exactly baseDelay * 2^3
    expect(scheduledDelay).toBe(8000);

    await vi.advanceTimersByTimeAsync(8000);
    expect(states[1]?.attempt).toBe(4);
    cancel();
  });

  it("defaults to attempt 0 when no carry is given", () => {
    const states: ReconnectState[] = [];
    const cancel = startAutoReconnect(
      () => false,
      (state) => states.push(state),
      config,
    );
    expect(states[0]?.attempt).toBe(0);
    const scheduledDelay = (states[0]?.nextRetryAt ?? 0) - Date.now();
    expect(scheduledDelay).toBe(1000);
    cancel();
  });

  it("a carried attempt at the max gives up immediately", () => {
    const states: ReconnectState[] = [];
    const cancel = startAutoReconnect(
      () => false,
      (state) => states.push(state),
      config,
      10,
    );
    expect(states[0]?.gaveUp).toBe(true);
    cancel();
  });
});
