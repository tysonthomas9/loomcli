/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for ActivityLog component.
 */

import { render, screen, within } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import type { Comment, Event, EventType } from "@/types";

import { ActivityLog } from "../ActivityLog";

/**
 * Create a test comment with default values.
 */
function createTestComment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 1,
    issue_id: "test-issue",
    author: "Test Author",
    text: "Test comment text",
    created_at: "2026-01-20T10:00:00Z",
    ...overrides,
  };
}

/**
 * Create a test event with default values.
 */
function createTestEvent(overrides: Partial<Event> = {}): Event {
  return {
    id: 1,
    issue_id: "test-issue",
    event_type: "issue.created" as EventType,
    actor: "alice",
    created_at: "2026-01-20T10:00:00Z",
    ...overrides,
  };
}

describe("ActivityLog", () => {
  // Mock Date.now for consistent relative time formatting
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-01-27T12:00:00Z"));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  describe("rendering", () => {
    it('renders section with "Activity" title', () => {
      render(<ActivityLog comments={[]} events={[]} issueId="test-issue" />);
      expect(screen.getByTestId("activity-log")).toBeInTheDocument();
      expect(
        screen.getByRole("heading", { name: /activity/i }),
      ).toBeInTheDocument();
    });

    it("shows item count in title when items exist", () => {
      const comments = [createTestComment({ id: 1 })];
      const events = [
        createTestEvent({ id: 1, event_type: "issue.status_changed" }),
      ];
      render(
        <ActivityLog
          comments={comments}
          events={events}
          issueId="test-issue"
        />,
      );
      expect(screen.getByRole("heading")).toHaveTextContent("Activity (2)");
    });

    it("renders comment entries with data-testid", () => {
      const comments = [
        createTestComment({ id: 1, author: "Alice" }),
        createTestComment({ id: 2, author: "Bob" }),
      ];
      render(
        <ActivityLog comments={comments} events={[]} issueId="test-issue" />,
      );
      const items = screen.getAllByTestId("activity-comment");
      expect(items).toHaveLength(2);
    });

    it("renders event entries with data-testid", () => {
      const events = [
        createTestEvent({ id: 1, event_type: "issue.status_changed" }),
        createTestEvent({ id: 2, event_type: "issue.label_added" }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      const items = screen.getAllByTestId("activity-event");
      expect(items).toHaveLength(2);
    });
  });

  describe("mixed comments and events", () => {
    it("renders both comments and events together", () => {
      const comments = [createTestComment({ id: 1 })];
      const events = [
        createTestEvent({ id: 1, event_type: "issue.status_changed" }),
      ];
      render(
        <ActivityLog
          comments={comments}
          events={events}
          issueId="test-issue"
        />,
      );
      expect(screen.getAllByTestId("activity-comment")).toHaveLength(1);
      expect(screen.getAllByTestId("activity-event")).toHaveLength(1);
    });

    it("filters out 'commented' events to avoid duplicates", () => {
      const comments = [createTestComment({ id: 1 })];
      const events = [
        createTestEvent({ id: 1, event_type: "issue.commented" }),
        createTestEvent({ id: 2, event_type: "issue.status_changed" }),
      ];
      render(
        <ActivityLog
          comments={comments}
          events={events}
          issueId="test-issue"
        />,
      );
      // Only the status_changed event should render (commented is filtered out)
      expect(screen.getAllByTestId("activity-event")).toHaveLength(1);
      // Total count: 1 comment + 1 event (commented is filtered)
      expect(screen.getByRole("heading")).toHaveTextContent("Activity (2)");
    });
  });

  describe("chronological ordering", () => {
    it("orders items oldest first", () => {
      const comments = [
        createTestComment({
          id: 2,
          author: "Middle",
          created_at: "2026-01-26T10:00:00Z",
        }),
      ];
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.created",
          actor: "First",
          created_at: "2026-01-25T10:00:00Z",
        }),
        createTestEvent({
          id: 3,
          event_type: "issue.closed",
          actor: "Last",
          created_at: "2026-01-27T10:00:00Z",
        }),
      ];
      render(
        <ActivityLog
          comments={comments}
          events={events}
          issueId="test-issue"
        />,
      );

      // Gather all activity items in DOM order
      const allItems = screen.getAllByTestId(/^activity-(comment|event)$/);
      expect(allItems).toHaveLength(3);

      // First item: event "created" by "First" (oldest)
      expect(
        within(allItems[0]).getByText(/First created this issue/),
      ).toBeInTheDocument();
      // Second item: comment by "Middle"
      expect(within(allItems[1]).getByText("Middle")).toBeInTheDocument();
      // Third item: event "closed" by "Last" (newest)
      expect(
        within(allItems[2]).getByText(/Last closed this issue/),
      ).toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty message when no comments and no events", () => {
      render(<ActivityLog comments={[]} events={[]} issueId="test-issue" />);
      expect(screen.getByTestId("activity-empty")).toBeInTheDocument();
      expect(screen.getByText("No activity yet.")).toBeInTheDocument();
    });

    it("does not show empty message when comments exist", () => {
      render(
        <ActivityLog
          comments={[createTestComment()]}
          events={[]}
          issueId="test-issue"
        />,
      );
      expect(screen.queryByTestId("activity-empty")).not.toBeInTheDocument();
    });

    it("does not show empty message when events exist", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[createTestEvent({ event_type: "issue.created" })]}
          issueId="test-issue"
        />,
      );
      expect(screen.queryByTestId("activity-empty")).not.toBeInTheDocument();
    });
  });

  describe("event description text", () => {
    it("describes 'created' events", () => {
      const events = [
        createTestEvent({ id: 1, event_type: "issue.created", actor: "alice" }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("alice created this issue")).toBeInTheDocument();
    });

    it("describes 'status_changed' events with old/new values", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.status_changed",
          actor: "bob",
          old_value: "open",
          new_value: "in_progress",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(
        screen.getByText("bob changed status from open to in_progress"),
      ).toBeInTheDocument();
    });

    it("describes 'status_changed' events without values", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.status_changed",
          actor: "bob",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("bob changed the status")).toBeInTheDocument();
    });

    it("describes 'closed' events", () => {
      const events = [
        createTestEvent({ id: 1, event_type: "issue.closed", actor: "carol" }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("carol closed this issue")).toBeInTheDocument();
    });

    it("describes 'reopened' events", () => {
      const events = [
        createTestEvent({ id: 1, event_type: "issue.reopened", actor: "dave" }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("dave reopened this issue")).toBeInTheDocument();
    });

    it("describes 'label_added' events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.label_added",
          actor: "eve",
          new_value: "bug",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("eve added label bug")).toBeInTheDocument();
    });

    it("describes 'label_removed' events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.label_removed",
          actor: "eve",
          old_value: "wontfix",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("eve removed label wontfix")).toBeInTheDocument();
    });

    it("describes 'dependency_added' events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.dependency_added",
          actor: "frank",
          new_value: "issue-42",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(
        screen.getByText("frank added dependency issue-42"),
      ).toBeInTheDocument();
    });

    it("describes 'dependency_removed' events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.dependency_removed",
          actor: "frank",
          old_value: "issue-42",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(
        screen.getByText("frank removed dependency issue-42"),
      ).toBeInTheDocument();
    });

    it("describes 'compacted' events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.compacted",
          actor: "system",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(
        screen.getByText("Earlier activity was summarized"),
      ).toBeInTheDocument();
    });

    it("describes 'updated' events with values", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.updated",
          actor: "grace",
          old_value: "low",
          new_value: "high",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("grace updated low to high")).toBeInTheDocument();
    });

    it("describes 'updated' events without values", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.updated",
          actor: "grace",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("grace updated this issue")).toBeInTheDocument();
    });

    it.each([
      ["issue.created", {}, "alice created this issue"],
      ["issue.status_changed", {}, "alice changed the status"],
      ["issue.claimed", {}, "alice claimed this issue"],
      ["issue.released", {}, "alice released this issue"],
      [
        "issue.deferred",
        { new_value: "2026-05-01T00:00:00Z" },
        "alice deferred this issue until 2026-05-01T00:00:00Z",
      ],
      ["issue.undeferred", {}, "alice un-deferred this issue"],
      ["issue.closed", {}, "alice closed this issue"],
      [
        "issue.closed",
        { comment: "shipped after review" },
        "alice closed this issue: shipped after review",
      ],
      ["issue.reopened", {}, "alice reopened this issue"],
      [
        "issue.assigned",
        { new_value: "worker-1" },
        "alice assigned this issue to worker-1",
      ],
      ["issue.deleted", {}, "alice deleted this issue"],
      ["issue.updated", {}, "alice updated this issue"],
      [
        "issue.dependency_added",
        { new_value: "issue-42" },
        "alice added dependency issue-42",
      ],
      [
        "issue.dependency_removed",
        { old_value: "issue-42" },
        "alice removed dependency issue-42",
      ],
      ["issue.label_added", { new_value: "bug" }, "alice added label bug"],
      [
        "issue.label_removed",
        { old_value: "wontfix" },
        "alice removed label wontfix",
      ],
      ["issue.compacted", {}, "Earlier activity was summarized"],
    ])(
      "renders specific text for %s events",
      (eventType, overrides, expectedText) => {
        render(
          <ActivityLog
            comments={[]}
            events={[
              createTestEvent({
                event_type: eventType as EventType,
                actor: "alice",
                ...(overrides as Partial<Event>),
              }),
            ]}
            issueId="test-issue"
          />,
        );

        expect(screen.getByText(expectedText)).toBeInTheDocument();
        expect(
          screen.queryByText("alice performed an action"),
        ).not.toBeInTheDocument();
      },
    );

    it("describes deferred events without a date using fallback text", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[
            createTestEvent({
              event_type: "issue.deferred",
              actor: "alice",
            }),
          ]}
          issueId="test-issue"
        />,
      );

      expect(screen.getByText("alice deferred this issue")).toBeInTheDocument();
    });

    it("describes single-field updates with the field name", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[
            createTestEvent({
              event_type: "issue.updated",
              actor: "alice",
              field: "priority",
              fields: ["priority"],
              field_count: 1,
              old_value: "P2",
              new_value: "P0",
            }),
          ]}
          issueId="test-issue"
        />,
      );

      expect(
        screen.getByText("alice updated priority from P2 to P0"),
      ).toBeInTheDocument();
    });

    it("humanises known update field names", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[
            createTestEvent({
              event_type: "issue.updated",
              actor: "alice",
              field: "issue_type",
              fields: ["issue_type"],
              field_count: 1,
              old_value: "task",
              new_value: "bug",
            }),
          ]}
          issueId="test-issue"
        />,
      );

      expect(
        screen.getByText("alice updated type from task to bug"),
      ).toBeInTheDocument();
    });

    it("lists multi-field update names without before or after values", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[
            createTestEvent({
              event_type: "issue.updated",
              actor: "alice",
              fields: ["priority", "owner"],
              field_count: 2,
              old_value: "P2",
              new_value: "P0",
            }),
          ]}
          issueId="test-issue"
        />,
      );

      expect(
        screen.getByText("alice updated priority, owner"),
      ).toBeInTheDocument();
      expect(screen.queryByText(/from P2 to P0/)).not.toBeInTheDocument();
    });

    it("summarizes multi-field updates by count when names are unavailable", () => {
      render(
        <ActivityLog
          comments={[]}
          events={[
            createTestEvent({
              event_type: "issue.updated",
              actor: "alice",
              field_count: 3,
            }),
          ]}
          issueId="test-issue"
        />,
      );

      expect(screen.getByText("alice updated 3 fields")).toBeInTheDocument();
    });

    it("uses 'Someone' when actor is empty", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.created",
          actor: "",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(
        screen.getByText("Someone created this issue"),
      ).toBeInTheDocument();
    });

    it("describes unknown event types with fallback", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "some_future_type" as EventType,
          actor: "hal",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      expect(screen.getByText("hal performed an action")).toBeInTheDocument();
    });
  });

  describe("comment content", () => {
    it("shows author name in comment entries", () => {
      const comments = [createTestComment({ author: "Jane Doe" })];
      render(
        <ActivityLog comments={comments} events={[]} issueId="test-issue" />,
      );
      expect(screen.getByText("Jane Doe")).toBeInTheDocument();
    });

    it('shows "Unknown" when comment author is empty', () => {
      const comments = [createTestComment({ author: "" })];
      render(
        <ActivityLog comments={comments} events={[]} issueId="test-issue" />,
      );
      // Both avatar and author line use "Unknown"
      expect(screen.getAllByText("Unknown").length).toBeGreaterThanOrEqual(1);
    });

    it("shows formatted timestamp on comment entries", () => {
      const comments = [
        createTestComment({ created_at: "2026-01-27T10:00:00Z" }),
      ];
      render(
        <ActivityLog comments={comments} events={[]} issueId="test-issue" />,
      );
      // Should show relative time "2h ago"
      expect(screen.getByText("2h ago")).toBeInTheDocument();
    });

    it("uses time element with datetime attribute on comments", () => {
      const comments = [
        createTestComment({ created_at: "2026-01-20T10:00:00Z" }),
      ];
      render(
        <ActivityLog comments={comments} events={[]} issueId="test-issue" />,
      );
      const timeElements = screen.getAllByRole("time");
      expect(timeElements.length).toBeGreaterThanOrEqual(1);
      expect(timeElements[0]).toHaveAttribute(
        "datetime",
        "2026-01-20T10:00:00Z",
      );
    });

    it("uses time element with datetime attribute on events", () => {
      const events = [
        createTestEvent({
          id: 1,
          event_type: "issue.created",
          created_at: "2026-01-20T10:00:00Z",
        }),
      ];
      render(
        <ActivityLog comments={[]} events={events} issueId="test-issue" />,
      );
      const timeElements = screen.getAllByRole("time");
      expect(timeElements.length).toBeGreaterThanOrEqual(1);
      expect(timeElements[0]).toHaveAttribute(
        "datetime",
        "2026-01-20T10:00:00Z",
      );
    });
  });

  describe("accessibility", () => {
    it("uses heading for section title", () => {
      render(<ActivityLog comments={[]} events={[]} issueId="test-issue" />);
      expect(screen.getByRole("heading", { level: 3 })).toBeInTheDocument();
    });
  });
});
