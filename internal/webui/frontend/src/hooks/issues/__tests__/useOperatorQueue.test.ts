/**
 * @vitest-environment jsdom
 */

import { renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import type { Issue } from "@/types";

import type { QueueChild, QueueChildIndex } from "../useOperatorQueue";
import { deriveOperatorQueue, useOperatorQueue } from "../useOperatorQueue";

function issue(overrides: Partial<Issue>): Issue {
  return {
    id: "TASK-1",
    title: "Queue task",
    priority: 2,
    issue_type: "task",
    status: "open",
    created_at: "2026-08-21T15:00:00.000Z",
    updated_at: "2026-08-21T15:00:00.000Z",
    ...overrides,
  } as Issue;
}

function children(entries: Record<string, QueueChild[]>): QueueChildIndex {
  return new Map(Object.entries(entries));
}

describe("deriveOperatorQueue", () => {
  it("derives all three current-state row types", () => {
    const result = deriveOperatorQueue([
      issue({
        id: "DESIGN-1",
        status: "review",
        has_design: true,
      }),
      issue({
        id: "BLOCK-1",
        status: "blocked",
        notes: "BLOCKED: missing credentials",
      }),
      issue({
        id: "REVISION-1",
        status: "open",
        labels: ["needs-revision"],
      }),
    ]);

    expect(result.map(({ issue: row, kind }) => [row.id, kind])).toEqual([
      ["BLOCK-1", "blocked"],
      ["DESIGN-1", "design-gate"],
      ["REVISION-1", "needs-revision"],
    ]);
  });

  it("ranks by queue age using oldest updated_at first", () => {
    const result = deriveOperatorQueue([
      issue({
        id: "NEWER",
        status: "blocked",
        notes: "BLOCKED: newer",
        updated_at: "2026-08-21T15:20:00.000Z",
      }),
      issue({
        id: "OLDEST",
        status: "open",
        labels: ["needs-revision"],
        updated_at: "2026-08-21T15:05:00.000Z",
      }),
      issue({
        id: "MIDDLE",
        status: "review",
        has_design: true,
        updated_at: "2026-08-21T15:10:00.000Z",
      }),
    ]);

    expect(result.map(({ issue: row }) => row.id)).toEqual([
      "OLDEST",
      "MIDDLE",
      "NEWER",
    ]);
  });

  it("excludes epics, code reviews, missing designs, note-less blocks, and dependency blocks", () => {
    const result = deriveOperatorQueue([
      issue({
        id: "EPIC",
        issue_type: "epic",
        status: "review",
        has_design: true,
      }),
      issue({
        id: "CODE-REVIEW",
        status: "review",
        has_design: true,
        external_ref: "https://github.com/acme/repo/pull/42",
      }),
      issue({ id: "NO-DESIGN", status: "review", has_design: false }),
      issue({ id: "NO-NOTE", status: "blocked", notes: "" }),
      issue({
        id: "DEPENDENCY-BLOCKED",
        status: "open",
        is_blocked: true,
        blocked_by_count: 1,
      }),
      issue({ id: "ORDINARY", status: "in_progress" }),
    ]);

    expect(result).toEqual([]);
  });

  it("recognizes collection has_design without requiring the design body", () => {
    const [item] = deriveOperatorQueue([
      issue({
        id: "DESIGN-1",
        status: "review",
        has_design: true,
        design: undefined,
      }),
    ]);

    expect(item?.kind).toBe("design-gate");
  });
});

describe("useOperatorQueue", () => {
  it("returns the shared pure derivation", () => {
    const issues = [
      issue({
        id: "BLOCK-1",
        status: "blocked",
        notes: "BLOCKED: waiting",
      }),
    ];

    const { result } = renderHook(() => useOperatorQueue(issues));

    expect(result.current).toEqual(deriveOperatorQueue(issues));
  });
});

describe("deriveOperatorQueue with a child index", () => {
  const parent = () =>
    issue({
      id: "PARENT",
      status: "blocked",
      // Deliberately prose a regex could match: the rule must not read notes.
      notes: "BLOCKED: split into A and B",
    });

  function ids(items: ReturnType<typeof deriveOperatorQueue>): string[] {
    return items.map(({ issue: row }) => row.id);
  }

  it("excludes a parked parent whose children are all open", () => {
    const result = deriveOperatorQueue(
      [parent()],
      children({
        PARENT: [
          { id: "A", status: "open" },
          { id: "B", status: "open" },
        ],
      }),
    );

    expect(result).toEqual([]);
  });

  it("excludes a parked parent with one closed and one open child", () => {
    const result = deriveOperatorQueue(
      [parent()],
      children({
        PARENT: [
          { id: "A", status: "closed" },
          { id: "B", status: "open" },
        ],
      }),
    );

    expect(result).toEqual([]);
  });

  it("counts a child that is neither open nor closed as not closed", () => {
    const result = deriveOperatorQueue(
      [parent()],
      children({
        PARENT: [
          { id: "A", status: "closed" },
          { id: "B", status: "closed" },
          { id: "C", status: "blocked" },
        ],
      }),
    );

    expect(result).toEqual([]);
  });

  it("counts a child with no status as not closed", () => {
    const result = deriveOperatorQueue(
      [parent()],
      children({ PARENT: [{ id: "A" }] }),
    );

    expect(result).toEqual([]);
  });

  it("keeps a parked parent whose children have all closed", () => {
    const result = deriveOperatorQueue(
      [parent()],
      children({
        PARENT: [
          { id: "A", status: "closed" },
          { id: "B", status: "closed" },
        ],
      }),
    );

    expect(result.map(({ issue: row, kind }) => [row.id, kind])).toEqual([
      ["PARENT", "blocked"],
    ]);
  });

  it("keeps a parked issue whose children are unknown", () => {
    expect(ids(deriveOperatorQueue([parent()], children({})))).toEqual([
      "PARENT",
    ]);
    expect(ids(deriveOperatorQueue([parent()]))).toEqual(["PARENT"]);
  });

  it("keeps a parked issue with an empty child list", () => {
    expect(
      ids(deriveOperatorQueue([parent()], children({ PARENT: [] }))),
    ).toEqual(["PARENT"]);
  });

  it("leaves design-gate and needs-revision rows untouched", () => {
    const result = deriveOperatorQueue(
      [
        issue({ id: "DESIGN-1", status: "review", has_design: true }),
        issue({ id: "REVISION-1", status: "open", labels: ["needs-revision"] }),
      ],
      children({
        "DESIGN-1": [{ id: "A", status: "open" }],
        "REVISION-1": [{ id: "B", status: "open" }],
      }),
    );

    expect(result.map(({ issue: row, kind }) => [row.id, kind])).toEqual([
      ["DESIGN-1", "design-gate"],
      ["REVISION-1", "needs-revision"],
    ]);
  });
});
