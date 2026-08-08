// @vitest-environment jsdom

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { usePullRequests } from "../usePullRequests";

const mocks = vi.hoisted(() => ({
  fetchPullRequests: vi.fn(),
  workspaceId: "WS",
}));

vi.mock("../api/pullRequests", () => ({
  fetchPullRequests: mocks.fetchPullRequests,
}));

let documentHidden = false;

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

function setDocumentHidden(hidden: boolean): void {
  documentHidden = hidden;
  act(() => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
}

describe("usePullRequests polling", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    documentHidden = false;
    Object.defineProperty(document, "hidden", {
      configurable: true,
      get: () => documentHidden,
    });
    mocks.fetchPullRequests.mockResolvedValue({
      pullRequests: [],
      warnings: [],
    });
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("pauses while hidden and refreshes immediately when visible", async () => {
    renderHook(() => usePullRequests({ workspaceId: mocks.workspaceId }));
    await flushPromises();
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(1);

    setDocumentHidden(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5 * 60_000);
    });
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(1);

    setDocumentHidden(false);
    await flushPromises();
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(2);
  });

  it("backs off after failure and resets after success", async () => {
    mocks.fetchPullRequests.mockRejectedValueOnce(new Error("upstream down"));
    renderHook(() => usePullRequests({ workspaceId: mocks.workspaceId }));
    await flushPromises();
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(59_999);
    });
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    await flushPromises();
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(2);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    await flushPromises();
    expect(mocks.fetchPullRequests).toHaveBeenCalledTimes(3);
  });
});
