/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionTimeline component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord } from "@/types/session";

import { SessionTimeline } from "../SessionTimeline";

function createSession(overrides: Partial<SessionRecord> = {}): SessionRecord {
  return {
    id: "sess-1",
    agent_name: "nova",
    backend: "claude",
    model: "opus-4",
    status: "completed",
    started_at: "2026-01-20T10:00:00Z",
    ended_at: "2026-01-20T10:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0.15,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

describe("SessionTimeline", () => {
  const defaultProps = {
    sessions: [] as SessionRecord[],
    selectedId: null as string | null,
    onSelect: vi.fn(),
    isLoading: false,
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("loading state", () => {
    it("shows skeleton rows when loading with no sessions", () => {
      render(
        <SessionTimeline {...defaultProps} isLoading={true} sessions={[]} />,
      );
      expect(screen.queryByTestId("session-timeline")).not.toBeInTheDocument();
      // Skeleton rows should be present (3 divs)
      const container = document.querySelector('[class*="timeline"]');
      expect(container).toBeInTheDocument();
    });

    it("shows sessions instead of skeleton when sessions exist during loading", () => {
      const sessions = [createSession()];
      render(
        <SessionTimeline
          {...defaultProps}
          isLoading={true}
          sessions={sessions}
        />,
      );
      expect(screen.getByTestId("session-timeline")).toBeInTheDocument();
      expect(screen.getByText("nova")).toBeInTheDocument();
    });
  });

  describe("rendering sessions", () => {
    it("renders a row for each session", () => {
      const sessions = [
        createSession({ id: "s1", agent_name: "nova" }),
        createSession({ id: "s2", agent_name: "falcon" }),
        createSession({ id: "s3", agent_name: "eagle" }),
      ];
      render(<SessionTimeline {...defaultProps} sessions={sessions} />);
      expect(screen.getByTestId("session-row-s1")).toBeInTheDocument();
      expect(screen.getByTestId("session-row-s2")).toBeInTheDocument();
      expect(screen.getByTestId("session-row-s3")).toBeInTheDocument();
    });

    it("renders the timeline container with correct testid", () => {
      const sessions = [createSession()];
      render(<SessionTimeline {...defaultProps} sessions={sessions} />);
      expect(screen.getByTestId("session-timeline")).toBeInTheDocument();
    });
  });

  describe("sorting", () => {
    it("sorts sessions newest first by started_at", () => {
      const sessions = [
        createSession({
          id: "oldest",
          agent_name: "oldest-agent",
          started_at: "2026-01-18T10:00:00Z",
        }),
        createSession({
          id: "newest",
          agent_name: "newest-agent",
          started_at: "2026-01-20T10:00:00Z",
        }),
        createSession({
          id: "middle",
          agent_name: "middle-agent",
          started_at: "2026-01-19T10:00:00Z",
        }),
      ];
      render(<SessionTimeline {...defaultProps} sessions={sessions} />);
      const rows = screen.getAllByRole("button");
      // Newest first
      expect(rows[0]).toHaveAttribute("data-testid", "session-row-newest");
      expect(rows[1]).toHaveAttribute("data-testid", "session-row-middle");
      expect(rows[2]).toHaveAttribute("data-testid", "session-row-oldest");
    });

    it("does not mutate the original sessions array", () => {
      const sessions = [
        createSession({ id: "b", started_at: "2026-01-18T10:00:00Z" }),
        createSession({ id: "a", started_at: "2026-01-20T10:00:00Z" }),
      ];
      const originalOrder = sessions.map((s) => s.id);

      render(<SessionTimeline {...defaultProps} sessions={sessions} />);

      // Original array should be unchanged
      expect(sessions.map((s) => s.id)).toEqual(originalOrder);
    });
  });

  describe("selection", () => {
    it("marks the selected session row", () => {
      const sessions = [
        createSession({ id: "s1" }),
        createSession({ id: "s2", started_at: "2026-01-19T10:00:00Z" }),
      ];
      render(
        <SessionTimeline
          {...defaultProps}
          sessions={sessions}
          selectedId="s1"
        />,
      );
      const selectedRow = screen.getByTestId("session-row-s1");
      expect(selectedRow.className).toContain("selected");

      const unselectedRow = screen.getByTestId("session-row-s2");
      expect(unselectedRow.className).not.toContain("selected");
    });

    it("calls onSelect with session id when a row is clicked", () => {
      const onSelect = vi.fn();
      const sessions = [createSession({ id: "s1" })];
      render(
        <SessionTimeline
          {...defaultProps}
          sessions={sessions}
          onSelect={onSelect}
        />,
      );
      fireEvent.click(screen.getByTestId("session-row-s1"));
      expect(onSelect).toHaveBeenCalledWith("s1");
    });

    it("no row is selected when selectedId is null", () => {
      const sessions = [
        createSession({ id: "s1" }),
        createSession({ id: "s2", started_at: "2026-01-19T10:00:00Z" }),
      ];
      render(
        <SessionTimeline
          {...defaultProps}
          sessions={sessions}
          selectedId={null}
        />,
      );
      const rows = screen.getAllByRole("button");
      for (const row of rows) {
        expect(row.className).not.toContain("selected");
      }
    });
  });

  describe("empty state", () => {
    it("renders empty timeline when no sessions and not loading", () => {
      render(
        <SessionTimeline {...defaultProps} sessions={[]} isLoading={false} />,
      );
      const timeline = screen.getByTestId("session-timeline");
      expect(timeline).toBeInTheDocument();
      // No rows should be present
      expect(screen.queryByRole("button")).not.toBeInTheDocument();
    });
  });
});
