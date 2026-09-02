/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for useWorkspaceSessionCount.
 * Covers the counting rule (non-agent tabs with a live PTY), the empty-workspace
 * short circuit, the workspace-switch reset that is the PUPPET-123 regression,
 * the stale-response guard, SSE-driven debounced refetch, the cancellation of a
 * debounce left pending across a workspace switch, the hidden-paused five
 * minute safety poll, and silent failure.
 */

import { renderHook, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import * as terminalApi from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { useEventSubscription } from "@/hooks/common";

import { useWorkspaceSessionCount } from "../useWorkspaceSessionCount";

vi.mock("@/api/terminal", () => ({
  listTabMetadata: vi.fn(),
}));

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useEventSubscription: vi.fn() };
});

let currentWorkspaceId = "ws-1";

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: currentWorkspaceId }),
  };
});

function tab(overrides?: Partial<TabMetadata>): TabMetadata {
  return {
    session_name: "lead-shell-1",
    label: "Tab",
    notes: "",
    sort_order: 0,
    pinned: false,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    pty_alive: true,
    attached_clients: 0,
    ...overrides,
  };
}

const mockList = vi.mocked(terminalApi.listTabMetadata);

/** Mirrors DEBOUNCE_MS in the hook under test. */
const DEBOUNCE_MS = 200;
/** Mirrors POLL_MS in the hook under test. */
const POLL_MS = 5 * 60_000;

