/**
 * @vitest-environment jsdom
 */

import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as api from "@/api";
import { useResyncSubscription } from "@/hooks/common";
import type { Event, Issue, LoomAgentStatus, MutationPayload } from "@/types";

import {
  describeIssueEvent,
  describeMutation,
  activityIdForMutation,
  mergeRecentActivity,
  useRecentActivity,
} from "../useRecentActivity";

vi.mock("@/api", () => ({
  getIssueEvents: vi.fn(),
}));

vi.mock("@/hooks/common", () => ({
  useEventSubscription: vi.fn(),
  useResyncSubscription: vi.fn(),
}));

const mockGetIssueEvents = vi.mocked(api.getIssueEvents);

function fleetEvent(overrides: Partial<Event>): Event {
  return {
    id: "1",
    issue_id: "TASK-1",
    event_type: "issue.update",
    actor: "agent-dev-1",
    old_value: null,
    new_value: null,
    comment: null,
    created_at: "2026-08-21T15:48:02.000Z",
    ...overrides,
  } as Event;
}

describe("describeMutation", () => {
  it("uses status changes for activity wording and markers", () => {
    const mutation: MutationPayload = {
      type: "status",
      timestamp: "2026-08-21T15:48:02.000Z",
      actor: "agent-architect-1",
      new_status: "review",
    };

    expect(describeMutation(mutation, new Set(["agent-architect-1"]))).toEqual({
      text: "set to review",
      marker: "rev",
      status: "review",
    });
  });

  it("marks an unknown actor as an operator when no status marker applies", () => {
    const mutation: MutationPayload = {
      type: "update",
      action: "issue.update",
      timestamp: "2026-08-21T15:48:02.000Z",
      actor: "tyson",
    };

    expect(describeMutation(mutation, new Set(["agent-dev-1"]))).toEqual({
      text: "updated",
      marker: "op",
    });
  });
});

describe("mergeRecentActivity", () => {
  it("deduplicates by id and keeps distinct same-second issue events", () => {
    const rows = mergeRecentActivity(
      [
        {
          id: "one",
          timestamp: "2026-08-21T15:00:00.000Z",
          actor: "agent-1",
          issueId: "TASK-1",
          text: "updated",
          marker: "default",
        },
      ],
      [
        {
          id: "one",
          timestamp: "2026-08-21T15:01:00.000Z",
          actor: "agent-1",
          issueId: "TASK-1",
          text: "updated",
          marker: "default",
        },
        {
          id: "two",
          timestamp: "2026-08-21T15:02:00.000Z",
          actor: "agent-2",
          issueId: "TASK-2",
          text: "closed",
          marker: "ok",
        },
        {
          id: "three",
          timestamp: "2026-08-21T15:02:00.000Z",
          actor: "agent-2",
          issueId: "TASK-2",
          text: "assigned",
          marker: "default",
        },
      ],
    );

    expect(rows.map((row) => row.id)).toEqual(["two", "three", "one"]);
  });
});

describe("activityIdForMutation", () => {
  it("uses the durable cursor shared with issue history", () => {
    expect(
      activityIdForMutation({
        cursor: "1787591234567-0",
        type: "update",
        issue_id: "TASK-1",
        timestamp: "2026-08-21T15:48:02.000Z",
      }),
    ).toBe("event-1787591234567-0");
  });

  it("deduplicates an SSE delivery against the same seeded history event", () => {
    const cursor = "1787591234567-0";
    const seeded = describeIssueEvent(fleetEvent({ id: cursor }), new Set());
    const live = {
      ...seeded,
      id: activityIdForMutation({
        cursor,
        type: "update",
        issue_id: "TASK-1",
        timestamp: seeded.timestamp,
      }),
    };

    expect(mergeRecentActivity([live], [seeded])).toHaveLength(1);
  });

  it("retains a stable fallback when a mutation has no cursor", () => {
    expect(
      activityIdForMutation({
        type: "update",
        issue_id: "TASK-1",
        timestamp: "2026-08-21T15:48:02.000Z",
      }),
    ).toBe("mutation-TASK-1-2026-08-21T15:48:02.000Z-update");
  });
});

