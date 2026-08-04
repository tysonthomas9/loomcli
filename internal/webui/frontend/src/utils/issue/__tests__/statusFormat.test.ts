/**
 * Unit tests for statusFormat utility.
 */

import { describe, it, expect } from "vitest";

import { formatStatusLabel } from "../statusFormat";

describe("formatStatusLabel", () => {
  it("formats open correctly", () => {
    expect(formatStatusLabel("open")).toBe("Open");
  });

  it("formats in_progress correctly", () => {
    expect(formatStatusLabel("in_progress")).toBe("In Progress");
  });

  it("formats closed correctly", () => {
    expect(formatStatusLabel("closed")).toBe("Closed");
  });

  it("formats blocked correctly", () => {
    expect(formatStatusLabel("blocked")).toBe("Blocked");
  });

  it("formats deferred correctly", () => {
    expect(formatStatusLabel("deferred")).toBe("Deferred");
  });

  it("formats custom snake_case status", () => {
    expect(formatStatusLabel("custom_status")).toBe("Custom Status");
  });

  it("formats multi-word snake_case status", () => {
    expect(formatStatusLabel("some_long_custom_status")).toBe(
      "Some Long Custom Status",
    );
  });

  it("handles single character", () => {
    expect(formatStatusLabel("a")).toBe("A");
  });

  it("handles uppercase input by converting to title case", () => {
    expect(formatStatusLabel("OPEN")).toBe("Open");
    expect(formatStatusLabel("IN_PROGRESS")).toBe("In Progress");
  });
});
