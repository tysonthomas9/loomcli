import { describe, expect, it } from "vitest";

import type { AuditEvent } from "@/types/activity";
import {
  activityTimelineReducer,
  describeActivityEvent,
  filterActivityEvents,
  mergeActivityEvents,
  selectActivityEvents,
  toAuditEvent,
} from "../activityTimeline";

function event(overrides: Partial<AuditEvent> = {}): AuditEvent {
  return {
    cursor: "100-0",
    timestamp: "2026-08-14T18:00:00Z",
    actor: "api-architect-1",
    action: "issue.claim",
    entity_type: "issue",
    entity_id: "TEAMBACKEND-1",
    details: {},
    ...overrides,
  };
}

describe("describeActivityEvent", () => {
  it.each([
    ["issue.create", "api-architect-1 created TEAMBACKEND-1"],
    ["issue.update", "api-architect-1 updated TEAMBACKEND-1"],
    ["issue.claim", "api-architect-1 claimed TEAMBACKEND-1"],
    ["issue.release", "api-architect-1 released TEAMBACKEND-1"],
    ["issue.assign", "api-architect-1 assigned TEAMBACKEND-1"],
    ["issue.close", "api-architect-1 closed TEAMBACKEND-1"],
    ["issue.reopen", "api-architect-1 reopened TEAMBACKEND-1"],
    ["label.add", "api-architect-1 label architect added to TEAMBACKEND-1"],
    [
      "label.remove",
      "api-architect-1 label architect removed from TEAMBACKEND-1",
    ],
    ["comment.add", "api-architect-1 commented on TEAMBACKEND-1"],
    ["agent.create", "api-architect-1 created agent api-architect-1"],
    ["agent.update", "api-architect-1 updated agent api-architect-1"],
    ["workspace.create", "api-architect-1 created workspace platform"],
  ])("maps %s to a human-readable phrase", (action, expected) => {
    const overrides: Partial<AuditEvent> = { action };
    if (action.startsWith("label.")) overrides.details = { label: "architect" };
    if (action.startsWith("agent.")) {
      overrides.entity_type = "agent";
      overrides.entity_id = "api-architect-1";
    }
    if (action === "workspace.create") {
      overrides.entity_type = "workspace";
      overrides.entity_id = "platform";
    }

    expect(describeActivityEvent(event(overrides)).text).toBe(expected);
  });

  it("prefers a status-change phrase when details include old and new status", () => {
    expect(
      describeActivityEvent(
        event({
          action: "issue.update",
          details: { old_status: "in_progress", new_status: "review" },
        }),
      ).text,
    ).toBe("api-architect-1 moved TEAMBACKEND-1 to review");
  });

  it("renders unknown actions as action on entity", () => {
    expect(describeActivityEvent(event({ action: "design.save" })).text).toBe(
      "api-architect-1 design.save on TEAMBACKEND-1",
    );
  });
});

describe("activity event identity", () => {
  it("deduplicates history and live events by cursor", () => {
    const history = event({ action: "issue.claim" });
    const live = event({ action: "issue.update" });

    expect(mergeActivityEvents([history], [live])).toEqual([history]);
  });

  it("falls back to action, entity, and timestamp when a cursor is absent", () => {
    const history = event({ cursor: "100-0" });
    const live = event({ cursor: "" });

    expect(mergeActivityEvents([history], [live])).toEqual([history]);
  });

  it("keeps distinct cursor-backed events even when fallback fields match", () => {
    const first = event({ cursor: "100-0" });
    const second = event({ cursor: "101-0" });

    expect(mergeActivityEvents([first], [second])).toHaveLength(2);
  });
});

describe("filterActivityEvents", () => {
  const events = [
    event({ cursor: "100-0", actor: "alice", entity_id: "TEAM-1" }),
    event({ cursor: "101-0", actor: "bob", entity_id: "TEAM-2" }),
    event({ cursor: "102-0", actor: "alice", entity_id: "TEAM-2" }),
  ];

  it("filters by actor", () => {
    expect(filterActivityEvents(events, { actor: "alice" })).toHaveLength(2);
  });

  it("filters by issue entity", () => {
    expect(filterActivityEvents(events, { entity: "TEAM-2" })).toHaveLength(2);
  });

  it("combines actor and entity filters", () => {
    expect(
      filterActivityEvents(events, { actor: "alice", entity: "TEAM-2" }),
    ).toEqual([events[2]]);
  });
});

describe("activityTimelineReducer", () => {
  it("prepends a live event and exposes newest events first", () => {
    const state = {
      history: [event({ cursor: "100-0", timestamp: "2026-08-14T17:00:00Z" })],
      live: [],
      nextCursor: "",
    };
    const live = event({ cursor: "101-0", action: "issue.close" });

    const next = activityTimelineReducer(state, { type: "live", event: live });

    expect(next.live).toEqual([live]);
    expect(selectActivityEvents(next, {})).toEqual([live, state.history[0]]);
  });

  it("does not append a live event already present in history", () => {
    const history = event();
    const state = { history: [history], live: [], nextCursor: "" };

    expect(
      activityTimelineReducer(state, {
        type: "live",
        event: event({ action: "issue.update" }),
      }),
    ).toBe(state);
  });

  it("adds older history pages without duplicating events", () => {
    const current = event({ cursor: "102-0" });
    const older = event({
      cursor: "100-0",
      timestamp: "2026-08-14T16:00:00Z",
    });
    const state = { history: [current], live: [], nextCursor: "100-0" };

    const next = activityTimelineReducer(state, {
      type: "history",
      events: [older, current],
      nextCursor: "",
      append: true,
    });

    expect(next.history).toEqual([current, older]);
  });
});

describe("toAuditEvent", () => {
  it("maps the locked mutation fields into an audit event", () => {
    expect(
      toAuditEvent({
        type: "status",
        cursor: "200-0",
        action: "issue.update",
        entity_type: "issue",
        entity_id: "TEAMBACKEND-1",
        actor: "api-architect-1",
        timestamp: "2026-08-14T18:01:00Z",
        old_status: "in_progress",
        new_status: "review",
      }),
    ).toEqual(
      event({
        cursor: "200-0",
        timestamp: "2026-08-14T18:01:00Z",
        action: "issue.update",
        details: { old_status: "in_progress", new_status: "review" },
      }),
    );
  });

  it("ignores non-audit mutation payloads", () => {
    expect(
      toAuditEvent({ type: "refresh", timestamp: "2026-08-14T18:01:00Z" }),
    ).toBeNull();
  });
});
