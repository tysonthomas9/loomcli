// @vitest-environment jsdom

import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/types/common/errors";

import { usePRReviewConversation } from "../usePRReviewConversation";

const mocks = vi.hoisted(() => ({
  ensureReviewer: vi.fn(),
  getReviewerConversation: vi.fn(),
  sendReviewerMessage: vi.fn(),
}));

vi.mock("@/api/workspace/prReview", () => ({
  ensureReviewer: mocks.ensureReviewer,
  getReviewerConversation: mocks.getReviewerConversation,
  sendReviewerMessage: mocks.sendReviewerMessage,
}));

describe("usePRReviewConversation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("surfaces a stale reviewer subject through the dedicated callback", async () => {
    mocks.ensureReviewer.mockRejectedValue(
      new ApiError(409, "Conflict", {
        code: "stale_subject",
        error: "pull request head changed",
        retryable: true,
      }),
    );
    const onStaleSubject = vi.fn();

    const { result } = renderHook(() =>
      usePRReviewConversation({
        workspaceId: "WS",
        owner: "octocat",
        repo: "hello",
        number: 7,
        enabled: true,
        onStaleSubject,
      }),
    );

    await waitFor(() => {
      expect(onStaleSubject).toHaveBeenCalledTimes(1);
    });
    expect(result.current.agentName).toBeNull();
    expect(result.current.error).toBe("pull request head changed");
  });
});
