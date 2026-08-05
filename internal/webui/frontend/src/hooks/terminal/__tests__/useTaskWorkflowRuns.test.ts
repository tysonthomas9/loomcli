/**
 * @vitest-environment jsdom
 */

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getTaskWorkflowRuns, type TaskWorkflowRun } from "@/api/workflows";

import { useTaskWorkflowRuns } from "../useTaskWorkflowRuns";

const workspaceMock = vi.hoisted(() => ({ id: "test-ws-id" }));

vi.mock("@/api/workflows", () => ({ getTaskWorkflowRuns: vi.fn() }));
vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: workspaceMock.id }),
  };
});

const mockGetTaskWorkflowRuns = vi.mocked(getTaskWorkflowRuns);

function run(id: string): TaskWorkflowRun {
  return {
    workspace_key: "test-ws-id",
    run_id: id,
    driver_id: "prompt-agent",
    driver_version_id: "v1",
    status: "completed",
    created_at: "2026-07-18T20:00:00Z",
    updated_at: "2026-07-18T20:00:01Z",
  };
}

function deferred<T>(): {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: Error) => void;
} {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((done, fail) => {
    resolve = done;
    reject = fail;
  });
  return { promise, resolve, reject };
}

describe("useTaskWorkflowRuns", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.clearAllMocks();
    workspaceMock.id = "test-ws-id";
  });

  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("deduplicates workflow runs by durable run id", async () => {
    mockGetTaskWorkflowRuns.mockResolvedValueOnce([run("run-1"), run("run-1")]);
    const { result } = renderHook(() => useTaskWorkflowRuns("TASK-1"));
    await act(async () => Promise.resolve());
    expect(result.current.runs.map((item) => item.run_id)).toEqual(["run-1"]);
  });

  it("uses the active polling cadence from the initial fetched status", async () => {
    mockGetTaskWorkflowRuns.mockResolvedValue([
      { ...run("active-run"), status: "running" },
    ]);
    renderHook(() => useTaskWorkflowRuns("TASK-ACTIVE"));
    await act(async () => Promise.resolve());
    expect(mockGetTaskWorkflowRuns).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(2_999);
    });
    expect(mockGetTaskWorkflowRuns).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(mockGetTaskWorkflowRuns).toHaveBeenCalledTimes(2);
  });

  it("does not commit or reschedule an old task fetch after task changes", async () => {
    const oldRequest = deferred<TaskWorkflowRun[]>();
    mockGetTaskWorkflowRuns
      .mockReturnValueOnce(oldRequest.promise)
      .mockResolvedValue([run("new-run")]);

    const { result, rerender } = renderHook(
      ({ taskId }: { taskId: string }) => useTaskWorkflowRuns(taskId),
      { initialProps: { taskId: "TASK-OLD" } },
    );
    rerender({ taskId: "TASK-NEW" });
    await act(async () => Promise.resolve());
    expect(result.current.runs.map((item) => item.run_id)).toEqual(["new-run"]);

    await act(async () => {
      oldRequest.resolve([run("stale-run")]);
      await oldRequest.promise;
    });
    expect(result.current.runs.map((item) => item.run_id)).toEqual(["new-run"]);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    // Initial old + initial new + one current-generation poll. A stale timer
    // would make this four calls and could overwrite the current task again.
    expect(mockGetTaskWorkflowRuns).toHaveBeenCalledTimes(3);
    expect(mockGetTaskWorkflowRuns).toHaveBeenLastCalledWith(
      "test-ws-id",
      "TASK-NEW",
    );
  });

  it("clears old runs and errors while a new workspace generation is pending or fails", async () => {
    mockGetTaskWorkflowRuns.mockResolvedValueOnce([run("old-run")]);
    const { result, rerender } = renderHook(
      ({ revision }: { revision: number }) => {
        void revision;
        return useTaskWorkflowRuns("TASK-1");
      },
      { initialProps: { revision: 0 } },
    );
    await act(async () => Promise.resolve());
    expect(result.current.runs.map((item) => item.run_id)).toEqual(["old-run"]);

    const nextRequest = deferred<TaskWorkflowRun[]>();
    mockGetTaskWorkflowRuns.mockReturnValueOnce(nextRequest.promise);
    workspaceMock.id = "other-ws";
    rerender({ revision: 1 });

    expect(result.current.runs).toEqual([]);
    expect(result.current.error).toBeNull();
    expect(result.current.isLoading).toBe(true);

    await act(async () => {
      nextRequest.reject(new Error("new workspace unavailable"));
      await Promise.resolve();
    });
    expect(result.current.runs).toEqual([]);
    expect(result.current.isLoading).toBe(false);
    expect(result.current.error?.message).toBe("new workspace unavailable");
  });
});
