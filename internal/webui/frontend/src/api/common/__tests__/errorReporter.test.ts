import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Fresh module import helper — each call returns a fresh errorReporter
// with zeroed module-level state (consecutiveFailures, circuitOpenUntil, recentErrors).
async function loadModule() {
  const mod = await import("../errorReporter");
  return mod;
}

// ---------------------------------------------------------------------------
// Globals setup
// ---------------------------------------------------------------------------

// Provide navigator.userAgent for the payload builder
const FAKE_UA = "TestAgent/1.0";
vi.stubGlobal("navigator", { userAgent: FAKE_UA });

// Provide a minimal window for initErrorReporter
function makeWindow() {
  const listeners: Record<string, ((e: unknown) => void)[]> = {};
  return {
    addEventListener: vi.fn((type: string, handler: (e: unknown) => void) => {
      listeners[type] = listeners[type] ?? [];
      listeners[type]!.push(handler);
    }),
    _fire(type: string, event: unknown) {
      for (const fn of listeners[type] ?? []) {
        fn(event);
      }
    },
  };
}

describe("errorReporter", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.useFakeTimers();
    vi.resetModules();

    fetchMock = vi.fn().mockResolvedValue({ ok: true });
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  // -----------------------------------------------------------------------
  // 1. reportError sends correct payload via fetch
  // -----------------------------------------------------------------------
  it("sends a POST to /api/client-errors with the right payload shape", async () => {
    const { reportError } = await loadModule();

    const err = new Error("boom");
    err.stack = "Error: boom\n    at foo.ts:1:1";

    reportError("global-error", err, {
      url: "http://localhost/app",
      line: 42,
      col: 7,
    });

    // sendReport is fire-and-forget; flush microtasks so fetch is called
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledOnce();

    const [url, init] = fetchMock.mock.calls[0]!;
    expect(url).toBe("/api/client-errors");
    expect(init.method).toBe("POST");
    expect(init.headers).toEqual({ "Content-Type": "application/json" });
    expect(init.signal).toBeInstanceOf(AbortSignal);

    const body = JSON.parse(init.body as string);
    expect(body.type).toBe("global-error");
    expect(body.message).toBe("boom");
    expect(body.stack).toBe(err.stack);
    expect(body.url).toBe("http://localhost/app");
    expect(body.line).toBe(42);
    expect(body.col).toBe(7);
    expect(body.userAgent).toBe(FAKE_UA);
    expect(body.timestamp).toBeTruthy();
  });

  // -----------------------------------------------------------------------
  // 2. Dedup within 5s window
  // -----------------------------------------------------------------------
  it("deduplicates identical type+message within 5s window", async () => {
    const { reportError } = await loadModule();

    reportError("global-error", new Error("dup"));
    reportError("global-error", new Error("dup")); // duplicate

    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledOnce();
  });

  // -----------------------------------------------------------------------
  // 3. Dedup expires after 5s
  // -----------------------------------------------------------------------
  it("allows same error after 5s dedup window expires", async () => {
    const { reportError } = await loadModule();

    reportError("global-error", new Error("dup"));
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledOnce();

    // Advance past the 5s dedup window
    vi.advanceTimersByTime(5_001);

    reportError("global-error", new Error("dup"));
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  // -----------------------------------------------------------------------
  // 4. Different error types bypass dedup
  // -----------------------------------------------------------------------
  it("does not dedup errors with different types", async () => {
    const { reportError } = await loadModule();

    reportError("global-error", new Error("same"));
    reportError("api-error", new Error("same"));

    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  // -----------------------------------------------------------------------
  // 5. Circuit breaker opens after 3 consecutive failures
  // -----------------------------------------------------------------------
  it("opens circuit breaker after 3 consecutive fetch failures", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 500 });

    const { reportError } = await loadModule();

    // Three unique errors to avoid dedup
    reportError("global-error", new Error("fail-1"));
    await vi.advanceTimersByTimeAsync(0);

    reportError("global-error", new Error("fail-2"));
    await vi.advanceTimersByTimeAsync(0);

    reportError("global-error", new Error("fail-3"));
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(3);

    // Fourth call should be suppressed by circuit breaker
    reportError("global-error", new Error("fail-4"));
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  // -----------------------------------------------------------------------
  // 6. Circuit breaker resets after 60s cooldown
  // -----------------------------------------------------------------------
  it("re-allows reports after 60s circuit breaker cooldown", async () => {
    fetchMock.mockResolvedValue({ ok: false, status: 500 });
    const { reportError } = await loadModule();

    // Trip the breaker
    for (let i = 0; i < 3; i++) {
      reportError("global-error", new Error(`fail-${i}`));
      await vi.advanceTimersByTimeAsync(0);
    }
    expect(fetchMock).toHaveBeenCalledTimes(3);

    // Advance past 60s cooldown
    vi.advanceTimersByTime(60_001);

    // Succeeding fetch now
    fetchMock.mockResolvedValue({ ok: true });
    reportError("global-error", new Error("after-cooldown"));
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  // -----------------------------------------------------------------------
  // 7. Circuit breaker resets consecutiveFailures on success
  // -----------------------------------------------------------------------
  it("resets consecutive failures counter on a successful report", async () => {
    const { reportError } = await loadModule();

    // Two failures
    fetchMock.mockResolvedValue({ ok: false, status: 500 });
    reportError("global-error", new Error("f1"));
    await vi.advanceTimersByTimeAsync(0);
    reportError("global-error", new Error("f2"));
    await vi.advanceTimersByTimeAsync(0);

    // One success resets the counter
    fetchMock.mockResolvedValue({ ok: true });
    reportError("global-error", new Error("ok"));
    await vi.advanceTimersByTimeAsync(0);

    // Two more failures should NOT trip the breaker (counter was reset)
    fetchMock.mockResolvedValue({ ok: false, status: 500 });
    reportError("global-error", new Error("f3"));
    await vi.advanceTimersByTimeAsync(0);
    reportError("global-error", new Error("f4"));
    await vi.advanceTimersByTimeAsync(0);

    // All 5 calls should have gone through (breaker not open)
    expect(fetchMock).toHaveBeenCalledTimes(5);

    // A 6th call should also go through since we only have 2 consecutive failures
    reportError("global-error", new Error("f5"));
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(6);
  });

  // -----------------------------------------------------------------------
  // 8. Timeout after 5s via AbortController
  // -----------------------------------------------------------------------
  it("aborts fetch after 5s timeout", async () => {
    // fetch that never resolves until aborted
    fetchMock.mockImplementation(
      (_url: string, init: { signal: AbortSignal }) =>
        new Promise((_resolve, reject) => {
          init.signal.addEventListener("abort", () => {
            reject(
              new DOMException("The operation was aborted.", "AbortError"),
            );
          });
        }),
    );

    const { reportError } = await loadModule();

    reportError("global-error", new Error("slow"));

    // Advance past the 5s report timeout
    await vi.advanceTimersByTimeAsync(5_001);

    expect(fetchMock).toHaveBeenCalledOnce();

    // The abort should have incremented consecutiveFailures (catch branch),
    // but no throw should propagate — reportError is fire-and-forget.
    // Send another to confirm the module didn't blow up
    reportError("global-error", new Error("after-timeout"));
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  // -----------------------------------------------------------------------
  // 9. Non-Error objects handled
  // -----------------------------------------------------------------------
  describe("normalizeMessage for non-Error objects", () => {
    it("handles a plain string", async () => {
      const { reportError } = await loadModule();

      reportError("global-error", "string error");
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.message).toBe("string error");
      expect(body.stack).toBeUndefined();
    });

    it("handles an object via String()", async () => {
      const { reportError } = await loadModule();

      reportError("global-error", { code: 42 });
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.message).toBe("[object Object]");
    });

    it("handles null", async () => {
      const { reportError } = await loadModule();

      reportError("global-error", null);
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.message).toBe("null");
    });

    it("handles undefined", async () => {
      const { reportError } = await loadModule();

      reportError("global-error", undefined);
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.message).toBe("undefined");
    });

    it("handles a number", async () => {
      const { reportError } = await loadModule();

      reportError("api-error", 404);
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.message).toBe("404");
    });
  });

  // -----------------------------------------------------------------------
  // 10. initErrorReporter installs window event listeners
  // -----------------------------------------------------------------------
  describe("initErrorReporter", () => {
    it("registers 'error' and 'unhandledrejection' listeners", async () => {
      const fakeWindow = makeWindow();
      vi.stubGlobal("window", fakeWindow);

      const { initErrorReporter } = await loadModule();
      initErrorReporter();

      expect(fakeWindow.addEventListener).toHaveBeenCalledWith(
        "error",
        expect.any(Function),
      );
      expect(fakeWindow.addEventListener).toHaveBeenCalledWith(
        "unhandledrejection",
        expect.any(Function),
      );
    });

    it("error listener reports global-error with url/line/col from ErrorEvent", async () => {
      const fakeWindow = makeWindow();
      vi.stubGlobal("window", fakeWindow);

      const { initErrorReporter } = await loadModule();
      initErrorReporter();

      const err = new Error("window boom");
      const fakeEvent = {
        error: err,
        message: "Uncaught Error: window boom",
        filename: "app.js",
        lineno: 10,
        colno: 5,
      };
      fakeWindow._fire("error", fakeEvent);
      await vi.advanceTimersByTimeAsync(0);

      expect(fetchMock).toHaveBeenCalledOnce();
      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.type).toBe("global-error");
      expect(body.message).toBe("window boom");
      expect(body.url).toBe("app.js");
      expect(body.line).toBe(10);
      expect(body.col).toBe(5);
    });

    it("error listener falls back to event.message when event.error is null", async () => {
      const fakeWindow = makeWindow();
      vi.stubGlobal("window", fakeWindow);

      const mod = await loadModule();
      mod.initErrorReporter();

      const fakeEvent = {
        error: null,
        message: "Script error.",
        filename: "unknown",
        lineno: 0,
        colno: 0,
      };
      fakeWindow._fire("error", fakeEvent);
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.type).toBe("global-error");
      expect(body.message).toBe("Script error.");
    });

    it("unhandledrejection listener reports unhandled-rejection type", async () => {
      const fakeWindow = makeWindow();
      vi.stubGlobal("window", fakeWindow);

      const { initErrorReporter } = await loadModule();
      initErrorReporter();

      const fakeEvent = {
        reason: new Error("rejected"),
      };
      fakeWindow._fire("unhandledrejection", fakeEvent);
      await vi.advanceTimersByTimeAsync(0);

      expect(fetchMock).toHaveBeenCalledOnce();
      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.type).toBe("unhandled-rejection");
      expect(body.message).toBe("rejected");
    });
  });

  // -----------------------------------------------------------------------
  // 11. Error normalization edge cases
  // -----------------------------------------------------------------------
  describe("payload construction edge cases", () => {
    it("uses extra.stack when error is not an Error instance", async () => {
      const { reportError } = await loadModule();

      reportError("api-error", "string error", {
        stack: "fake stack trace",
      });
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.stack).toBe("fake stack trace");
    });

    it("prefers Error.stack over extra.stack", async () => {
      const { reportError } = await loadModule();

      const err = new Error("has stack");
      err.stack = "real stack";

      reportError("react-error", err, { stack: "extra stack" });
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.stack).toBe("real stack");
    });

    it("appends componentStack to existing stack", async () => {
      const { reportError } = await loadModule();

      const err = new Error("react crash");
      err.stack = "Error: react crash\n    at Comp";

      reportError("react-error", err, {
        componentStack: "\n    at App\n    at Root",
      });
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.stack).toBe(
        "Error: react crash\n    at Comp\n\nComponent Stack:\n    at App\n    at Root",
      );
    });

    it("creates stack from componentStack alone when no error stack", async () => {
      const { reportError } = await loadModule();

      reportError("react-error", "no stack error", {
        componentStack: "\n    at Widget",
      });
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.stack).toBe("\n\nComponent Stack:\n    at Widget");
    });

    it("omits url/line/col when not provided in extra", async () => {
      const { reportError } = await loadModule();

      reportError("global-error", new Error("minimal"));
      await vi.advanceTimersByTimeAsync(0);

      const body = JSON.parse(
        (fetchMock.mock.calls[0]![1] as { body: string }).body,
      );
      expect(body.url).toBeUndefined();
      expect(body.line).toBeUndefined();
      expect(body.col).toBeUndefined();
    });
  });

  // -----------------------------------------------------------------------
  // Circuit breaker with network errors (catch branch)
  // -----------------------------------------------------------------------
  it("opens circuit breaker after 3 consecutive network errors", async () => {
    fetchMock.mockRejectedValue(new TypeError("Failed to fetch"));

    const { reportError } = await loadModule();

    reportError("global-error", new Error("net-1"));
    await vi.advanceTimersByTimeAsync(0);
    reportError("global-error", new Error("net-2"));
    await vi.advanceTimersByTimeAsync(0);
    reportError("global-error", new Error("net-3"));
    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(3);

    // Breaker should be open
    reportError("global-error", new Error("net-4"));
    await vi.advanceTimersByTimeAsync(0);
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  // -----------------------------------------------------------------------
  // reportError never throws
  // -----------------------------------------------------------------------
  it("never throws even if internal logic has an unexpected issue", async () => {
    const { reportError } = await loadModule();

    // Even with a broken fetch, reportError should not throw
    fetchMock.mockImplementation(() => {
      throw new Error("sync explosion");
    });

    expect(() => {
      reportError("global-error", new Error("should not throw"));
    }).not.toThrow();
  });

  // -----------------------------------------------------------------------
  // Dedup key uses both type and message
  // -----------------------------------------------------------------------
  it("treats same message but different type as distinct for dedup", async () => {
    const { reportError } = await loadModule();

    reportError("global-error", "same msg");
    reportError("react-error", "same msg");
    reportError("api-error", "same msg");

    await vi.advanceTimersByTimeAsync(0);

    expect(fetchMock).toHaveBeenCalledTimes(3);
  });
});
