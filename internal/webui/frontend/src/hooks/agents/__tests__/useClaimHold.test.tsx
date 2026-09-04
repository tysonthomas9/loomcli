// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/api/common";
import { useClaimHold } from "../useClaimHold";

const mocks = vi.hoisted(() => ({
  fetchClaimHold: vi.fn(),
  releaseClaimHold: vi.fn(),
}));

vi.mock("@/api/agents/claimHold", () => ({
  fetchClaimHold: mocks.fetchClaimHold,
  releaseClaimHold: mocks.releaseClaimHold,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "LOCALMODE" }),
}));

describe("useClaimHold", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.fetchClaimHold.mockResolvedValue({
      hold: {
        held: true,
        actor: "deployer",
        reason: "deploy",
        since: "2026-01-15T11:46:00Z",
      },
      running: [],
      gated: 1,
    });
  });

  it("offers force release only after an ownership conflict", async () => {
    mocks.releaseClaimHold.mockRejectedValue(
      new ApiError(409, "Conflict", { error: "claims held by deployer" }),
    );
    const { result } = renderHook(() => useClaimHold());

    await act(async () => {
      await Promise.resolve();
    });
    await act(async () => {
      expect(await result.current.release()).toBe(false);
    });

    expect(result.current.canForceRelease).toBe(true);
    expect(result.current.error).toBe("claims held by deployer");
  });
});
