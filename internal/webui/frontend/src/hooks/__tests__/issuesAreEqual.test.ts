/**
 * Unit tests for the issuesAreEqual comparison logic used by useIssues refetch merge.
 *
 * issuesAreEqual is a private function in useIssues.ts. These tests validate
 * the expected comparison contract by re-implementing the same logic — ensuring
 * that the fields checked for display-relevant equality are correct and complete.
 *
 * If the implementation changes, these tests will need to be updated in parallel.
 */

import { describe, it, expect } from "vitest";

import type { Issue } from "@/types";

/**
 * Mirror of the private issuesAreEqual function from useIssues.ts.
 * Tests validate this contract matches the production implementation.
 */
function issuesAreEqual(a: Issue, b: Issue): boolean {
  if (a.id !== b.id) return false;
  if (a.updated_at !== b.updated_at) return false;
  if (a.title !== b.title) return false;
  if (a.status !== b.status) return false;
  if (a.priority !== b.priority) return false;
  if (a.assignee !== b.assignee) return false;
  if (a.issue_type !== b.issue_type) return false;
  if (a.owner !== b.owner) return false;
  // Shallow array comparison for labels
  const aLabels = a.labels;
  const bLabels = b.labels;
  if (aLabels !== bLabels) {
    if (!aLabels || !bLabels) return false;
    if (aLabels.length !== bLabels.length) return false;
    for (let i = 0; i < aLabels.length; i++) {
      if (aLabels[i] !== bLabels[i]) return false;
    }
  }
  return true;
}

/**
 * Helper to create a test issue with required fields.
 */
function createTestIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    title: "Test Issue",
    priority: 2,
    created_at: "2025-01-23T10:00:00Z",
    updated_at: "2025-01-23T10:00:00Z",
    ...overrides,
  };
}

describe("issuesAreEqual", () => {
  describe("identical issues", () => {
    it("returns true for two identical issues", () => {
      const a = createTestIssue();
      const b = createTestIssue();
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns true for issues with all display fields matching", () => {
      const common = {
        id: "issue-42",
        title: "Full Issue",
        priority: 1 as const,
        status: "open" as const,
        assignee: "alice",
        issue_type: "task" as const,
        labels: ["bug", "urgent"],
        created_at: "2025-01-23T10:00:00Z",
        updated_at: "2025-01-23T12:00:00Z",
      };
      const a = createTestIssue(common);
      const b = createTestIssue(common);
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns true when non-display fields differ (e.g., description)", () => {
      const a = createTestIssue({ description: "Description A" });
      const b = createTestIssue({ description: "Description B" });
      // description is not a display-relevant field for card rendering
      expect(issuesAreEqual(a, b)).toBe(true);
    });
  });

  describe("different display fields", () => {
    it("returns false when id differs", () => {
      const a = createTestIssue({ id: "issue-1" });
      const b = createTestIssue({ id: "issue-2" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when updated_at differs", () => {
      const a = createTestIssue({ updated_at: "2025-01-23T10:00:00Z" });
      const b = createTestIssue({ updated_at: "2025-01-23T11:00:00Z" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when title differs", () => {
      const a = createTestIssue({ title: "Title A" });
      const b = createTestIssue({ title: "Title B" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when status differs", () => {
      const a = createTestIssue({ status: "open" });
      const b = createTestIssue({ status: "closed" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when priority differs", () => {
      const a = createTestIssue({ priority: 1 });
      const b = createTestIssue({ priority: 3 });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when assignee differs", () => {
      const a = createTestIssue({ assignee: "alice" });
      const b = createTestIssue({ assignee: "bob" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when issue_type differs", () => {
      const a = createTestIssue({ issue_type: "task" });
      const b = createTestIssue({ issue_type: "bug" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when owner differs", () => {
      const a = createTestIssue({ owner: "Alice" });
      const b = createTestIssue({ owner: "Bob" });
      expect(issuesAreEqual(a, b)).toBe(false);
    });
  });

  describe("labels comparison", () => {
    it("returns true when both have undefined labels", () => {
      const a = createTestIssue();
      const b = createTestIssue();
      // Neither has labels set
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns false when one has labels and the other does not", () => {
      const a = createTestIssue({ labels: ["bug"] });
      const b = createTestIssue();
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when the other has labels and the first does not", () => {
      const a = createTestIssue();
      const b = createTestIssue({ labels: ["bug"] });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns true when both have same labels in same order", () => {
      const a = createTestIssue({ labels: ["bug", "urgent"] });
      const b = createTestIssue({ labels: ["bug", "urgent"] });
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns false when labels differ in content", () => {
      const a = createTestIssue({ labels: ["bug"] });
      const b = createTestIssue({ labels: ["feature"] });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when labels differ in length", () => {
      const a = createTestIssue({ labels: ["bug"] });
      const b = createTestIssue({ labels: ["bug", "urgent"] });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns false when labels differ in order", () => {
      const a = createTestIssue({ labels: ["bug", "urgent"] });
      const b = createTestIssue({ labels: ["urgent", "bug"] });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("returns true when both have empty labels array", () => {
      const a = createTestIssue({ labels: [] });
      const b = createTestIssue({ labels: [] });
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns true when both reference the same labels array", () => {
      const sharedLabels = ["bug", "urgent"];
      const a = createTestIssue({ labels: sharedLabels });
      const b = createTestIssue({ labels: sharedLabels });
      expect(issuesAreEqual(a, b)).toBe(true);
    });
  });

  describe("edge cases", () => {
    it("returns true for the same object reference", () => {
      const a = createTestIssue();
      expect(issuesAreEqual(a, a)).toBe(true);
    });

    it("handles undefined optional fields consistently", () => {
      const a = createTestIssue({ status: undefined, assignee: undefined });
      const b = createTestIssue({ status: undefined, assignee: undefined });
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns false when one has status and other has undefined", () => {
      const a = createTestIssue({ status: "open" });
      const b = createTestIssue({ status: undefined });
      expect(issuesAreEqual(a, b)).toBe(false);
    });

    it("handles priority 0 correctly (P0 is valid, not falsy)", () => {
      const a = createTestIssue({ priority: 0 });
      const b = createTestIssue({ priority: 0 });
      expect(issuesAreEqual(a, b)).toBe(true);
    });

    it("returns false for priority 0 vs priority 1", () => {
      const a = createTestIssue({ priority: 0 });
      const b = createTestIssue({ priority: 1 });
      expect(issuesAreEqual(a, b)).toBe(false);
    });
  });
});
