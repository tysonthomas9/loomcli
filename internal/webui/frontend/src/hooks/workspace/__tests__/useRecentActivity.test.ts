/**
 * @vitest-environment jsdom
 */

import { createElement, type ReactNode } from "react";
import {
  QueryRecoveryCoordinator,
  QueryRecoveryContext,
} from "@/hooks/common/queryRecovery";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import * as api from "@/api";
import { useEventSubscription } from "@/hooks/common";
import type { Event, Issue, LoomAgentStatus, MutationPayload } from "@/types";

import {
  describeIssueEvent,
  describeMutation,
  activityIdForMutation,
  mergeRecentActivity,
  useRecentActivity,
} from "../useRecentActivity";

const storeSnapshot = vi.hoisted(() => ({
  issuesMap: new Map<string, Issue>(),
}));
const store = { getState: () => storeSnapshot };
vi.mock("@/hooks/common/useStoreContext", () => ({
  useIssueStoreInstance: () => store,
}));

vi.mock("@/api", () => ({
  getIssueEvents: vi.fn(),
}));

vi.mock("@/hooks/common", () => ({
  useEventSubscription: vi.fn(),
  useEventContext: () => ({ connectionEpoch: 0 }),
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
    vi.mocked(useEventSubscription).mockClear();
    storeSnapshot.issuesMap = new Map();
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
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    const { result, rerender } = renderHook(
      ({ agents }) => useRecentActivity("WS", issues, agents),
      { initialProps: { agents: [] as LoomAgentStatus[] } },
    );
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
    expect(mockGetIssueEvents).toHaveBeenCalledWith("WS", "TASK-1", 15, {
      signal: expect.any(AbortSignal),
    });

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

  const recoveryWrapper = (coordinator: QueryRecoveryCoordinator) =>
    function RecoveryWrapper({ children }: { children: ReactNode }) {
      return createElement(
        QueryRecoveryContext.Provider,
        { value: coordinator },
        children,
      );
    };

  it("rebuilds through the coordinator with successful bounded history", async () => {
    mockGetIssueEvents
      .mockResolvedValueOnce([fleetEvent({ id: "before" })])
      .mockResolvedValueOnce([fleetEvent({ id: "after" })]);
    const issues = [issue("TASK-1", "2026-08-21T15:48:02.000Z")];
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    const coordinator = new QueryRecoveryCoordinator();
    const { result } = renderHook(() => useRecentActivity("WS", issues, []), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() => expect(result.current).toHaveLength(1));
    await act(async () => coordinator.refresh());
    expect(mockGetIssueEvents).toHaveBeenCalledTimes(2);
    expect(result.current).toHaveLength(2);
  });

  it("rejects any selected history failure without committing partial results", async () => {
    const issues = [issue("A", "2026-08-22"), issue("B", "2026-08-21")];
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    mockGetIssueEvents.mockResolvedValue([]);
    const coordinator = new QueryRecoveryCoordinator();
    const { result } = renderHook(() => useRecentActivity("WS", issues, []), {
      wrapper: recoveryWrapper(coordinator),
    });
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(2));
    mockGetIssueEvents
      .mockResolvedValueOnce([fleetEvent({ id: "partial", issue_id: "A" })])
      .mockRejectedValueOnce(new Error("history unavailable"));
    await act(async () => {
      await expect(coordinator.refresh()).rejects.toThrow(
        "history unavailable",
      );
    });
    expect(result.current).toEqual([]);
  });

  it.each([false, true])(
    "rechecks issue-map changes before React renders (same IDs: %s)",
    async (sameIds) => {
      const issues = [issue("A", "2026-08-21")];
      storeSnapshot.issuesMap = new Map(
        issues.map((issue) => [issue.id, issue]),
      );
      mockGetIssueEvents.mockResolvedValue([]);
      const coordinator = new QueryRecoveryCoordinator();
      let finishIssues!: () => void;
      coordinator.register(
        "issues",
        () =>
          new Promise<void>((resolve) => {
            finishIssues = resolve;
          }),
      );
      const { result } = renderHook(() => useRecentActivity("WS", issues, []), {
        wrapper: recoveryWrapper(coordinator),
      });
      await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
      let recovery!: Promise<void>;
      act(() => {
        recovery = coordinator.refresh();
      });
      await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(2));
      await act(async () => {
        storeSnapshot.issuesMap = new Map([["A", issue("A", "2026-08-23")]]);
        if (!sameIds)
          storeSnapshot.issuesMap.set("B", issue("B", "2026-08-24"));
        mockGetIssueEvents.mockImplementation(async (_ws, id) => [
          fleetEvent({ id: `${id}-recovered`, issue_id: id }),
        ]);
        finishIssues();
        await recovery;
      });
      expect(
        mockGetIssueEvents.mock.calls.slice(2).map((call) => call[1]),
      ).toEqual(sameIds ? ["A"] : ["B", "A"]);
      expect(result.current.map((item) => item.id)).toEqual(
        expect.arrayContaining(
          sameIds
            ? ["event-A-recovered"]
            : ["event-A-recovered", "event-B-recovered"],
        ),
      );
    },
  );

  it("fences old A history through an A to B to A workspace transition", async () => {
    const issues = [issue("TASK-1", "2026-08-21")];
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    let finishOld!: (events: Event[]) => void;
    mockGetIssueEvents
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishOld = resolve;
        }),
      )
      .mockResolvedValue([]);
    const { result, rerender } = renderHook(
      ({ workspace }) => useRecentActivity(workspace, issues, []),
      { initialProps: { workspace: "A" } },
    );
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
    rerender({ workspace: "B" });
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(2));
    rerender({ workspace: "A" });
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(3));
    await act(async () => finishOld([fleetEvent({ id: "stale-A" })]));
    expect(result.current).toEqual([]);
  });
  it("preserves live events during history recovery and deduplicates source IDs", async () => {
    const issues = [issue("TASK-1", "2026-08-21")];
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    let finish!: (events: Event[]) => void;
    mockGetIssueEvents.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const { result } = renderHook(() => useRecentActivity("WS", issues, []));
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
    const onMutation = vi.mocked(useEventSubscription).mock.calls.at(-1)?.[0];
    act(() =>
      onMutation?.({
        workspace_id: "WS",
        cursor: "shared",
        type: "update",
        issue_id: "TASK-1",
        timestamp: "2026-08-21T15:48:02.000Z",
      }),
    );
    await act(async () =>
      finish([
        fleetEvent({ id: "shared" }),
        fleetEvent({ id: "history-only" }),
      ]),
    );
    expect(result.current.map((item) => item.id).sort()).toEqual([
      "event-history-only",
      "event-shared",
    ]);
  });

  it("reads at most five non-epic trails at fifteen events each", async () => {
    const issues = Array.from({ length: 7 }, (_, index) =>
      issue(`TASK-${index}`, `2026-08-${21 + index}`),
    );
    issues.push({ ...issue("EPIC", "2026-08-31"), issue_type: "epic" });
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    mockGetIssueEvents.mockResolvedValue([]);
    renderHook(() => useRecentActivity("WS", issues, []));
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(5));
    expect(mockGetIssueEvents.mock.calls.map((call) => call[1])).toEqual([
      "TASK-6",
      "TASK-5",
      "TASK-4",
      "TASK-3",
      "TASK-2",
    ]);
    expect(mockGetIssueEvents.mock.calls.every((call) => call[2] === 15)).toBe(
      true,
    );
  });
  it("restarts and fences history when the coordinator changes with the same workspace and seed", async () => {
    const issues = [issue("TASK-1", "2026-08-21", "repo-a")];
    storeSnapshot.issuesMap = new Map(issues.map((issue) => [issue.id, issue]));
    let finishOld!: (events: Event[]) => void;
    mockGetIssueEvents
      .mockReturnValueOnce(
        new Promise((resolve) => {
          finishOld = resolve;
        }),
      )
      .mockResolvedValueOnce([fleetEvent({ id: "new-owner" })]);
    let coordinator = new QueryRecoveryCoordinator("first");
    function Wrapper({ children }: { children: ReactNode }) {
      return createElement(
        QueryRecoveryContext.Provider,
        { value: coordinator },
        children,
      );
    }
    const { result, rerender } = renderHook(
      () => useRecentActivity("WS", issues, []),
      { wrapper: Wrapper },
    );
    await waitFor(() => expect(mockGetIssueEvents).toHaveBeenCalledTimes(1));
    const oldSignal = mockGetIssueEvents.mock.calls[0]?.[3]?.signal;
    coordinator = new QueryRecoveryCoordinator("second");
    rerender();
    await waitFor(() =>
      expect(result.current.map((item) => item.id)).toEqual([
        "event-new-owner",
      ]),
    );
    expect(oldSignal?.aborted).toBe(true);
    await act(async () => finishOld([fleetEvent({ id: "old-owner" })]));
    expect(result.current.map((item) => item.id)).toEqual(["event-new-owner"]);
    expect(mockGetIssueEvents).toHaveBeenCalledTimes(2);
  });
});
