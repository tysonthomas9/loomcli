/**
 * Unit tests for IssueDetailPanel utility functions.
 */

import { describe, it, expect } from "vitest";

import type { Issue, IssueDetails } from "@/types";

import { formatDate, formatIssueType, isIssueDetails } from "../utils";

describe("formatIssueType", () => {
  it("returns 'Task' when type is undefined", () => {
    expect(formatIssueType(undefined)).toBe("Task");
  });

  it("capitalizes known types", () => {
    expect(formatIssueType("epic")).toBe("Epic");
    expect(formatIssueType("task")).toBe("Task");
    expect(formatIssueType("bug")).toBe("Bug");
    expect(formatIssueType("feature")).toBe("Feature");
  });

  it("returns unknown types unchanged", () => {
    // Cast because IssueType is a union of the known values; we're asserting
    // the fall-through branch still preserves whatever string came in.
    expect(formatIssueType("chore" as never)).toBe("chore");
  });
});

describe("formatDate", () => {
  it("formats an ISO date string as 'Mon D, YYYY'", () => {
    // Use a fixed UTC date; the formatter uses the local timezone, but the
    // "Mon D, YYYY" shape is stable regardless of timezone for a mid-day UTC
    // timestamp well inside the day boundary everywhere on Earth.
    expect(formatDate("2026-03-15T12:00:00Z")).toBe("Mar 15, 2026");
  });

  it("handles a date near year boundaries without drifting", () => {
    expect(formatDate("2025-06-01T12:00:00Z")).toBe("Jun 1, 2025");
  });
});

describe("isIssueDetails", () => {
  it("returns true when issue has a comments array", () => {
    const issue = {
      id: "t-1",
      title: "Test",
      status: "open",
      priority: 2,
      comments: [],
    } as unknown as IssueDetails;
    expect(isIssueDetails(issue)).toBe(true);
  });

  it("returns true when issue has a dependencies array", () => {
    const issue = {
      id: "t-1",
      title: "Test",
      status: "open",
      priority: 2,
      dependencies: [],
    } as unknown as IssueDetails;
    expect(isIssueDetails(issue)).toBe(true);
  });

  it("returns true when issue has a dependents array", () => {
    const issue = {
      id: "t-1",
      title: "Test",
      status: "open",
      priority: 2,
      dependents: [],
    } as unknown as IssueDetails;
    expect(isIssueDetails(issue)).toBe(true);
  });

  it("returns false for a plain Issue without IssueDetails fields", () => {
    const issue = {
      id: "t-1",
      title: "Test",
      status: "open",
      priority: 2,
    } as unknown as Issue;
    expect(isIssueDetails(issue)).toBe(false);
  });
});
