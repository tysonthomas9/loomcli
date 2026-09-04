// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

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
