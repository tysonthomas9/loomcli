/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for the useWaitingTracking hook.
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, afterEach } from "vitest";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";

import { WAITING_QUIET_MS } from "../waitingState";
import { useWaitingTracking } from "../useWaitingTracking";

// ── Helpers ────────────────────────────────────────────────────────────────

function makeHandle(
  probe: { cursorAtLineStart: boolean; altScreen: boolean } | null,
): TerminalInstanceHandle {
  return {
    disconnect: () => Promise.resolve(),
    reconnect: () => {},
    focus: () => {},
    pasteText: () => {},
    probeActivity: () => probe,
  };
}

function setup(
  options: {
    probe?: { cursorAtLineStart: boolean; altScreen: boolean } | null;
    connectionState?: ConnectionState;
  } = {},
) {
  const { probe = { cursorAtLineStart: false, altScreen: false } } = options;
  const instanceRefs = {
    current: new Map<string, TerminalInstanceHandle>([
      ["tab-1", makeHandle(probe)],
    ]),
  };
  const getConnectionState = () => options.connectionState ?? "connected";
  return renderHook(() =>
    useWaitingTracking({ instanceRefs, getConnectionState }),
  );
}

/** Advance past the quiet threshold and let at least one tick run. */
function advancePastQuiet() {
  act(() => {
    vi.advanceTimersByTime(WAITING_QUIET_MS + 1000);
  });
}

// ── Tests ──────────────────────────────────────────────────────────────────

describe("useWaitingTracking", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  it("badges a tab that has been quiet with the cursor mid-line", () => {
    vi.useFakeTimers();
    const { result } = setup();

    act(() => {
      result.current.noteOutput("tab-1");
    });
    expect(result.current.tabWaiting.get("tab-1")).toBeUndefined();

    advancePastQuiet();
    expect(result.current.tabWaiting.get("tab-1")).toBe(true);
  });

  it("does not badge a tab whose cursor sits at the line start", () => {
    vi.useFakeTimers();
    const { result } = setup({
      probe: { cursorAtLineStart: true, altScreen: false },
    });

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();

    expect(result.current.tabWaiting.has("tab-1")).toBe(false);
  });

  it("does not badge a tab that is not connected", () => {
    vi.useFakeTimers();
    const { result } = setup({ connectionState: "session_ended" });

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();

    expect(result.current.tabWaiting.has("tab-1")).toBe(false);
  });

  it("clears the badge synchronously on input, without waiting for a tick", () => {
    vi.useFakeTimers();
    const { result } = setup();

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();
    expect(result.current.tabWaiting.get("tab-1")).toBe(true);

    act(() => {
      result.current.noteInput("tab-1");
    });
    expect(result.current.tabWaiting.has("tab-1")).toBe(false);
  });

  it("re-arms after the next output burst goes quiet again", () => {
    vi.useFakeTimers();
    const { result } = setup();

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();
    act(() => {
      result.current.noteInput("tab-1");
    });
    expect(result.current.tabWaiting.has("tab-1")).toBe(false);

    // The echo of that keystroke, then silence again.
    act(() => {
      vi.advanceTimersByTime(10);
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();
    expect(result.current.tabWaiting.get("tab-1")).toBe(true);
  });

  it("clearTab drops the record and the badge", () => {
    vi.useFakeTimers();
    const { result } = setup();

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();
    expect(result.current.tabWaiting.get("tab-1")).toBe(true);

    act(() => {
      result.current.clearTab("tab-1");
    });
    expect(result.current.tabWaiting.has("tab-1")).toBe(false);

    // The record is gone, so further ticks cannot resurrect it.
    advancePastQuiet();
    expect(result.current.tabWaiting.has("tab-1")).toBe(false);
  });

  it("never badges a tab whose renderer is not mounted", () => {
    vi.useFakeTimers();
    const { result } = setup({ probe: null });

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();

    expect(result.current.tabWaiting.has("tab-1")).toBe(false);
  });

  it("keeps the map identity stable when nothing changes", () => {
    vi.useFakeTimers();
    const { result } = setup();

    act(() => {
      result.current.noteOutput("tab-1");
    });
    advancePastQuiet();
    const first = result.current.tabWaiting;

    act(() => {
      vi.advanceTimersByTime(5000);
    });
    expect(result.current.tabWaiting).toBe(first);
  });

  it("clears its interval on unmount", () => {
    vi.useFakeTimers();
    const clearSpy = vi.spyOn(globalThis, "clearInterval");
    const { unmount } = setup();

    unmount();
    expect(clearSpy).toHaveBeenCalled();
    clearSpy.mockRestore();
  });
});
