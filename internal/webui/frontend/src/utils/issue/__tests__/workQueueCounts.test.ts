import { describe, expect, it } from "vitest";

import type { Issue } from "@/types";

import { getWorkQueueCounts } from "../workQueueCounts";

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: overrides.id ?? "issue",
    title: overrides.title ?? "Issue",
    priority: overrides.priority ?? 2,
    issue_type: overrides.issue_type ?? "task",
    created_at: overrides.created_at ?? "2026-05-19T00:00:00Z",
    updated_at: overrides.updated_at ?? "2026-05-19T00:00:00Z",
    ...overrides,
  } as Issue;
}

describe("getWorkQueueCounts", () => {
  it("uses canonical status before FleetDB ready metadata", () => {
    const counts = getWorkQueueCounts([
      issue({ id: "epic", issue_type: "epic", status: "open", is_ready: true }),
      issue({ id: "ready", status: "open", is_ready: true }),
      issue({ id: "active", status: "in_progress", is_ready: true }),
      issue({ id: "review", status: "review", is_ready: true }),
      issue({ id: "done", status: "closed", is_ready: true }),
    ]);

    expect(counts).toEqual({
      backlog: 0,
      open: 1,
      blocked: 0,
      inProgress: 1,
      needsReview: 1,
      done: 1,
    });
  });

  it("counts FleetDB blocked and deferred projections before open fallback", () => {
    const counts = getWorkQueueCounts([
      issue({ id: "blocked-status", status: "blocked" }),
      issue({ id: "blocked-projection", status: "open", is_blocked: true }),
      issue({ id: "deferred-status", status: "deferred" }),
      issue({ id: "deferred-projection", status: "open", is_deferred: true }),
      issue({ id: "plain-open", status: "open" }),
    ]);

    expect(counts).toEqual({
      backlog: 2,
      open: 1,
      blocked: 2,
      inProgress: 0,
      needsReview: 0,
      done: 0,
    });
  });
});
