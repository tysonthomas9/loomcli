import { describe, expect, it } from "vitest";

import type { Event } from "@/types";

import { foldJourney } from "../journeyFold";
import {
  formatJourneyClock,
  formatJourneyDuration,
  journeyStageForStatus,
} from "../journeyPresentation";

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

describe("journey presentation", () => {
  it("treats blocked as a halt rather than a stage", () => {
    expect(journeyStageForStatus("blocked")).toBeNull();
    expect(journeyStageForStatus(" in_progress ")).toBe("In progress");
  });

  it.each([
    [37_999, "37s"],
    [13 * 60_000 + 26_000, "13m 26s"],
    [3_600_000 + 4 * 60_000 + 54_000, "1h 04m 54s"],
    [2 * 86_400_000 + 3 * 3_600_000 + 41 * 60_000 + 59_000, "2d 03h 41m"],
  ])("formats %i milliseconds as %s", (durationMs, expected) => {
    expect(formatJourneyDuration(durationMs)).toBe(expected);
  });

  it("formats local clocks and rejects unparseable input", () => {
    const local = new Date(2026, 7, 20, 9, 5, 7).toISOString();
    expect(formatJourneyClock(local)).toBe("09:05:07");
    expect(formatJourneyClock("not-a-clock")).toBe("");
  });
});

