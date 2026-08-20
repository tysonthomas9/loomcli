import { describe, expect, it } from "vitest";

import type { Event } from "@/types";

import { foldJourney } from "../journeyFold";

const BASE_MS = Date.parse("2026-08-20T12:00:00.000Z");

function event(
  seconds: number,
  eventType: string,
  overrides: Partial<Event> = {},
): Event {
  return {
    id: `${BASE_MS + seconds * 1_000}-0`,
    issue_id: "loom-1",
    event_type: eventType,
    actor: "alice",
    created_at: new Date(BASE_MS + seconds * 1_000).toISOString(),
    ...overrides,
  };
}

describe("foldJourney", () => {
  it("folds an ordinary lifecycle into ordered stage spans", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.claim", { actor: "worker-1" }),
        event(40, "issue.close", { actor: "worker-1" }),
      ],
      BASE_MS + 60_000,
    );

    expect(spans).toEqual([
      {
        stage: "Open",
        owner: "alice",
        start: "2026-08-20T12:00:00.000Z",
        end: "2026-08-20T12:00:10.000Z",
        durationMs: 10_000,
      },
      {
        stage: "In progress",
        owner: "worker-1",
        start: "2026-08-20T12:00:10.000Z",
        end: "2026-08-20T12:00:40.000Z",
        durationMs: 30_000,
      },
      {
        stage: "Closed",
        owner: "worker-1",
        start: "2026-08-20T12:00:40.000Z",
        end: "2026-08-20T12:00:40.000Z",
        durationMs: 0,
      },
    ]);
  });

  it("keeps an in-flight task open and measures it through now", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(5, "issue.claim", { actor: "worker-1" }),
      ],
      BASE_MS + 15_000,
    );

    expect(spans.at(-1)).toEqual({
      stage: "In progress",
      owner: "worker-1",
      start: "2026-08-20T12:00:05.000Z",
      end: null,
      durationMs: 10_000,
    });
  });

  it("keeps an in-flight span whose start is later than now", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.close", { actor: "closer" }),
        event(20, "issue.reopen", { actor: "reopener" }),
      ],
      BASE_MS + 15_000,
    );

    expect(spans.at(-1)).toEqual({
      stage: "Open",
      owner: null,
      start: "2026-08-20T12:00:20.000Z",
      end: null,
      durationMs: 0,
    });
  });

  it("labels an agent-declared blocked status as Stuck", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(5, "issue.claim", { actor: "worker-1" }),
        event(10, "issue.update", {
          actor: "worker-1",
          changes: [
            { field: "status", before: "in_progress", after: "blocked" },
          ],
        }),
      ],
      BASE_MS + 25_000,
    );

    expect(spans.at(-1)).toMatchObject({
      stage: "Stuck",
      owner: "worker-1",
      end: null,
      durationMs: 15_000,
    });
    expect(spans.map(({ stage }) => stage)).not.toContain("Blocked");
  });

  it.each(["issue.defer", "issue.deferred"])(
    "folds an action-only %s and undefer round trip without changing owner",
    (deferAction) => {
      const spans = foldJourney(
        [
          event(0, "issue.create"),
          event(5, "issue.claim", { actor: "worker-1" }),
          event(10, deferAction, { actor: "scheduler" }),
          event(20, "issue.undefer", { actor: "scheduler" }),
        ],
        BASE_MS + 30_000,
      );

      expect(
        spans.map(({ stage, owner, durationMs }) => ({
          stage,
          owner,
          durationMs,
        })),
      ).toEqual([
        { stage: "Open", owner: "alice", durationMs: 5_000 },
        { stage: "In progress", owner: "worker-1", durationMs: 5_000 },
        { stage: "Deferred", owner: "worker-1", durationMs: 10_000 },
        { stage: "Open", owner: "worker-1", durationMs: 10_000 },
      ]);
    },
  );

  it.each(["issue.reopen", "issue.reopened"])(
    "clears the owner when %s begins an Open stage",
    (reopenAction) => {
      const spans = foldJourney(
        [
          event(0, "issue.create"),
          event(5, "issue.assign", {
            actor: "dispatcher",
            metadata: { assignee: "worker-2" },
          }),
          event(10, "issue.close", { actor: "closer" }),
          event(20, reopenAction, { actor: "reopener" }),
        ],
        BASE_MS + 30_000,
      );

      expect(spans.at(-1)).toEqual({
        stage: "Open",
        owner: null,
        start: "2026-08-20T12:00:20.000Z",
        end: null,
        durationMs: 10_000,
      });
    },
  );

  it("sorts out-of-order events and ignores labels and unknown actions", () => {
    const spans = foldJourney(
      [
        event(30, "issue.close", { actor: "closer" }),
        event(15, "label.add", {
          metadata: { label: "loom:quarantined" },
        }),
        event(25, "future.action"),
        event(0, "issue.create"),
        event(10, "issue.assign", {
          actor: "dispatcher",
          metadata: { assignee: "worker-2" },
        }),
        event(20, "issue.release", { actor: "worker-2" }),
      ],
      BASE_MS + 40_000,
    );

    expect(
      spans.map(({ stage, owner, durationMs }) => ({
        stage,
        owner,
        durationMs,
      })),
    ).toEqual([
      { stage: "Open", owner: "alice", durationMs: 10_000 },
      { stage: "Open", owner: "worker-2", durationMs: 10_000 },
      { stage: "Open", owner: null, durationMs: 10_000 },
      { stage: "Closed", owner: null, durationMs: 0 },
    ]);
  });

  it("treats an empty assignment as unassigned", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.claim", { actor: "worker-1" }),
        event(39, "issue.assign", {
          actor: "dispatcher",
          metadata: { assignee: "" },
        }),
        event(40, "issue.close", { actor: "worker-1" }),
      ],
      BASE_MS + 60_000,
    );

    expect(
      spans.map(({ stage, owner, durationMs }) => ({
        stage,
        owner,
        durationMs,
      })),
    ).toEqual([
      { stage: "Open", owner: "alice", durationMs: 10_000 },
      { stage: "In progress", owner: "worker-1", durationMs: 29_000 },
      { stage: "In progress", owner: null, durationMs: 1_000 },
      { stage: "Closed", owner: null, durationMs: 0 },
    ]);
  });

  it("drops a zero-duration unassigned span but carries it into Closed", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.claim", { actor: "worker-1" }),
        event(40, "issue.assign", {
          actor: "dispatcher",
          metadata: { assignee: "" },
        }),
        event(40, "issue.close", { actor: "worker-1" }),
      ],
      BASE_MS + 60_000,
    );

    expect(spans).toEqual([
      {
        stage: "Open",
        owner: "alice",
        start: "2026-08-20T12:00:00.000Z",
        end: "2026-08-20T12:00:10.000Z",
        durationMs: 10_000,
      },
      {
        stage: "In progress",
        owner: "worker-1",
        start: "2026-08-20T12:00:10.000Z",
        end: "2026-08-20T12:00:40.000Z",
        durationMs: 30_000,
      },
      {
        stage: "Closed",
        owner: null,
        start: "2026-08-20T12:00:40.000Z",
        end: "2026-08-20T12:00:40.000Z",
        durationMs: 0,
      },
    ]);
  });

  it("drops an instantaneous stage without merging its surrounding spans", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.claim", { actor: "worker-1" }),
        event(20, "issue.update", {
          actor: "worker-1",
          changes: [
            { field: "status", before: "in_progress", after: "review" },
          ],
        }),
        event(20, "issue.update", {
          actor: "worker-1",
          changes: [
            { field: "status", before: "review", after: "in_progress" },
          ],
        }),
        event(30, "issue.close", { actor: "worker-1" }),
      ],
      BASE_MS + 40_000,
    );

    expect(spans).toEqual([
      {
        stage: "Open",
        owner: "alice",
        start: "2026-08-20T12:00:00.000Z",
        end: "2026-08-20T12:00:10.000Z",
        durationMs: 10_000,
      },
      {
        stage: "In progress",
        owner: "worker-1",
        start: "2026-08-20T12:00:10.000Z",
        end: "2026-08-20T12:00:20.000Z",
        durationMs: 10_000,
      },
      {
        stage: "In progress",
        owner: "worker-1",
        start: "2026-08-20T12:00:20.000Z",
        end: "2026-08-20T12:00:30.000Z",
        durationMs: 10_000,
      },
      {
        stage: "Closed",
        owner: "worker-1",
        start: "2026-08-20T12:00:30.000Z",
        end: "2026-08-20T12:00:30.000Z",
        durationMs: 0,
      },
    ]);
  });

  it("returns an empty list for an empty event window", () => {
    expect(foldJourney([], BASE_MS)).toEqual([]);
  });
});
