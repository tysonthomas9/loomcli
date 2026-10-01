/**
 * @vitest-environment jsdom
 */
import { act, renderHook } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import React from "react";

import { EventContext } from "@/hooks/common/useEventProvider";
import type { SessionRecord } from "@/types/agent";

const mocks = vi.hoisted(() => ({
  getTaskSessions: vi.fn(),
}));

vi.mock("@/api/terminal", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/api/terminal")>()),
  getTaskSessions: mocks.getTaskSessions,
}));

vi.mock("@/hooks/workspace", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/workspace")>()),
  useWorkspaceContext: () => ({ workspaceId: "ws-replay" }),
}));

import { useTaskSessions } from "../useTaskSessions";

function session(id: string): SessionRecord {
  return { session_id: id, is_active: false } as SessionRecord;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

function eventValue(connected: boolean, epoch: number) {
  return {
    state: connected ? "connected" : "disconnected",
    reconnectAttempts: connected ? 0 : 1,
    lastError: null,
    isConnected: connected,
    connectionEpoch: epoch,
    subscribe: () => () => {},
    retryNow: () => {},
    disconnect: () => {},
  } as unknown as React.ContextType<typeof EventContext>;
}

afterEach(() => {
  vi.useRealTimers();
  mocks.getTaskSessions.mockReset();
});

it("#647 an older task's session read cannot commit after the task changes", async () => {
  const taskA = deferred<SessionRecord[]>();
  const taskB = deferred<SessionRecord[]>();
  mocks.getTaskSessions.mockImplementation((_ws: string, taskId: string) =>
    taskId === "task-a" ? taskA.promise : taskB.promise,
  );
  const { result, rerender } = renderHook(
    ({ taskId }) => useTaskSessions(taskId),
    { initialProps: { taskId: "task-a" } },
  );

  rerender({ taskId: "task-b" });
  await act(async () => {
    taskA.resolve([session("session-from-task-a")]);
    await taskA.promise;
  });

  expect(
    result.current.sessions.map((s) => s.session_id),
    "task A's late read committed into task B's session view",
  ).not.toContain("session-from-task-a");
});

it("#647 a 404 session read is not reported as an empty session list", async () => {
  const { api, ApiError } = await import("@/api/common");
  const getSpy = vi.spyOn(api, "GET").mockResolvedValue({
    data: undefined,
    error: { error: "workspace not found" },
    response: new Response(null, { status: 404, statusText: "Not Found" }),
  } as never);
  const { getTaskSessions } =
    await vi.importActual<typeof import("@/api/terminal")>("@/api/terminal");

  await expect(
    getTaskSessions("ws-replay", "task-a"),
    "404 was converted into an authoritative empty session list",
  ).rejects.toBeInstanceOf(ApiError);
  getSpy.mockRestore();
});

it("V1 a completed SSE reconnect re-reads task sessions before the next poll", async () => {
  vi.useFakeTimers();
  mocks.getTaskSessions.mockResolvedValue([session("s-1")]);
  let value = eventValue(true, 1);
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <EventContext.Provider value={value}>{children}</EventContext.Provider>
  );
  const { rerender } = renderHook(() => useTaskSessions("task-a"), {
    wrapper,
  });
  await act(async () => {});
  const readsBeforeGap = mocks.getTaskSessions.mock.calls.length;
  expect(readsBeforeGap).toBeGreaterThan(0);

  value = eventValue(false, 1);
  rerender();
  value = eventValue(true, 2);
  rerender();
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1_000);
  });

  expect(
    mocks.getTaskSessions.mock.calls.length,
    "reconnect gap left the session view waiting for its 10s poll",
  ).toBeGreaterThan(readsBeforeGap);
});
