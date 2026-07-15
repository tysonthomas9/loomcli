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

vi.mock("@/api/workspace/prReview", () => ({
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

  // Stand up the hook, wait for the first (full-snapshot) poll to settle, then
  // return the hook handle so each cursor test can drive the next poll via a
  // send()-triggered refetch.
  async function renderWithFirstSnapshot(
    first: Record<string, unknown>,
  ): Promise<
    ReturnType<typeof renderHook<ReturnType<typeof usePRReviewConversation>, unknown>>
  > {
    mocks.ensureReviewer.mockResolvedValue({
      agent_name: "review-octocat-hello-pr-7",
    });
    mocks.sendReviewerMessage.mockResolvedValue({
      state: "delivered",
      reason: "",
    });
    mocks.getReviewerConversation.mockResolvedValue(first);

    const rendered = renderHook(() =>
      usePRReviewConversation({
        workspaceId: "WS",
        owner: "octocat",
        repo: "hello",
        number: 7,
        enabled: true,
      }),
    );
    await waitFor(() => {
      expect(rendered.result.current.agentName).toBe(
        "review-octocat-hello-pr-7",
      );
    });
    return rendered;
  }

  it("appends an incremental tail and advances the cursor", async () => {
    const { result } = await renderWithFirstSnapshot({
      state: "idle",
      messages: [{ turn_id: "t1", item_id: "i1", role: "user", text: "hello" }],
      cursor: "t1/i1",
      reset: true,
    });
    await waitFor(() => {
      expect(result.current.messages).toHaveLength(1);
    });
    // First poll carries no cursor.
    expect(mocks.getReviewerConversation).toHaveBeenLastCalledWith(
      "WS",
      "octocat",
      "hello",
      7,
      undefined,
    );

    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [{ turn_id: "t1", item_id: "i2", role: "assistant", text: "hi" }],
      cursor: "t1/i2",
      reset: false,
    });
    await act(async () => {
      await result.current.send("more");
    });

    await waitFor(() => {
      expect(result.current.messages).toHaveLength(2);
    });
    expect(result.current.messages[1].text).toBe("hi");
    // The next poll is keyed off the cursor the client now holds.
    expect(mocks.getReviewerConversation).toHaveBeenLastCalledWith(
      "WS",
      "octocat",
      "hello",
      7,
      "t1/i1",
    );
  });

  it("replaces messages wholesale on a reset (unknown cursor)", async () => {
    const { result } = await renderWithFirstSnapshot({
      state: "idle",
      messages: [{ turn_id: "t1", item_id: "i1", role: "user", text: "hello" }],
      cursor: "t1/i1",
      reset: true,
    });
    await waitFor(() => {
      expect(result.current.messages).toHaveLength(1);
    });

    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [
        { turn_id: "t9", item_id: "i9", role: "user", text: "rotated" },
        { turn_id: "t9", item_id: "i10", role: "assistant", text: "fresh" },
      ],
      cursor: "t9/i10",
      reset: true,
    });
    await act(async () => {
      await result.current.send("more");
    });

    await waitFor(() => {
      expect(result.current.messages).toHaveLength(2);
    });
    expect(result.current.messages[0].text).toBe("rotated");
    expect(result.current.messages[1].text).toBe("fresh");
  });

  it("keeps messages on an empty no-new-messages response", async () => {
    const { result } = await renderWithFirstSnapshot({
      state: "idle",
      messages: [{ turn_id: "t1", item_id: "i1", role: "user", text: "hello" }],
      cursor: "t1/i1",
      reset: true,
    });
    await waitFor(() => {
      expect(result.current.messages).toHaveLength(1);
    });

    mocks.getReviewerConversation.mockResolvedValue({
      state: "idle",
      messages: [],
      cursor: "t1/i1",
      reset: false,
    });
    await act(async () => {
      await result.current.send("more");
    });

    // No new messages appended; the existing message is retained.
    expect(result.current.messages).toHaveLength(1);
    expect(result.current.messages[0].text).toBe("hello");
  });

  it("keeps the last good messages through a reconnecting snapshot", async () => {
    const { result } = await renderWithFirstSnapshot({
      state: "idle",
      messages: [
        { turn_id: "t1", item_id: "i1", role: "user", text: "hello" },
        { turn_id: "t1", item_id: "i2", role: "assistant", text: "hi" },
      ],
      cursor: "t1/i2",
      reset: true,
    });
    await waitFor(() => {
      expect(result.current.messages).toHaveLength(2);
    });

    // A transient read failure: reconnecting with no messages and no reset.
    mocks.getReviewerConversation.mockResolvedValue({
      state: "reconnecting",
      messages: [],
      cursor: "t1/i2",
      reset: false,
    });
    await act(async () => {
      await result.current.send("still there?");
    });

    // The chat must not blank out.
    expect(result.current.messages).toHaveLength(2);
    expect(result.current.state).toBe("reconnecting");
  });
});
