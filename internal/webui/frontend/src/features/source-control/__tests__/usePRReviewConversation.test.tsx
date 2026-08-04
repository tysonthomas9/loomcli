// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/types/common/errors";

import { usePRReviewConversation } from "../usePRReviewConversation";

const mocks = vi.hoisted(() => ({
  ensureReviewer: vi.fn(),
  getReviewerConversation: vi.fn(),
  sendReviewerMessage: vi.fn(),
}));

vi.mock("../api/prReview", () => ({
  ensureReviewer: mocks.ensureReviewer,
  getReviewerConversation: mocks.getReviewerConversation,
  sendReviewerMessage: mocks.sendReviewerMessage,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

describe("usePRReviewConversation", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("keeps an in-flight ensure stable across callback identity changes", async () => {
    const ensure = deferred<{ agent_name: string }>();
    mocks.ensureReviewer.mockReturnValue(ensure.promise);
    mocks.getReviewerConversation.mockResolvedValue({
      messages: [],
      state: "idle",
    });
    const firstCallback = vi.fn();
    const secondCallback = vi.fn();

    const { result, rerender } = renderHook(
      ({ onStaleSubject }) =>
        usePRReviewConversation({
          workspaceId: "WS",
          owner: "octocat",
          repo: "hello",
          number: 7,
          enabled: true,
          onStaleSubject,
        }),
      { initialProps: { onStaleSubject: firstCallback } },
    );

    await waitFor(() => {
      expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);
    });
    rerender({ onStaleSubject: secondCallback });
    expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);

    await act(async () => {
      ensure.resolve({ agent_name: "review-octocat-hello-pr-7" });
      await ensure.promise;
    });

    await waitFor(() => {
      expect(result.current.agentName).toBe("review-octocat-hello-pr-7");
      expect(result.current.state).toBe("idle");
    });
    expect(firstCallback).not.toHaveBeenCalled();
    expect(secondCallback).not.toHaveBeenCalled();
    expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);
  });

  it("reports a stale subject to the latest callback without re-ensuring on callback churn", async () => {
    const ensure = deferred<{ agent_name: string }>();
    mocks.ensureReviewer.mockReturnValue(ensure.promise);
    const staleError = new ApiError(409, "Conflict", {
      code: "stale_subject",
      error: "pull request head changed",
      retryable: true,
    });
    const firstCallback = vi.fn();
    const latestCallback = vi.fn();
    const laterCallback = vi.fn();

    const { result, rerender } = renderHook(
      ({ onStaleSubject }) =>
        usePRReviewConversation({
          workspaceId: "WS",
          owner: "octocat",
          repo: "hello",
          number: 7,
          enabled: true,
          onStaleSubject,
        }),
      { initialProps: { onStaleSubject: firstCallback } },
    );

    await waitFor(() => {
      expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);
    });
    rerender({ onStaleSubject: latestCallback });

    await act(async () => {
      ensure.reject(staleError);
      await expect(ensure.promise).rejects.toBe(staleError);
    });

    await waitFor(() => {
      expect(latestCallback).toHaveBeenCalledTimes(1);
    });
    expect(firstCallback).not.toHaveBeenCalled();
    expect(result.current.agentName).toBeNull();
    expect(result.current.error).toBe("pull request head changed");

    rerender({ onStaleSubject: laterCallback });
    await act(async () => Promise.resolve());

    expect(mocks.ensureReviewer).toHaveBeenCalledTimes(1);
    expect(latestCallback).toHaveBeenCalledTimes(1);
    expect(laterCallback).not.toHaveBeenCalled();
  });
});
