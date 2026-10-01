/**
 * @vitest-environment jsdom
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import type { IssueDetails } from "@/types";

const mocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  workspaceId: "ws-a",
}));

vi.mock("@/api/issues", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/issues")>()),
  getIssue: mocks.getIssue,
}));

vi.mock("@/hooks/workspace", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/workspace")>()),
  useWorkspaceContext: () => ({ workspaceId: mocks.workspaceId }),
}));

import { useIssueDetail } from "../useIssueDetail";

function details(id: string, title: string): IssueDetails {
  return {
    id,
    title,
    priority: 2,
    status: "open",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  } as IssueDetails;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

afterEach(() => {
  mocks.getIssue.mockReset();
  mocks.workspaceId = "ws-a";
});

it("#665 an obsolete workspace's detail read cannot overwrite the selected detail", async () => {
  const oldRead = deferred<IssueDetails>();
  mocks.getIssue.mockReturnValue(oldRead.promise);
  const { result, rerender } = renderHook(() => useIssueDetail());

  let oldFetch!: Promise<void>;
  act(() => {
    oldFetch = result.current.fetchIssue("issue-1");
  });
  mocks.workspaceId = "ws-b";
  rerender();

  await act(async () => {
    oldRead.resolve(details("issue-1", "workspace A detail"));
    await oldFetch;
  });

  expect(
    result.current.issueDetails?.title,
    "workspace A's obsolete detail response was committed after the switch to B",
  ).not.toBe("workspace A detail");
});

it("#665 a detail response for a different issue is not committed as the selection", async () => {
  mocks.getIssue.mockResolvedValue(details("issue-2", "foreign issue"));
  const { result } = renderHook(() => useIssueDetail());

  await act(async () => {
    await result.current.fetchIssue("issue-1").catch(() => undefined);
  });

  expect(
    result.current.issueDetails?.id,
    "foreign-ID detail response was committed for issue-1",
  ).not.toBe("issue-2");
});
