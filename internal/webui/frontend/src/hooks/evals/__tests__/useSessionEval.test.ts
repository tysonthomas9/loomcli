/**
 * @vitest-environment jsdom
 */
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { getSessionEval, rejudgeSession } from "@/api/evals";

import { useSessionEval } from "../useSessionEval";

vi.mock("@/api/evals", () => ({
  getSessionEval: vi.fn(),
  rejudgeSession: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "WS" }),
  };
});

const mockGetSessionEval = vi.mocked(getSessionEval);
const mockRejudgeSession = vi.mocked(rejudgeSession);

describe("useSessionEval", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("requests rejudge, returns the cron binding state, and refreshes eval state", async () => {
    mockGetSessionEval
      .mockResolvedValueOnce({
        eval_status: "none",
        eval_requested: false,
        eval: null,
      })
      .mockResolvedValueOnce({
        eval_status: "none",
        eval_requested: true,
        eval: null,
      });
    mockRejudgeSession.mockResolvedValue({
      requested: true,
      binding_enabled: false,
    });

    const { result } = renderHook(() => useSessionEval("sess-1"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    let rejudgeResult: Awaited<
      ReturnType<typeof result.current.requestRejudge>
    > = null;
    await act(async () => {
      rejudgeResult = await result.current.requestRejudge();
    });

    expect(mockRejudgeSession).toHaveBeenCalledWith("WS", "sess-1");
    expect(mockGetSessionEval).toHaveBeenCalledTimes(2);
    expect(rejudgeResult).toEqual({
      requested: true,
      binding_enabled: false,
    });
    expect(result.current.evalState?.eval_requested).toBe(true);
  });

  it("propagates rejudge rejections to the caller without setting the load error", async () => {
    mockGetSessionEval.mockResolvedValue({
      eval_status: "none",
      eval_requested: false,
      eval: null,
    });
    mockRejudgeSession.mockRejectedValue(
      new Error('not an eval candidate: session "s1" has no transcript_ref'),
    );

    const { result } = renderHook(() => useSessionEval("sess-1"));

    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await expect(result.current.requestRejudge()).rejects.toThrow(
        "not an eval candidate",
      );
    });

    expect(result.current.error).toBeNull();
    expect(result.current.isRejudging).toBe(false);
  });
});
