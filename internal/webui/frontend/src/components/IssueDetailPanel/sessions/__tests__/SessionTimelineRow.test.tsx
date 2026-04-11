/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionTimelineRow component.
 */

import { render, screen, fireEvent } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord } from "@/types/agent";

import { SessionTimelineRow } from "../SessionTimelineRow";

function createSession(overrides: Partial<SessionRecord> = {}): SessionRecord {
  return {
    session_id: "sess-1",
    task_id: "task-1",
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

describe("SessionTimelineRow", () => {
  const defaultProps = {
    session: createSession(),
    isSelected: false,
    onClick: vi.fn(),
  };

  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("rendering", () => {
    it("renders agent name", () => {
      render(<SessionTimelineRow {...defaultProps} />);
      expect(screen.getByText("nova")).toBeInTheDocument();
    });

    it("renders with correct test id", () => {
      render(<SessionTimelineRow {...defaultProps} />);
      expect(screen.getByTestId("session-row-sess-1")).toBeInTheDocument();
    });

    it("renders status dot with data-status attribute", () => {
      render(<SessionTimelineRow {...defaultProps} />);
      const statusDot = screen.getByLabelText("completed");
      expect(statusDot).toHaveAttribute("data-status", "completed");
    });

    it("renders phase badge when phase is present", () => {
      const session = createSession({ phase: "planning" });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("planning")).toBeInTheDocument();
    });

    it("does not render phase badge when phase is absent", () => {
      const session = createSession({ phase: undefined });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.queryByText("planning")).not.toBeInTheDocument();
      expect(screen.queryByText("implementation")).not.toBeInTheDocument();
    });

    it("renders aria-label with agent name and status", () => {
      render(<SessionTimelineRow {...defaultProps} />);
      expect(
        screen.getByLabelText("Session by nova, completed"),
      ).toBeInTheDocument();
    });
  });

  describe("duration formatting", () => {
    it("formats seconds only", () => {
      const session = createSession({ duration_s: 45 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("45s")).toBeInTheDocument();
    });

    it("formats minutes and seconds", () => {
      const session = createSession({ duration_s: 125 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("2m 5s")).toBeInTheDocument();
    });

    it('shows "--" for undefined duration', () => {
      const session = createSession({ duration_s: undefined });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("--")).toBeInTheDocument();
    });

    it('shows "--" for zero duration', () => {
      const session = createSession({ duration_s: 0 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("--")).toBeInTheDocument();
    });

    it('shows "--" for negative duration', () => {
      const session = createSession({ duration_s: -5 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("--")).toBeInTheDocument();
    });
  });

  describe("token formatting", () => {
    it("renders small token count as plain number", () => {
      const session = createSession({
        input_tokens: 200,
        output_tokens: 300,
      });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("500 tok")).toBeInTheDocument();
    });

    it("formats token count with K suffix for >= 1000", () => {
      const session = createSession({
        input_tokens: 800,
        output_tokens: 700,
      });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("1.5K tok")).toBeInTheDocument();
    });

    it("formats token count with K suffix for >= 10000", () => {
      const session = createSession({
        input_tokens: 8000,
        output_tokens: 7000,
      });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("15.0K tok")).toBeInTheDocument();
    });
  });

  describe("cost formatting", () => {
    it("renders cost as USD", () => {
      const session = createSession({ estimated_cost_usd: 1.5 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("$1.50")).toBeInTheDocument();
    });

    it("renders zero cost", () => {
      const session = createSession({ estimated_cost_usd: 0 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("$0.00")).toBeInTheDocument();
    });

    it("renders very small cost as <$0.01", () => {
      const session = createSession({ estimated_cost_usd: 0.005 });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByText("<$0.01")).toBeInTheDocument();
    });
  });

  describe("selection state", () => {
    it("does not apply selected class when not selected", () => {
      render(<SessionTimelineRow {...defaultProps} isSelected={false} />);
      const row = screen.getByTestId("session-row-sess-1");
      expect(row.className).not.toContain("selected");
    });

    it("applies selected class when selected", () => {
      render(<SessionTimelineRow {...defaultProps} isSelected={true} />);
      const row = screen.getByTestId("session-row-sess-1");
      expect(row.className).toContain("selected");
    });
  });

  describe("click interactions", () => {
    it("calls onClick when clicked", () => {
      const onClick = vi.fn();
      render(<SessionTimelineRow {...defaultProps} onClick={onClick} />);
      fireEvent.click(screen.getByTestId("session-row-sess-1"));
      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it("calls onClick on Enter key", () => {
      const onClick = vi.fn();
      render(<SessionTimelineRow {...defaultProps} onClick={onClick} />);
      fireEvent.keyDown(screen.getByTestId("session-row-sess-1"), {
        key: "Enter",
      });
      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it("calls onClick on Space key", () => {
      const onClick = vi.fn();
      render(<SessionTimelineRow {...defaultProps} onClick={onClick} />);
      fireEvent.keyDown(screen.getByTestId("session-row-sess-1"), {
        key: " ",
      });
      expect(onClick).toHaveBeenCalledTimes(1);
    });

    it("prevents default on Space key to avoid scrolling", () => {
      const onClick = vi.fn();
      render(<SessionTimelineRow {...defaultProps} onClick={onClick} />);
      const event = new KeyboardEvent("keydown", {
        key: " ",
        bubbles: true,
      });
      const preventDefaultSpy = vi.spyOn(event, "preventDefault");
      screen.getByTestId("session-row-sess-1").dispatchEvent(event);
      expect(preventDefaultSpy).toHaveBeenCalled();
    });

    it("does not call onClick for other keys", () => {
      const onClick = vi.fn();
      render(<SessionTimelineRow {...defaultProps} onClick={onClick} />);
      fireEvent.keyDown(screen.getByTestId("session-row-sess-1"), {
        key: "Tab",
      });
      expect(onClick).not.toHaveBeenCalled();
    });
  });

  describe("accessibility", () => {
    it('has role="button"', () => {
      render(<SessionTimelineRow {...defaultProps} />);
      expect(screen.getByRole("button")).toBeInTheDocument();
    });

    it("has tabIndex=0 for keyboard focus", () => {
      render(<SessionTimelineRow {...defaultProps} />);
      expect(screen.getByTestId("session-row-sess-1")).toHaveAttribute(
        "tabindex",
        "0",
      );
    });
  });

  describe("different session statuses", () => {
    it("renders running status", () => {
      const session = createSession({ status: "running" });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByLabelText("running")).toBeInTheDocument();
    });

    it("renders failed status", () => {
      const session = createSession({ status: "failed" });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByLabelText("failed")).toBeInTheDocument();
    });

    it("renders aborted status", () => {
      const session = createSession({ status: "aborted" });
      render(<SessionTimelineRow {...defaultProps} session={session} />);
      expect(screen.getByLabelText("aborted")).toBeInTheDocument();
    });
  });
});
