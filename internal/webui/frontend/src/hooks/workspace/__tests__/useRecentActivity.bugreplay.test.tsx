/**
 * @vitest-environment jsdom
 */
import { act, renderHook, waitFor } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import React from "react";

import * as api from "@/api";
import { EventContext } from "@/hooks/common/useEventProvider";
import type { Event, Issue } from "@/types";

import { useRecentActivity } from "../useRecentActivity";

vi.mock("@/api", () => ({
  getIssueEvents: vi.fn(),
}));

const getIssueEvents = vi.mocked(api.getIssueEvents);

function issue(id: string, updatedAt: string): Issue {
  return {
    id,
    title: id,
    priority: 2,
    created_at: updatedAt,
    updated_at: updatedAt,
  } as Issue;
}

function claim(issueId: string): Event {
  return {
    id: `${issueId}-claim`,
    issue_id: issueId,
    event_type: "issue.claim",
    actor: "agent-dev-1",
    old_value: null,
    new_value: null,
    comment: null,
    created_at: "2026-08-21T15:48:02.000Z",
  } as Event;
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

it("#649 a skipped failed issue history is re-read after an SSE gap", async () => {
  let task2Reads = 0;
  getIssueEvents.mockImplementation(async (_ws, issueId) => {
    if (issueId === "TASK-2" && task2Reads++ === 0) {
      throw new Error("history temporarily unavailable");
    }
    return [claim(issueId)];
  });
  const issues = [
    issue("TASK-1", "2026-08-21T15:48:02.000Z"),
    issue("TASK-2", "2026-08-21T15:47:02.000Z"),
  ];
  let value = eventValue(true, 1);
  const wrapper = ({ children }: { children: React.ReactNode }) => (
    <EventContext.Provider value={value}>{children}</EventContext.Provider>
  );
  const { result, rerender } = renderHook(
    () => useRecentActivity("WS", issues, []),
    { wrapper },
  );
  await waitFor(() => expect(result.current).toHaveLength(1));
  expect(result.current.map((item) => item.issueId)).toEqual(["TASK-1"]);

  value = eventValue(false, 1);
  rerender();
  value = eventValue(true, 2);
  rerender();
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 20));
  });

  expect(
    result.current.map((item) => item.issueId).sort(),
    "the seed treated TASK-2's failed history as done and never re-read it",
  ).toEqual(["TASK-1", "TASK-2"]);
});
