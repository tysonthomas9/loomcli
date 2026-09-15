// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/api/common";
import { useClaimHold } from "../useClaimHold";

const mocks = vi.hoisted(() => ({
  fetchClaimHold: vi.fn(),
  releaseClaimHold: vi.fn(),
  workspaceId: "LOCALMODE",
}));

vi.mock("@/api/agents/claimHold", () => ({
  fetchClaimHold: mocks.fetchClaimHold,
  releaseClaimHold: mocks.releaseClaimHold,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: mocks.workspaceId }),
}));

const freeStatus = (supervisorAvailable?: boolean) => ({
  hold: null,
  running: [],
  gated: 0,
  ...(supervisorAvailable === undefined
    ? {}
    : { supervisor_available: supervisorAvailable }),
});

const heldStatus = (actor: string) => ({
  hold: {
    held: true,
    actor,
    reason: "deploy",
    since: "2026-01-15T11:46:00Z",
  },
  running: [],
  gated: 1,
});

describe("useClaimHold", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.workspaceId = "LOCALMODE";
    mocks.fetchClaimHold.mockResolvedValue(heldStatus("deployer"));
  });

  it("refreshes the holder before offering force release", async () => {
    mocks.releaseClaimHold.mockRejectedValue(
      new ApiError(409, "Conflict", { error: "claims held by deployer" }),
    );
    mocks.fetchClaimHold
      .mockResolvedValueOnce(heldStatus("previous-owner"))
      .mockResolvedValueOnce(heldStatus("current-owner"));
    const { result } = renderHook(() => useClaimHold());

    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      expect(await result.current.release()).toBe(false);
    });

    expect(result.current.canForceRelease).toBe(true);
    expect(result.current.hold?.actor).toBe("current-owner");
    expect(result.current.error).toBe("claims held by deployer");
  });

  // PUPPET-529: a server that can reach no agent supervisor answers the GET
  // 200 with supervisor_available:false, forever. Polling that at 10 s is what
  // made this endpoint the dashboard's entire client-error rate, so the hook
  // must slow down — and, just as importantly, must not turn the reachability
  // signal into a re-render loop that fetches faster than the interval it
  // replaced.
  describe("poll cadence", () => {
    beforeEach(() => {
      vi.useFakeTimers();
    });

    afterEach(() => {
      vi.useRealTimers();
    });

    // Settle the mount fetch (and any state it writes) without advancing timers.
    const flush = async () => {
      await act(async () => {
        await Promise.resolve();
        await Promise.resolve();
      });
    };

    it("does not poll faster than the slow interval once the supervisor is unreachable", async () => {
      mocks.fetchClaimHold.mockResolvedValue(freeStatus(false));
      renderHook(() => useClaimHold());
      await flush();
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(1);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(30000);
      });

      // Exactly the mount fetch: three 10 s ticks must not have happened, and
      // neither may a reachability-triggered effect re-run have fetched.
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(1);
    });

    it("polls once a minute while the supervisor is unreachable", async () => {
      mocks.fetchClaimHold.mockResolvedValue(freeStatus(false));
      renderHook(() => useClaimHold());
      await flush();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(3);
    });

    it("keeps the 10 s cadence when the field is absent", async () => {
      mocks.fetchClaimHold.mockResolvedValue(freeStatus());
      renderHook(() => useClaimHold());
      await flush();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(20000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(4);
    });

    it("returns to the 10 s cadence when a supervisor becomes reachable", async () => {
      mocks.fetchClaimHold
        .mockResolvedValueOnce(freeStatus(false))
        .mockResolvedValue(freeStatus(true));
      renderHook(() => useClaimHold());
      await flush();

      await act(async () => {
        await vi.advanceTimersByTimeAsync(60000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(3);
    });

    it("resets to the 10 s cadence on a workspace switch", async () => {
      mocks.fetchClaimHold.mockResolvedValue(freeStatus(false));
      const { rerender } = renderHook(() => useClaimHold());
      await flush();
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(1);

      mocks.workspaceId = "OTHER";
      mocks.fetchClaimHold.mockResolvedValue(freeStatus(true));
      rerender();
      await flush();

      // Exactly one immediate fetch for the new workspace, not two.
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(2);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(10000);
      });
      expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(3);
    });
  });

  // The transport is quiet about a claim-hold 503 (isOutageExemptPath), but the
  // hook is not: an operator who presses Release on a supervisor-less host must
  // still be told it failed.
  it("surfaces a 503 from release as an error and clears busy", async () => {
    mocks.fetchClaimHold.mockResolvedValue(heldStatus("deployer"));
    mocks.releaseClaimHold.mockRejectedValue(
      new ApiError(503, "Service Unavailable", {
        error: "agent supervisor is not running",
      }),
    );
    const { result } = renderHook(() => useClaimHold());

    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      expect(await result.current.release()).toBe(false);
    });

    expect(result.current.error).toBe("agent supervisor is not running");
    expect(result.current.busy).toBe(false);
    expect(result.current.canForceRelease).toBe(false);
  });

  it("ignores a release conflict that completes after switching workspaces", async () => {
    let rejectRelease: ((reason: unknown) => void) | undefined;
    mocks.releaseClaimHold.mockImplementation(
      () =>
        new Promise((_, reject) => {
          rejectRelease = reject;
        }),
    );
    mocks.fetchClaimHold
      .mockResolvedValueOnce(heldStatus("workspace-a-owner"))
      .mockResolvedValueOnce(heldStatus("workspace-b-owner"));
    const { result, rerender } = renderHook(() => useClaimHold());

    await act(async () => {
      await Promise.resolve();
    });
    let releaseResult: Promise<boolean> | undefined;
    act(() => {
      releaseResult = result.current.release();
    });
    mocks.workspaceId = "OTHER";
    rerender();
    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      rejectRelease?.(
        new ApiError(409, "Conflict", { error: "claims held in workspace A" }),
      );
      await releaseResult;
    });

    expect(result.current.hold?.actor).toBe("workspace-b-owner");
    expect(result.current.canForceRelease).toBe(false);
    expect(result.current.error).toBeNull();
    expect(mocks.fetchClaimHold).toHaveBeenCalledTimes(2);
  });
});
