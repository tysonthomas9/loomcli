import { describe, expect, it } from "vitest";

import type { Event } from "@/types";

import { actorKindFor, describeEvent } from "../eventDescription";

function event(overrides: Partial<Event> = {}): Event {
  return {
    id: "1-0",
    issue_id: "loom-1",
    event_type: "issue.created",
    actor: "alice",
    created_at: "2026-08-20T12:00:00.000Z",
    ...overrides,
  };
}

describe("actorKindFor", () => {
  it.each([
    [" system ", "system"],
    ["agent-dev-1", "agent"],
    ["Claude reviewer", "agent"],
    ["Alice", "operator"],
    [null, "operator"],
  ] as const)("classifies %s as %s", (actor, expected) => {
    expect(actorKindFor(actor)).toBe(expected);
  });
});

describe("describeEvent", () => {
  it("prioritizes structured status changes over a generic summary", () => {
    expect(
      describeEvent(
        event({
          event_type: "issue.updated",
          summary: "Updated status and updated_at",
          changes: [
            { field: "status", before: "in_progress", after: "blocked" },
          ],
        }),
      ),
    ).toBe("alice changed status from In Progress to Blocked");
  });

  it("never includes updated_at in an update description", () => {
    const description = describeEvent(
      event({
        event_type: "issue.updated",
        summary: "Updated notes and updated_at",
        changes: [
          { field: "notes", after: "BLOCKED: toolchain unavailable" },
          { field: "updated_at", after: "2026-08-20T12:00:00.000Z" },
        ],
      }),
    );

    expect(description).toBe("Updated notes");
    expect(description).not.toContain("updated_at");

    expect(
      describeEvent(
        event({
          event_type: "issue.updated",
          summary: "Updated updated_at",
          changes: [{ field: "updated_at", after: "2026-08-20T12:00:00.000Z" }],
        }),
      ),
    ).not.toContain("updated_at");
  });

  it("preserves the system release explanation", () => {
    expect(
      describeEvent(
        event({
          event_type: "issue.release",
          actor: "system",
          summary: "Released claim",
        }),
      ),
    ).toBe(
      "System released the claim: no active lock or live agent session was vouching for it, so the task returned to the pool",
    );
  });

  it("preserves the empty-assignee phrasing", () => {
    expect(
      describeEvent(
        event({
          event_type: "issue.assign",
          actor: "dispatcher",
          summary: "Assigned issue",
          metadata: { assignee: "" },
        }),
      ),
    ).toBe("Unassigned issue");
  });

  it.each([
    ["issue.created", "alice created this issue"],
    ["issue.closed", "alice closed this issue"],
    ["issue.reopened", "alice reopened this issue"],
    ["issue.commented", "alice commented"],
    ["issue.compacted", "Earlier activity was summarized"],
    ["future.action", "alice performed an action"],
  ])("describes %s", (eventType, expected) => {
    expect(describeEvent(event({ event_type: eventType }))).toBe(expected);
  });
});