describe("useWorkspaceSessionCount", () => {
  beforeEach(() => {
    currentWorkspaceId = "ws-1";
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("counts only non-agent tabs with a live PTY", async () => {
    mockList.mockResolvedValue([
      tab({ session_name: "lead-shell-1" }),
      tab({ session_name: "lead-shell-2" }),
      tab({ session_name: "lead-shell-3", pty_alive: false }),
      tab({ session_name: "agent-observer", kind: "agent" }),
    ]);

    const { result } = renderHook(() => useWorkspaceSessionCount());

    await waitFor(() => {
      expect(result.current.sessionCount).toBe(2);
    });
  });

  it("returns 0 and issues no request when the workspace id is empty", async () => {
    currentWorkspaceId = "";

    const { result } = renderHook(() => useWorkspaceSessionCount());

    await act(async () => {
      await Promise.resolve();
    });

    expect(result.current.sessionCount).toBe(0);
    expect(mockList).not.toHaveBeenCalled();
  });

  it("resets to 0 on a workspace change, then reports the new workspace's count", async () => {
    mockList.mockResolvedValue([tab()]);

    const { result, rerender } = renderHook(() => useWorkspaceSessionCount());

    await waitFor(() => {
      expect(result.current.sessionCount).toBe(1);
    });

    // Switching workspaces must not carry the previous count over — the badge
    // has to drop to 0 synchronously and be re-derived from the new
    // workspace's server state. Hold the new fetch pending so the reset is
    // observable rather than raced by the response.
    let resolveNew: (tabs: TabMetadata[]) => void = () => {};
    mockList.mockImplementationOnce(
      () =>
        new Promise<TabMetadata[]>((resolve) => {
          resolveNew = resolve;
        }),
    );
    currentWorkspaceId = "ws-2";
    rerender();

    expect(result.current.sessionCount).toBe(0);
    expect(mockList).toHaveBeenLastCalledWith("ws-2");

    await act(async () => {
      resolveNew([tab(), tab({ session_name: "lead-shell-2" })]);
      await Promise.resolve();
    });

    expect(result.current.sessionCount).toBe(2);
  });

  it("ignores a stale response for the previous workspace", async () => {
    let resolveStale: (tabs: TabMetadata[]) => void = () => {};
    mockList.mockImplementationOnce(
      () =>
        new Promise<TabMetadata[]>((resolve) => {
          resolveStale = resolve;
        }),
    );

    const { result, rerender } = renderHook(() => useWorkspaceSessionCount());

    currentWorkspaceId = "ws-2";
    mockList.mockResolvedValue([tab()]);
    rerender();

    await waitFor(() => {
      expect(result.current.sessionCount).toBe(1);
    });

    await act(async () => {
      resolveStale([
        tab(),
        tab({ session_name: "b" }),
        tab({ session_name: "c" }),
      ]);
      await Promise.resolve();
    });

    expect(result.current.sessionCount).toBe(1);
  });

  it("refetches once, debounced, on a terminal_metadata mutation", async () => {
    vi.useFakeTimers();
    mockList.mockResolvedValue([tab()]);

    renderHook(() => useWorkspaceSessionCount());

    await act(async () => {
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(1);

    const handler = vi.mocked(useEventSubscription).mock.calls[0]?.[0] as (
      m: MutationPayload,
    ) => void;

    act(() => {
      handler({ type: "terminal_metadata" } as MutationPayload);
      handler({ type: "terminal_metadata" } as MutationPayload);
    });

    await act(async () => {
      vi.advanceTimersByTime(DEBOUNCE_MS);
      await Promise.resolve();
    });

    expect(mockList).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it("drops a debounce left pending when the workspace changes", async () => {
    vi.useFakeTimers();
    mockList.mockResolvedValue([tab()]);

    const { rerender } = renderHook(() => useWorkspaceSessionCount());

    await act(async () => {
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(1);

    const handler = vi.mocked(useEventSubscription).mock.calls[0]?.[0] as (
      m: MutationPayload,
    ) => void;

    act(() => {
      handler({ type: "terminal_metadata" } as MutationPayload);
    });

    // Switching before the debounce elapses must cancel it: firing it would
    // re-query the workspace we just left.
    currentWorkspaceId = "ws-2";
    rerender();

    await act(async () => {
      vi.advanceTimersByTime(DEBOUNCE_MS);
      await Promise.resolve();
    });

    expect(mockList).toHaveBeenCalledTimes(2);
    expect(mockList).toHaveBeenNthCalledWith(1, "ws-1");
    expect(mockList).toHaveBeenNthCalledWith(2, "ws-2");
    vi.useRealTimers();
  });

  it("runs the safety poll every five minutes while visible", async () => {
    vi.useFakeTimers();
    mockList.mockResolvedValue([tab()]);

    renderHook(() => useWorkspaceSessionCount());
    await act(async () => {
      await Promise.resolve();
    });

    await act(async () => {
      vi.advanceTimersByTime(POLL_MS - 1);
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(1);

    await act(async () => {
      vi.advanceTimersByTime(1);
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(2);
    vi.useRealTimers();
  });

  it("pauses the safety poll while hidden and refetches when visible", async () => {
    vi.useFakeTimers();
    const hidden = vi.spyOn(document, "hidden", "get").mockReturnValue(true);
    const visibility = vi
      .spyOn(document, "visibilityState", "get")
      .mockReturnValue("hidden");
    mockList.mockResolvedValue([tab()]);

    renderHook(() => useWorkspaceSessionCount());
    await act(async () => {
      await Promise.resolve();
    });

    await act(async () => {
      vi.advanceTimersByTime(POLL_MS * 2);
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(1);

    hidden.mockReturnValue(false);
    visibility.mockReturnValue("visible");
    await act(async () => {
      document.dispatchEvent(new Event("visibilitychange"));
      await Promise.resolve();
    });
    expect(mockList).toHaveBeenCalledTimes(2);
  });

  it("keeps the previous count and does not throw when the fetch rejects", async () => {
    mockList.mockResolvedValueOnce([tab(), tab({ session_name: "b" })]);

    const { result } = renderHook(() => useWorkspaceSessionCount());

    await waitFor(() => {
      expect(result.current.sessionCount).toBe(2);
    });

    mockList.mockRejectedValueOnce(new Error("boom"));

    await act(async () => {
      await result.current.refetch();
    });

    expect(result.current.sessionCount).toBe(2);
  });
});