describe("describeIssueEvent", () => {
  it.each([
    ["issue.create", null, "created", "default"],
    ["issue.claim", null, "claimed", "default"],
    ["issue.assign", "agent-plan-1", "assigned to agent-plan-1", "default"],
    ["issue.release", null, "released", "default"],
    ["issue.update", "review", "set to review", "rev"],
    ["issue.close", null, "closed", "ok"],
    ["issue.reopen", null, "reopened", "default"],
    ["issue.defer", null, "deferred", "default"],
    ["issue.undefer", null, "undeferred", "default"],
    ["issue.comment", null, "commented", "default"],
    ["issue.label", null, "changed labels", "default"],
  ])("maps fleet-db %s events", (eventType, newValue, text, marker) => {
    const row = describeIssueEvent(
      fleetEvent({ event_type: eventType, new_value: newValue }),
      new Set(["agent-dev-1"]),
    );

    expect(row).toMatchObject({ text, marker, issueId: "TASK-1" });
  });

  it("uses status changes in an optional widened wire response", () => {
    const row = describeIssueEvent(
      fleetEvent({
        event_type: "issue.update",
        summary: "Updated status and updated_at",
        changes: [{ field: "status", before: "open", after: "blocked" }],
      } as Event),
      new Set(["agent-dev-1"]),
    );

    expect(row).toMatchObject({ text: "set to blocked", marker: "bad" });
  });

  it("uses the cleaned summary when the widened wire has no status change", () => {
    const row = describeIssueEvent(
      fleetEvent({
        event_type: "issue.update",
        summary: "Updated title and updated_at",
      } as Event),
      new Set(["agent-dev-1"]),
    );

    expect(row.text).toBe("updated title");
  });

  it("ignores summaries that restate the actor on non-update events", () => {
    const row = describeIssueEvent(
      fleetEvent({
        event_type: "issue.claim",
        actor: "local-planner",
        summary: "Claimed by local-planner",
      } as Event),
      new Set(["local-planner"]),
    );

    expect(row.text).toBe("claimed");
  });
});

describe("useRecentActivity", () => {
  const issue = (id: string, updatedAt: string, sourceRepo?: string): Issue =>
    ({
      id,
      title: id,
      status: "open",
      issue_type: "task",
      updated_at: updatedAt,
      source_repo: sourceRepo,
    }) as unknown as Issue;
  const agent = (name: string): LoomAgentStatus =>
    ({ name, role: "task", ahead: 0 }) as unknown as LoomAgentStatus;

  beforeEach(() => {
    mockGetIssueEvents.mockReset();
    vi.mocked(useResyncSubscription).mockClear();
  });

  it("seeds from the issue trails and survives agents arriving mid-fetch", async () => {
    let release: (() => void) | undefined;
    mockGetIssueEvents.mockImplementation(
      (_ws, issueId) =>
        new Promise<Event[]>((resolve) => {
          release = () =>
            resolve([
              fleetEvent({
                id: `${issueId}-claim`,
                issue_id: issueId,
                event_type: "issue.claim",
                actor: "agent-dev-1",
              }),
            ]);
        }),
    );

    const issues = [issue("TASK-1", "2026-08-21T15:48:02.000Z", "source-repo")];
    const { result, rerender } = renderHook(
      ({ agents }) => useRecentActivity("WS", issues, agents),
      { initialProps: { agents: [] as LoomAgentStatus[] } },
    );
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
    expect(mockGetIssueEvents).toHaveBeenCalledWith("WS", "TASK-1", 15);

    // Agents load while the seed is still in flight: must not abort it.
    rerender({ agents: [agent("agent-dev-1")] });
    release?.();

    await waitFor(() => expect(result.current).toHaveLength(1));
    expect(result.current[0]).toMatchObject({
      issueId: "TASK-1",
      text: "claimed",
      sourceRepo: "source-repo",
      // agent-dev-1 is a known agent once agents load, so not an operator.
      marker: "default",
    });
    expect(mockGetIssueEvents).toHaveBeenCalledTimes(1);
  });

  it("rebuilds once when the provider reports a resync", async () => {
    mockGetIssueEvents
      .mockResolvedValueOnce([fleetEvent({ id: "before" })])
      .mockResolvedValueOnce([fleetEvent({ id: "after" })]);
    const issues = [issue("TASK-1", "2026-08-21T15:48:02.000Z")];
    renderHook(() => useRecentActivity("WS", issues, []));
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));

    const onResync = vi.mocked(useResyncSubscription).mock.calls.at(-1)?.at(0);
    expect(onResync).toBeTypeOf("function");
    act(() => {
      onResync?.({ from: "c1.before", to: "c1.after", reason: "overflow" });
    });

    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(2));
  });
});