describe("foldJourney", () => {
  it("folds an ordinary lifecycle and nests every transition event", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(10, "issue.claim", { actor: "agent-dev-1" }),
        event(40, "issue.close", { actor: "agent-dev-1" }),
      ],
      BASE_MS + 60_000,
    );

    expect(
      spans.map(({ stage, owner, durationMs, haltedMs }) => ({
        stage,
        owner,
        durationMs,
        haltedMs,
      })),
    ).toEqual([
      { stage: "Open", owner: null, durationMs: 10_000, haltedMs: 0 },
      {
        stage: "In progress",
        owner: "agent-dev-1",
        durationMs: 30_000,
        haltedMs: 0,
      },
      {
        stage: "Closed",
        owner: "agent-dev-1",
        durationMs: 0,
        haltedMs: 0,
      },
    ]);
    expect(spans[0]?.events.map(({ id }) => id)).toEqual([`${BASE_MS}-0`]);
    expect(spans[1]?.events.map(({ id }) => id)).toEqual([
      `${BASE_MS + 10_000}-0`,
    ]);
    expect(spans[2]?.events.map(({ id }) => id)).toEqual([
      `${BASE_MS + 40_000}-0`,
    ]);
    expect(spans[1]?.events[0]).toMatchObject({
      actor: "agent-dev-1",
      actorKind: "agent",
    });
  });

  it("retains the initial Open span when a task is claimed within one second", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(0.434, "issue.claim", { actor: "agent-dev-1" }),
      ],
      BASE_MS + 10_000,
    );

    expect(
      spans.map(({ stage, owner, events }) => ({
        stage,
        owner,
        events: events.map(({ id }) => id),
      })),
    ).toEqual([
      {
        stage: "Open",
        owner: null,
        events: [`${BASE_MS}-0`],
      },
      {
        stage: "In progress",
        owner: "agent-dev-1",
        events: [`${BASE_MS + 434}-0`],
      },
    ]);
  });

  it("treats the creator as separate from the initial assignee", () => {
    const neverAssigned = foldJourney(
      [
        event(0, "issue.create", { actor: "creator" }),
        event(10, "issue.close", { actor: "closer" }),
      ],
      BASE_MS + 20_000,
    );
    expect(neverAssigned.map(({ stage, owner }) => ({ stage, owner }))).toEqual(
      [
        { stage: "Open", owner: null },
        { stage: "Closed", owner: null },
      ],
    );

    const assignedOnCreate = foldJourney(
      [
        event(0, "issue.created", {
          actor: "creator",
          metadata: { assignee: "agent-dev-1" },
        }),
      ],
      BASE_MS + 10_000,
    );
    expect(assignedOnCreate[0]?.owner).toBe("agent-dev-1");
  });

  it("folds a notes-first block into a halt inside its enclosing span", () => {
    const spans = foldJourney(
      [
        event(0, "issue.create"),
        event(5, "issue.claim", { actor: "agent-dev-1" }),
        event(10, "issue.update", {
          actor: "agent-dev-1",
          summary: "Updated notes and updated_at",
          changes: [
            {
              field: "notes",
              after: "  BLOCKED: toolchain unavailable  ",
            },
            { field: "updated_at", after: "2026-08-20T12:00:10.000Z" },
          ],
        }),
        event(10.003, "issue.update", {
          actor: "agent-dev-1",
          changes: [
            { field: "status", before: "in_progress", after: "blocked" },
            { field: "updated_at", after: "2026-08-20T12:00:10.003Z" },
          ],
        }),
        event(15, "issue.label_added", {
          actor: "system",
          new_value: "needs-operator",
        }),
        event(29.997, "issue.update", {
          actor: "operator",
          summary: "Updated notes and updated_at",
          changes: [
            { field: "notes", after: "added go 1.24 to the agent image" },
            { field: "updated_at", after: "2026-08-20T12:00:29.997Z" },
          ],
        }),
        event(30, "issue.status_changed", {
          actor: "operator",
          old_value: "blocked",
          new_value: "review",
        }),
      ],
      BASE_MS + 40_000,
    );

    const inProgress = spans.find((span) => span.stage === "In progress");
    expect(inProgress).toMatchObject({
      owner: "agent-dev-1",
      durationMs: 25_000,
      haltedMs: 19_997,
    });
    expect(inProgress?.halts).toHaveLength(1);
    expect(inProgress?.halts[0]).toMatchObject({
      start: "2026-08-20T12:00:10.003Z",
      end: "2026-08-20T12:00:30.000Z",
      durationMs: 19_997,
      note: "toolchain unavailable",
      clearedNote: "added go 1.24 to the agent image",
      endFraction: 1,
    });
    expect(inProgress?.halts[0]?.startFraction).toBeCloseTo(0.20012, 4);
    expect(inProgress?.halts[0]?.events.map(({ text }) => text)).toEqual([
      "agent-dev-1 changed status from In Progress to Blocked",
      "system added label needs-operator",
      "operator changed status from Blocked to Review",
    ]);
    expect(
      inProgress?.events.some(({ text }) => text === "Updated notes"),
    ).toBe(false);
    expect(spans.at(-1)?.stage).toBe("Review");
  });

  it("keeps a halt open and ticking at the end of the window", () => {
    const spans = foldJourney(
      [
        event(0, "issue.claim", { actor: "agent-dev-1" }),
        event(5, "issue.blocked", {
          actor: "agent-dev-1",
        }),
      ],
      BASE_MS + 20_000,
    );

    expect(spans).toHaveLength(1);
    expect(spans[0]).toMatchObject({
      stage: "In progress",
      durationMs: 20_000,
      haltedMs: 15_000,
    });
    expect(spans[0]?.halts[0]).toMatchObject({
      end: null,
      durationMs: 15_000,
      note: null,
      clearedNote: null,
      startFraction: 0.25,
      endFraction: 1,
    });
  });

  it("leaves a halt quote-less without a preceding BLOCKED note", () => {
    const spans = foldJourney(
      [
        event(0, "issue.claim", { actor: "agent-dev-1" }),
        event(5, "issue.update", {
          actor: "agent-dev-1",
          changes: [
            { field: "status", before: "in_progress", after: "blocked" },
            { field: "updated_at", after: "2026-08-20T12:00:05.000Z" },
          ],
        }),
      ],
      BASE_MS + 10_000,
    );

    expect(spans[0]?.halts[0]).toMatchObject({
      note: null,
      clearedNote: null,
    });
  });

  it("opens an In progress span before a halt when the window has no span", () => {
    const spans = foldJourney(
      [event(5, "issue.block", { actor: "agent-dev-1" })],
      BASE_MS + 10_000,
    );

    expect(spans[0]).toMatchObject({
      stage: "In progress",
      owner: "agent-dev-1",
      haltedMs: 5_000,
    });
    expect(spans[0]?.halts[0]?.events).toHaveLength(1);
  });

  it("keeps comments, labels, dependencies, and unknown actions as audit rows", () => {
    const source = [
      event(0, "issue.created"),
      event(1, "issue.commented", { actor: "agent-scout" }),
      event(2, "issue.label_added", { new_value: "ui" }),
      event(3, "issue.dependency_added", { new_value: "loom-2" }),
      event(4, "future.action", { actor: "system" }),
    ];

    const spans = foldJourney(source, BASE_MS + 10_000);
    expect(spans[0]?.events).toHaveLength(source.length);
    expect(spans[0]?.events.map(({ text }) => text)).toContain(
      "agent-scout commented",
    );
  });

  it("preserves a sub-second ownership transition as its own stage", () => {
    const source = [
      event(0, "issue.create"),
      event(10, "issue.claim", { actor: "agent-dev-1" }),
      event(20, "issue.status_changed", {
        actor: "agent-dev-1",
        old_value: "in_progress",
        new_value: "review",
      }),
      event(20.4, "issue.assign", {
        actor: "dispatcher",
        metadata: { assignee: "agent-review-1" },
      }),
    ];

    const spans = foldJourney(source, BASE_MS + 30_000);
    const reviews = spans.filter((span) => span.stage === "Review");
    expect(reviews).toHaveLength(2);
    expect(reviews.map(({ owner }) => owner)).toEqual([
      "agent-dev-1",
      "agent-review-1",
    ]);
    expect(reviews[0]?.events.map(({ id }) => id)).toEqual([
      `${BASE_MS + 20_000}-0`,
    ]);
    expect(reviews[1]?.events.map(({ id }) => id)).toEqual([
      `${BASE_MS + 20_400}-0`,
    ]);

    const attachedIds = spans.flatMap((span) => [
      ...span.events.map(({ id }) => id),
      ...span.halts.flatMap((halt) => halt.events.map(({ id }) => id)),
    ]);
    expect(attachedIds).toHaveLength(source.length);
    expect(new Set(attachedIds)).toEqual(new Set(source.map(({ id }) => id)));
  });

  it.each(["issue.defer", "issue.deferred"])(
    "preserves the owner through %s and undefer",
    (deferAction) => {
      const spans = foldJourney(
        [
          event(0, "issue.create"),
          event(5, "issue.claim", { actor: "agent-dev-1" }),
          event(10, deferAction, { actor: "scheduler" }),
          event(20, "issue.undefer", { actor: "scheduler" }),
        ],
        BASE_MS + 30_000,
      );

      expect(spans.map(({ stage, owner }) => ({ stage, owner }))).toEqual([
        { stage: "Open", owner: null },
        { stage: "In progress", owner: "agent-dev-1" },
        { stage: "Deferred", owner: "agent-dev-1" },
        { stage: "Open", owner: "agent-dev-1" },
      ]);
    },
  );

  it("returns an empty list for an empty event window", () => {
    expect(foldJourney([], BASE_MS)).toEqual([]);
  });
});
