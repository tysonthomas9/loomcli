/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError } from "@/types";
import type { Issue } from "@/types";
import {
  ACTIVE_ISSUE_LOOKUP_RETRY_MS,
  ACTIVE_ISSUE_LOOKUP_TIMEOUT_MS,
  useActiveIssueLookups,
} from "../useActiveIssueLookups";

const { mockGetIssue } = vi.hoisted(() => ({
  mockGetIssue: vi.fn(),
}));

vi.mock("@/api", () => ({
  getIssue: mockGetIssue,
}));

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
}

describe("useActiveIssueLookups", () => {
  beforeEach(() => {
    mockGetIssue.mockReset();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("returns a directly resolved issue", async () => {
    mockGetIssue.mockResolvedValue({
      id: "WS-1",
      title: "Active bug",
      issue_type: "bug",
    } as Issue);

    const { result } = renderHook(() => useActiveIssueLookups("WS", ["WS-1"]));
    await flushPromises();

    expect(result.current.results.get("WS-1")).toMatchObject({
      status: "found",
      issue: { id: "WS-1", title: "Active bug" },
    });
  });

  it("publishes each lookup without waiting for a slower active issue", async () => {
    mockGetIssue.mockImplementation((_workspace: string, issueID: string) => {
      if (issueID === "WS-SLOW") return new Promise(() => {});
      return Promise.resolve({
        id: issueID,
        title: "Fast issue",
        issue_type: "task",
      } as Issue);
    });

    const { result, unmount } = renderHook(() =>
      useActiveIssueLookups("WS", ["WS-SLOW", "WS-FAST"]),
    );
    await flushPromises();

    expect(result.current.results.get("WS-FAST")?.status).toBe("found");
    expect(result.current.results.has("WS-SLOW")).toBe(false);
    unmount();
  });

  it("only classifies an HTTP 404 as missing", async () => {
    mockGetIssue.mockRejectedValue(
      new ApiError(404, "Not Found", { error: "Issue not found" }),
    );

    const { result, unmount } = renderHook(() =>
      useActiveIssueLookups("WS", ["WS-404"]),
    );
    await flushPromises();

    expect(result.current.results.get("WS-404")?.status).toBe("missing");
    unmount();
  });

  it("aborts a stalled request and keeps the result non-authoritative", async () => {
    vi.useFakeTimers();
    mockGetIssue.mockImplementation(
      (
        _workspace: string,
        _issueID: string,
        options?: { signal?: AbortSignal },
      ) =>
        new Promise((_resolve, reject) => {
          options?.signal?.addEventListener("abort", () => {
            reject(new DOMException("aborted", "AbortError"));
          });
        }),
    );

    const { result } = renderHook(() =>
      useActiveIssueLookups("WS", ["WS-STALLED"]),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(ACTIVE_ISSUE_LOOKUP_TIMEOUT_MS);
    });

    expect(result.current.results.get("WS-STALLED")?.status).toBe("error");
  });

  it("retries a transient failure and makes the issue available", async () => {
    vi.useFakeTimers();
    mockGetIssue
      .mockRejectedValueOnce(new ApiError(503, "Unavailable"))
      .mockResolvedValue({
        id: "WS-2",
        title: "Recovered issue",
        issue_type: "chore",
      } as Issue);

    const { result } = renderHook(() => useActiveIssueLookups("WS", ["WS-2"]));
    await flushPromises();
    expect(result.current.results.get("WS-2")?.status).toBe("error");

    await act(async () => {
      await vi.advanceTimersByTimeAsync(ACTIVE_ISSUE_LOOKUP_RETRY_MS);
    });

    expect(mockGetIssue).toHaveBeenCalledTimes(2);
    expect(result.current.results.get("WS-2")).toMatchObject({
      status: "found",
      issue: { id: "WS-2", title: "Recovered issue" },
    });
  });
});
