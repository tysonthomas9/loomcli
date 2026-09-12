/**
 * @vitest-environment jsdom
 */
import { renderHook, waitFor, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useWorkspaceStats } from "../useWorkspaceStats";
import * as api from "@/api";
import { useEventSubscription } from "@/hooks/common";
import type { MutationPayload, Statistics } from "@/types";

vi.mock("@/api", async () => {
  const actual = await vi.importActual<typeof import("@/api")>("@/api");
  return { ...actual, getStats: vi.fn() };
});

vi.mock("@/hooks/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/common")>("@/hooks/common");
  return { ...actual, useEventSubscription: vi.fn() };
});

const STATS: Statistics = {
  total_issues: 408,
  open_issues: 52,
  in_progress_issues: 2,
  closed_issues: 340,
  blocked_issues: 6,
  status_blocked_issues: 10,
  deferred_issues: 0,
  ready_issues: 30,
  review_issues: 4,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 0,
};

/** Fire the callback the hook registered with the SSE stream. */
function emit(mutation: Partial<MutationPayload>): void {
  const calls = vi.mocked(useEventSubscription).mock.calls;
  const callback = calls[calls.length - 1]?.[0];
  if (!callback) throw new Error("hook did not subscribe to mutations");
  act(() => {
    callback({
      type: "issue",
      timestamp: "2026-09-04T00:00:00.000Z",
      ...mutation,
    } as MutationPayload);
  });
}

describe("useWorkspaceStats", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.getStats).mockResolvedValue(STATS);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("fetches once on mount and exposes the stats", async () => {
    const { result } = renderHook(() => useWorkspaceStats("PUPPET"));

    await waitFor(() => {
      expect(result.current.stats).toEqual(STATS);
    });
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(false);
    expect(api.getStats).toHaveBeenCalledTimes(1);
    expect(api.getStats).toHaveBeenCalledWith(
      "PUPPET",
      expect.objectContaining({ signal: expect.anything() }),
    );
  });

  it("clears stats and refetches when the workspace changes", async () => {
    const { result, rerender } = renderHook(
      ({ ws }: { ws: string }) => useWorkspaceStats(ws),
      { initialProps: { ws: "PUPPET" } },
    );

    await waitFor(() => expect(result.current.stats).toEqual(STATS));

    vi.mocked(api.getStats).mockReturnValue(new Promise(() => {}));
    rerender({ ws: "OTHER" });

    // The previous workspace's numbers must not render under the new name.
    expect(result.current.stats).toBeNull();
    expect(api.getStats).toHaveBeenCalledTimes(2);
    expect(vi.mocked(api.getStats).mock.calls[1]?.[0]).toBe("OTHER");
  });

  it("refetches on a mutation for the same workspace", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useWorkspaceStats("PUPPET"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.stats).toEqual(STATS);
    expect(api.getStats).toHaveBeenCalledTimes(1);

    emit({ workspace_id: "PUPPET" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(api.getStats).toHaveBeenCalledTimes(2);
  });

  it("ignores a mutation for a different workspace", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useWorkspaceStats("PUPPET"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.stats).toEqual(STATS);

    emit({ workspace_id: "OTHER" });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5000);
    });

    expect(api.getStats).toHaveBeenCalledTimes(1);
  });

  it("coalesces a burst of mutations into a single refetch", async () => {
    vi.useFakeTimers();
    const { result } = renderHook(() => useWorkspaceStats("PUPPET"));

    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(result.current.stats).toEqual(STATS);

    for (let i = 0; i < 20; i += 1) {
      emit({ workspace_id: "PUPPET" });
      await act(async () => {
        await vi.advanceTimersByTimeAsync(50);
      });
    }
    await act(async () => {
      await vi.advanceTimersByTimeAsync(1000);
    });

    expect(api.getStats).toHaveBeenCalledTimes(2);
  });

  it("surfaces an error without throwing, and keeps stats null", async () => {
    vi.mocked(api.getStats).mockRejectedValue(new Error("stats unavailable"));

    const { result } = renderHook(() => useWorkspaceStats("PUPPET"));

    await waitFor(() => {
      expect(result.current.error).toBe("stats unavailable");
    });
    expect(result.current.stats).toBeNull();
  });

  it("aborts the in-flight request on unmount", async () => {
    let seen: AbortSignal | undefined;
    vi.mocked(api.getStats).mockImplementation((_ws, options) => {
      seen = options?.signal;
      return new Promise(() => {});
    });

    const { unmount } = renderHook(() => useWorkspaceStats("PUPPET"));
    expect(seen?.aborted).toBe(false);

    unmount();

    expect(seen?.aborted).toBe(true);
  });
});
