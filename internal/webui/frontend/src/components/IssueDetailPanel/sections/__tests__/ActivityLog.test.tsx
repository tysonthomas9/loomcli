/**
 * @vitest-environment jsdom
 */

import { render, screen, within } from "@testing-library/react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Comment } from "@/types";

import { ActivityLog } from "../ActivityLog";

function comment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 1,
    issue_id: "test-issue",
    author: "Test Author",
    text: "Test comment text",
    created_at: "2026-01-20T10:00:00Z",
    ...overrides,
  };
}

describe("ActivityLog", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-27T12:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders the comments-only empty state", () => {
    render(<ActivityLog comments={[]} issueId="test-issue" />);

    expect(screen.getByTestId("activity-log")).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3 })).toHaveTextContent(
      "Comments (0)",
    );
    expect(screen.getByTestId("activity-empty")).toHaveTextContent(
      "No comments yet.",
    );
    expect(screen.queryByTestId("activity-event")).not.toBeInTheDocument();
  });

  it("counts and renders comments with the existing avatar and markdown markup", () => {
    render(
      <ActivityLog
        issueId="test-issue"
        comments={[
          comment({
            id: 1,
            author: "Alice",
            text: "First **comment**",
          }),
          comment({
            id: 2,
            author: "Bob",
            text: "Second comment",
          }),
        ]}
      />,
    );

    expect(screen.getByRole("heading")).toHaveTextContent("Comments (2)");
    const comments = screen.getAllByTestId("activity-comment");
    expect(comments).toHaveLength(2);
    expect(within(comments[0]!).getByTestId("author-avatar")).toHaveAttribute(
      "title",
      "Alice",
    );
    expect(within(comments[0]!).getByText("comment").tagName).toBe("STRONG");
    expect(screen.queryByTestId("activity-event")).not.toBeInTheDocument();
  });

  it("keeps unknown-author and timestamp behavior", () => {
    render(
      <ActivityLog
        issueId="test-issue"
        comments={[comment({ author: "", created_at: "2026-01-27T10:00:00Z" })]}
      />,
    );

    expect(screen.getAllByText("Unknown").length).toBeGreaterThanOrEqual(1);
    const timestamp = screen.getByRole("time");
    expect(timestamp).toHaveAttribute("datetime", "2026-01-27T10:00:00Z");
    expect(timestamp).toHaveTextContent("2h ago");
  });
});
