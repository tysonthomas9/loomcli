/**
 * @vitest-environment jsdom
 */

/**
 * Tests for the boot timeout pattern used in main.tsx.
 *
 * main.tsx has top-level side effects and unexported functions, so we
 * cannot import it directly.  Instead we replicate the Promise.race +
 * timer pattern in isolation and verify its semantics, plus confirm
 * that the BootError component renders the timeout message correctly.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { AppConfigError } from "@/api/common";
import { BootError } from "@/components/BootError";

// ---------------------------------------------------------------------------
// Helpers — mirror the pattern in main.tsx
// ---------------------------------------------------------------------------

const BOOT_TIMEOUT_MS = 10_000;

/**
 * Stripped-down replica of `bootAndRender` from main.tsx.  The `bootFn`
 * parameter replaces `doBootAndRender` so callers can control resolution
 * and rejection timing.
 */
async function bootWithTimeout(bootFn: () => Promise<void>): Promise<void> {
  let timeoutId: ReturnType<typeof setTimeout> | undefined;

  try {
    await Promise.race([
      bootFn(),
      new Promise<never>((_resolve, reject) => {
        timeoutId = setTimeout(() => {
          reject(new AppConfigError("Application boot timed out"));
        }, BOOT_TIMEOUT_MS);
      }),
    ]);
  } finally {
    if (timeoutId !== undefined) clearTimeout(timeoutId);
  }
}

// ---------------------------------------------------------------------------
// Promise.race timeout logic
// ---------------------------------------------------------------------------

describe("boot timeout pattern", () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("rejects with AppConfigError when boot never resolves and timeout elapses", async () => {
    const bootFn = () => new Promise<void>(() => {});

    const promise = bootWithTimeout(bootFn);

    // Register assertion before advancing timers to avoid unhandled rejection
    const assertion = expect(promise).rejects.toThrow(
      "Application boot timed out",
    );

    await vi.advanceTimersByTimeAsync(BOOT_TIMEOUT_MS);

    await assertion;
  });

  it("rejects with an AppConfigError instance on timeout", async () => {
    const bootFn = () => new Promise<void>(() => {});

    const promise = bootWithTimeout(bootFn);
    const assertion = expect(promise).rejects.toBeInstanceOf(AppConfigError);

    await vi.advanceTimersByTimeAsync(BOOT_TIMEOUT_MS);

    await assertion;
  });

  it("resolves when boot completes before timeout", async () => {
    const bootFn = () => Promise.resolve();

    await expect(bootWithTimeout(bootFn)).resolves.toBeUndefined();
  });

  it("surfaces original error when boot rejects before timeout", async () => {
    const originalError = new Error("fetch failed");
    const bootFn = () => Promise.reject(originalError);

    await expect(bootWithTimeout(bootFn)).rejects.toThrow("fetch failed");
  });

  it("preserves original error type when boot rejects before timeout", async () => {
    const originalError = new TypeError("Network request failed");
    const bootFn = () => Promise.reject(originalError);

    await expect(bootWithTimeout(bootFn)).rejects.toBeInstanceOf(TypeError);
  });

  it("clears the timer after successful boot (no dangling timers)", async () => {
    const bootFn = () => Promise.resolve();

    await bootWithTimeout(bootFn);

    expect(vi.getTimerCount()).toBe(0);
  });

  it("clears the timer after failed boot (no dangling timers)", async () => {
    const bootFn = () => Promise.reject(new Error("boom"));

    await bootWithTimeout(bootFn).catch(() => {
      // swallow expected error
    });

    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not reject before timeout elapses", async () => {
    const bootFn = () => new Promise<void>(() => {});

    const onReject = vi.fn();
    bootWithTimeout(bootFn).catch(onReject);

    // Advance to just before the timeout
    await vi.advanceTimersByTimeAsync(BOOT_TIMEOUT_MS - 1);

    expect(onReject).not.toHaveBeenCalled();

    // Now cross the threshold
    await vi.advanceTimersByTimeAsync(1);

    expect(onReject).toHaveBeenCalledTimes(1);
  });
});

// ---------------------------------------------------------------------------
// BootError rendering with timeout message
// ---------------------------------------------------------------------------

describe("BootError renders boot timeout", () => {
  it("displays the timeout message from AppConfigError", () => {
    const error = new AppConfigError("Application boot timed out");
    render(<BootError error={error} onRetry={vi.fn()} />);

    expect(screen.getByText("Application boot timed out")).toBeInTheDocument();
  });

  it("shows the heading alongside the timeout message", () => {
    const error = new AppConfigError("Application boot timed out");
    render(<BootError error={error} onRetry={vi.fn()} />);

    expect(screen.getByText("Unable to start application")).toBeInTheDocument();
    expect(screen.getByText("Application boot timed out")).toBeInTheDocument();
  });

  it("provides a retry button for timeout errors", () => {
    const error = new AppConfigError("Application boot timed out");
    render(<BootError error={error} onRetry={vi.fn()} />);

    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument();
  });
});
