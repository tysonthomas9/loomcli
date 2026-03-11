/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for consolidated issue categorization predicates.
 */

import { describe, it, expect } from "vitest";

import {
  NEEDS_REVISION_LABEL,
  hasNeedsRevision,
  getOpenStatus,
  isPRUrl,
  getReviewType,
} from "../issueCategory";

// --- hasNeedsRevision ---

describe("hasNeedsRevision", () => {
  it("returns true when labels include needs-revision", () => {
    expect(hasNeedsRevision({ labels: ["needs-revision"] })).toBe(true);
  });

  it("returns true when needs-revision is among other labels", () => {
    expect(
      hasNeedsRevision({ labels: ["bug", "needs-revision", "urgent"] }),
    ).toBe(true);
  });

  it("returns false when labels are empty", () => {
    expect(hasNeedsRevision({ labels: [] })).toBe(false);
  });

  it("returns false when labels are undefined", () => {
    expect(hasNeedsRevision({ labels: undefined })).toBe(false);
  });

  it("returns false when no labels field", () => {
    expect(hasNeedsRevision({})).toBe(false);
  });

  it("returns false when labels contain only other values", () => {
    expect(hasNeedsRevision({ labels: ["bug", "feature"] })).toBe(false);
  });
});

// --- isPRUrl ---

describe("isPRUrl", () => {
  it("returns true for GitHub PR URL with /pull/", () => {
    expect(isPRUrl("https://github.com/owner/repo/pull/42")).toBe(true);
  });

  it("returns true for URL with /pulls/", () => {
    expect(isPRUrl("https://github.com/owner/repo/pulls/123")).toBe(true);
  });

  it("returns false for non-PR URL", () => {
    expect(isPRUrl("https://github.com/owner/repo/issues/5")).toBe(false);
  });

  it("returns false for JIRA reference", () => {
    expect(isPRUrl("JIRA-123")).toBe(false);
  });

  it("returns false for null", () => {
    expect(isPRUrl(null)).toBe(false);
  });

  it("returns false for undefined", () => {
    expect(isPRUrl(undefined)).toBe(false);
  });

  it("returns false for empty string", () => {
    expect(isPRUrl("")).toBe(false);
  });
});

// --- getOpenStatus ---

describe("getOpenStatus", () => {
  describe("ready status", () => {
    it('returns "ready" when design has content and no needs-revision', () => {
      expect(getOpenStatus({ design: "some plan text" })).toBe("ready");
    });

    it('returns "ready" when design has content and empty labels', () => {
      expect(getOpenStatus({ design: "plan", labels: [] })).toBe("ready");
    });

    it('returns "ready" when design has content and other labels', () => {
      expect(getOpenStatus({ design: "plan", labels: ["bug"] })).toBe("ready");
    });
  });

  describe("needs_plan status", () => {
    it('returns "needs_plan" when design is empty string', () => {
      expect(getOpenStatus({ design: "" })).toBe("needs_plan");
    });

    it('returns "needs_plan" when design is undefined', () => {
      expect(getOpenStatus({ design: undefined })).toBe("needs_plan");
    });

    it('returns "needs_plan" when no design field is present', () => {
      expect(getOpenStatus({})).toBe("needs_plan");
    });

    it('returns "needs_plan" when design exists but has needs-revision label (bug fix)', () => {
      expect(
        getOpenStatus({ design: "plan text", labels: ["needs-revision"] }),
      ).toBe("needs_plan");
    });

    it('returns "needs_plan" when design exists and needs-revision among other labels', () => {
      expect(
        getOpenStatus({ design: "plan", labels: ["bug", "needs-revision"] }),
      ).toBe("needs_plan");
    });
  });
});

// --- getReviewType ---

describe("getReviewType", () => {
  describe("plan review", () => {
    it('returns "plan" when status is review with no external_ref', () => {
      expect(
        getReviewType({ title: "Design auth flow", status: "review" }),
      ).toBe("plan");
    });

    it('returns "plan" when status is review with non-PR external_ref', () => {
      expect(
        getReviewType({
          title: "Task",
          status: "review",
          external_ref: "JIRA-123",
        }),
      ).toBe("plan");
    });

    it('returns "plan" when status is review with null external_ref', () => {
      expect(
        getReviewType({ title: "Task", status: "review", external_ref: null }),
      ).toBe("plan");
    });

    it('returns "plan" when status is review with empty string external_ref', () => {
      expect(
        getReviewType({ title: "Task", status: "review", external_ref: "" }),
      ).toBe("plan");
    });
  });

  describe("code review", () => {
    it('returns "code" when status is review with PR URL in external_ref', () => {
      expect(
        getReviewType({
          title: "Implement feature X",
          status: "review",
          external_ref: "https://github.com/owner/repo/pull/42",
        }),
      ).toBe("code");
    });

    it('returns "code" when external_ref contains /pulls/ path', () => {
      expect(
        getReviewType({
          title: "Task",
          status: "review",
          external_ref: "https://github.com/owner/repo/pulls/123",
        }),
      ).toBe("code");
    });
  });

  describe("help review", () => {
    it('returns "help" when status is "blocked" with notes', () => {
      expect(
        getReviewType({
          title: "Task needing help",
          status: "blocked",
          notes: "Stuck on database migration",
        }),
      ).toBe("help");
    });

    it('returns null when status is "blocked" without notes', () => {
      expect(
        getReviewType({ title: "Blocked task", status: "blocked" }),
      ).toBeNull();
    });

    it('returns null when status is "blocked" with empty string notes', () => {
      expect(
        getReviewType({ title: "Blocked task", status: "blocked", notes: "" }),
      ).toBeNull();
    });
  });

  describe("no review type", () => {
    it("returns null for regular issues", () => {
      expect(
        getReviewType({ title: "Regular task", status: "open" }),
      ).toBeNull();
    });

    it("returns null for in_progress status", () => {
      expect(
        getReviewType({ title: "Working on it", status: "in_progress" }),
      ).toBeNull();
    });

    it("returns null for closed status", () => {
      expect(
        getReviewType({ title: "Done task", status: "closed" }),
      ).toBeNull();
    });

    it("returns null when no status is provided", () => {
      expect(getReviewType({ title: "No status task" })).toBeNull();
    });
  });

  describe("priority rules", () => {
    it("code takes priority when external_ref has PR URL even with notes", () => {
      expect(
        getReviewType({
          title: "Task",
          status: "review",
          notes: "Some notes",
          external_ref: "https://github.com/owner/repo/pull/1",
        }),
      ).toBe("code");
    });

    it("plan review when status is review without PR URL regardless of title", () => {
      expect(
        getReviewType({
          title: "[Need Review] Code review request",
          status: "review",
        }),
      ).toBe("plan");
    });
  });

  describe("edge cases", () => {
    it("handles undefined title gracefully", () => {
      // @ts-expect-error Testing undefined title
      expect(getReviewType({ title: undefined })).toBeNull();
    });

    it('returns "plan" for review status even with [Need Review] in title', () => {
      expect(getReviewType({ title: "[Need Review]", status: "review" })).toBe(
        "plan",
      );
    });

    it("does not detect review from title alone (no status)", () => {
      expect(
        getReviewType({ title: "[Need Review] My feature plan" }),
      ).toBeNull();
    });
  });
});

// --- Constants ---

describe("constants", () => {
  it("NEEDS_REVISION_LABEL matches expected value", () => {
    expect(NEEDS_REVISION_LABEL).toBe("needs-revision");
  });
});
