/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for issuesAreEqual helper and kzdf4 flicker fixes.
 */

import { describe, it, expect } from "vitest";

import { issuesAreEqual } from "../useIssues";
import type { Issue } from "../../types/issue";

function createIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    priority: 2,
    status: "open",
    assignee: "agent-1",
    issue_type: "task",
    created_at: "2025-01-23T10:00:00Z",
    updated_at: "2025-01-23T10:00:00Z",
    labels: ["bug", "frontend"],
    ...overrides,
  };
}

describe("issuesAreEqual", () => {
  it("returns true for identical issues", () => {
    const a = createIssue();
    const b = createIssue();
    expect(issuesAreEqual(a, b)).toBe(true);
  });

  it("returns true for same issue object (referential equality)", () => {
    const a = createIssue();
    expect(issuesAreEqual(a, a)).toBe(true);
  });

  it("returns false when id differs", () => {
    const a = createIssue({ id: "issue-1" });
    const b = createIssue({ id: "issue-2" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when title differs", () => {
    const a = createIssue({ title: "Title A" });
    const b = createIssue({ title: "Title B" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when status differs", () => {
    const a = createIssue({ status: "open" });
    const b = createIssue({ status: "closed" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when priority differs", () => {
    const a = createIssue({ priority: 1 });
    const b = createIssue({ priority: 2 });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when assignee differs", () => {
    const a = createIssue({ assignee: "agent-1" });
    const b = createIssue({ assignee: "agent-2" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when updated_at differs", () => {
    const a = createIssue({ updated_at: "2025-01-23T10:00:00Z" });
    const b = createIssue({ updated_at: "2025-01-23T11:00:00Z" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when issue_type differs", () => {
    const a = createIssue({ issue_type: "task" });
    const b = createIssue({ issue_type: "bug" });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when labels differ in length", () => {
    const a = createIssue({ labels: ["bug"] });
    const b = createIssue({ labels: ["bug", "frontend"] });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("returns false when labels differ in content", () => {
    const a = createIssue({ labels: ["bug", "frontend"] });
    const b = createIssue({ labels: ["bug", "backend"] });
    expect(issuesAreEqual(a, b)).toBe(false);
  });

  it("handles undefined labels on both sides", () => {
    const a = createIssue({ labels: undefined });
    const b = createIssue({ labels: undefined });
    expect(issuesAreEqual(a, b)).toBe(true);
  });

  it("handles undefined vs empty labels", () => {
    const a = createIssue({ labels: undefined });
    const b = createIssue({ labels: [] });
    expect(issuesAreEqual(a, b)).toBe(true);
  });

  it("ignores non-display fields like description", () => {
    const a = createIssue({ description: "Description A" });
    const b = createIssue({ description: "Description B" });
    // Description is not compared — updated_at is the catch-all
    // If both have same updated_at, they're considered equal
    expect(issuesAreEqual(a, b)).toBe(true);
  });
});
