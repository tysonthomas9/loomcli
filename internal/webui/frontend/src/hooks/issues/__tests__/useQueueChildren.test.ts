/**
 * @vitest-environment jsdom
 */

import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Issue, IssueDetails, IssueWithDependencyMetadata } from "@/types";

import { resetQueueChildrenCache, useQueueChildren } from "../useQueueChildren";

const mocks = vi.hoisted(() => ({
  getIssue: vi.fn(),
  workspaceId: "PUPPET",
}));

vi.mock("@/api/issues", () => ({
  getIssue: mocks.getIssue,
}));

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: mocks.workspaceId }),
}));

function issue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "PARENT",
    title: "Parked parent",
    priority: 2,
    issue_type: "task",
    status: "blocked",
    notes: "BLOCKED: waiting on children",
    created_at: "2026-09-03T06:00:00.000Z",
    updated_at: "2026-09-03T06:00:00.000Z",
    ...overrides,
  } as Issue;
}

function dependent(
  id: string,
  status: string,
  dependencyType = "parent-child",
): IssueWithDependencyMetadata {
  return {
    id,
    title: id,
    priority: 2,
    issue_type: "task",
    status,
    created_at: "2026-09-03T06:00:00.000Z",
    updated_at: "2026-09-03T06:00:00.000Z",
    dependency_type: dependencyType,
  } as IssueWithDependencyMetadata;
}

function details(overrides: Partial<IssueDetails>): IssueDetails {
  return { ...issue(), ...overrides } as IssueDetails;
}

describe("useQueueChildren", () => {
  beforeEach(() => {
    resetQueueChildrenCache();
    mocks.getIssue.mockReset();
    mocks.workspaceId = "PUPPET";
  });

  it("indexes the parent-child dependents of every parked candidate", async () => {
    mocks.getIssue.mockResolvedValue(
      details({
        dependents: [dependent("A", "open"), dependent("B", "closed")],
      }),
    );

    const issues = [issue({ id: "PARENT" })];
    const { result } = renderHook(() => useQueueChildren(issues));

    await waitFor(() => expect(result.current.size).toBe(1));
    expect(result.current.get("PARENT")).toEqual([
      { id: "A", status: "open" },
      { id: "B", status: "closed" },
    ]);
    expect(mocks.getIssue).toHaveBeenCalledWith("PUPPET", "PARENT");
  });

  it("filters out dependents that are not parent-child", async () => {
    mocks.getIssue.mockResolvedValue(
      details({
        dependents: [
          dependent("A", "open", "blocks"),
          dependent("B", "open", "related"),
          dependent("C", "open"),
        ],
      }),
    );

    const issues = [issue({ id: "PARENT" })];
    const { result } = renderHook(() => useQueueChildren(issues));

    await waitFor(() => expect(result.current.size).toBe(1));
    expect(result.current.get("PARENT")).toEqual([{ id: "C", status: "open" }]);
  });

  it("records an empty list when the issue has no dependents", async () => {
    mocks.getIssue.mockResolvedValue(details({ dependents: [] }));

    const issues = [issue({ id: "OPERATOR-ONLY" })];
    const { result } = renderHook(() => useQueueChildren(issues));

    await waitFor(() => expect(result.current.has("OPERATOR-ONLY")).toBe(true));
    expect(result.current.get("OPERATOR-ONLY")).toEqual([]);
  });

  it("omits an id whose fetch rejected, and does not reject itself", async () => {
    mocks.getIssue.mockRejectedValue(new Error("boom"));

    const issues = [issue({ id: "PARENT" })];
    const { result } = renderHook(() => useQueueChildren(issues));

    await waitFor(() => expect(mocks.getIssue).toHaveBeenCalledTimes(1));
    expect(result.current.has("PARENT")).toBe(false);
  });

  it("only fetches issues that are parked with a note", async () => {
    mocks.getIssue.mockResolvedValue(details({ dependents: [] }));

    const issues = [
      issue({ id: "PARKED" }),
      issue({ id: "NO-NOTE", notes: "" }),
      issue({ id: "OPEN", status: "open" }),
    ];
    const { result } = renderHook(() => useQueueChildren(issues));

    await waitFor(() => expect(result.current.size).toBe(1));
    expect(mocks.getIssue).toHaveBeenCalledTimes(1);
    expect(mocks.getIssue).toHaveBeenCalledWith("PUPPET", "PARKED");
  });

  it("does not refetch an id whose updated_at is unchanged", async () => {
    mocks.getIssue.mockResolvedValue(details({ dependents: [] }));

    const first = [issue({ id: "PARENT" })];
    const { result, rerender } = renderHook(
      ({ issues }: { issues: Issue[] }) => useQueueChildren(issues),
      { initialProps: { issues: first } },
    );

    await waitFor(() => expect(result.current.size).toBe(1));

    // A fresh array with identical contents: the cache key is id@updated_at.
    rerender({ issues: [issue({ id: "PARENT" })] });
    await waitFor(() => expect(result.current.size).toBe(1));
    expect(mocks.getIssue).toHaveBeenCalledTimes(1);

    // A changed updated_at is a new key, so it refetches.
    rerender({
      issues: [issue({ id: "PARENT", updated_at: "2026-09-03T07:00:00.000Z" })],
    });
    await waitFor(() => expect(mocks.getIssue).toHaveBeenCalledTimes(2));
  });

  it("returns an empty index with no workspace context", () => {
    mocks.workspaceId = "";

    const issues = [issue({ id: "PARENT" })];
    const { result } = renderHook(() => useQueueChildren(issues));

    expect(result.current.size).toBe(0);
    expect(mocks.getIssue).not.toHaveBeenCalled();
  });
});
