/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionHistorySection component.
 */

import {
  render,
  screen,
  waitFor,
  fireEvent,
  act,
} from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import "@testing-library/jest-dom";

import { SessionHistorySection } from "../SessionHistorySection";

// Mock API calls
vi.mock("@/api/terminal", () => ({
  listSessionHistory: vi.fn(),
  getSessionScrollback: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

import { listSessionHistory, getSessionScrollback } from "@/api/terminal";

const mockListSessionHistory = vi.mocked(listSessionHistory);
const mockGetSessionScrollback = vi.mocked(getSessionScrollback);

function createRecord(overrides: Record<string, unknown> = {}) {
  return {
    id: "rec-1",
    session_name: "nova-sess-1",
    issue_id: "issue-1",
    backend: "claude",
    status: "completed" as const,
    launcher: "user" as const,
    started_at: "2026-01-20T10:00:00Z",
    ended_at: "2026-01-20T10:05:00Z",
    scrollback_evidence_status: "finalized" as const,
    ...overrides,
  };
}

describe("SessionHistorySection", () => {
  const realDateNow = Date.now;

  beforeEach(() => {
    vi.clearAllMocks();
    // Mock Date.now without using fake timers to avoid blocking promises
    Date.now = () => new Date("2026-01-20T12:00:00Z").getTime();
  });

  afterEach(() => {
    Date.now = realDateNow;
  });

  describe("loading state", () => {
    it("shows loading message initially", () => {
      mockListSessionHistory.mockReturnValue(new Promise(() => {}));
      render(<SessionHistorySection issueId="issue-1" />);
      expect(screen.getByText("Loading sessions...")).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("shows error message on fetch failure", async () => {
      mockListSessionHistory.mockRejectedValue(new Error("Network error"));
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("Network error")).toBeInTheDocument();
      });
    });

    it("shows fallback error for non-Error exceptions", async () => {
      mockListSessionHistory.mockRejectedValue("string error");
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(
          screen.getByText("Failed to load session history"),
        ).toBeInTheDocument();
      });
    });
  });

  describe("empty state", () => {
    it("shows empty message when no records", async () => {
      mockListSessionHistory.mockResolvedValue([]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(
          screen.getByText("No terminal sessions yet"),
        ).toBeInTheDocument();
      });
    });
  });

  describe("records rendering", () => {
    it("renders session records with backend", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ id: "r1", backend: "claude" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("claude")).toBeInTheDocument();
      });
    });

    it("renders relative time", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-20T10:00:00Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("2h ago")).toBeInTheDocument();
      });
    });

    it("renders duration", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          started_at: "2026-01-20T10:00:00Z",
          ended_at: "2026-01-20T10:05:00Z",
        }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("5m")).toBeInTheDocument();
      });
    });

    it("renders status indicator with data-status attribute", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "active" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        const indicator = document.querySelector('[data-status="active"]');
        expect(indicator).toBeInTheDocument();
      });
    });

    it("renders multiple records", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ id: "r1", backend: "claude" }),
        createRecord({ id: "r2", backend: "gemini" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("claude")).toBeInTheDocument();
        expect(screen.getByText("gemini")).toBeInTheDocument();
      });
    });
  });

  describe("Jump to tab button", () => {
    it('shows "Jump to tab" for active sessions with onJumpToSession', async () => {
      const onJump = vi.fn();
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "active", session_name: "nova-sess" }),
      ]);
      render(
        <SessionHistorySection issueId="issue-1" onJumpToSession={onJump} />,
      );
      await waitFor(() => {
        expect(screen.getByText("Jump to tab")).toBeInTheDocument();
      });
    });

    it("calls onJumpToSession with session name when clicked", async () => {
      const onJump = vi.fn();
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "active", session_name: "nova-sess" }),
      ]);
      render(
        <SessionHistorySection issueId="issue-1" onJumpToSession={onJump} />,
      );
      await waitFor(() => {
        expect(screen.getByText("Jump to tab")).toBeInTheDocument();
      });
      fireEvent.click(screen.getByText("Jump to tab"));
      expect(onJump).toHaveBeenCalledWith("nova-sess");
    });

    it('does not show "Jump to tab" for active session without onJumpToSession', async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "active" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        const indicator = document.querySelector('[data-status="active"]');
        expect(indicator).toBeInTheDocument();
      });
      expect(screen.queryByText("Jump to tab")).not.toBeInTheDocument();
    });

    it('does not show "Jump to tab" for completed sessions', async () => {
      const onJump = vi.fn();
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "completed" }),
      ]);
      render(
        <SessionHistorySection issueId="issue-1" onJumpToSession={onJump} />,
      );
      await waitFor(() => {
        const indicator = document.querySelector('[data-status="completed"]');
        expect(indicator).toBeInTheDocument();
      });
      expect(screen.queryByText("Jump to tab")).not.toBeInTheDocument();
    });
  });

  describe("View scrollback button", () => {
    it('shows "View scrollback" for completed sessions', async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "completed" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });
    });

    it("does not require a storage locator to offer durable scrollback", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "completed" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        const indicator = document.querySelector('[data-status="completed"]');
        expect(indicator).toBeInTheDocument();
      });
      expect(screen.getByText("View scrollback")).toBeInTheDocument();
    });

    it('does not show "View scrollback" for active sessions', async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ status: "active" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        const indicator = document.querySelector('[data-status="active"]');
        expect(indicator).toBeInTheDocument();
      });
      expect(screen.queryByText("View scrollback")).not.toBeInTheDocument();
    });

    it("shows capture failure without offering an unavailable scrollback", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          status: "completed",
          scrollback_evidence_status: "capture_failed",
          scrollback_failure_class: "capture_unavailable",
        }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(
          screen.getByText("Scrollback capture failed"),
        ).toBeInTheDocument();
      });
      expect(screen.queryByText("View scrollback")).not.toBeInTheDocument();
    });

    it("offers truncated durable scrollback and labels its provenance", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ scrollback_evidence_status: "truncated" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(
          screen.getByText("Scrollback ready (truncated)"),
        ).toBeInTheDocument();
      });
      expect(screen.getByText("View scrollback")).toBeInTheDocument();
    });
  });

  describe("scrollback overlay", () => {
    it("opens scrollback overlay on click and shows loading", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          id: "r1",
          status: "completed",
          session_name: "test-sess",
        }),
      ]);
      mockGetSessionScrollback.mockReturnValue(new Promise(() => {}));

      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });

      await act(async () => {
        fireEvent.click(screen.getByText("View scrollback"));
      });

      expect(screen.getByText("Scrollback: test-sess")).toBeInTheDocument();
      expect(screen.getByText("Loading...")).toBeInTheDocument();
    });

    it("shows scrollback content after loading", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          id: "r1",
          status: "completed",
          session_name: "test-sess",
        }),
      ]);
      mockGetSessionScrollback.mockResolvedValue({
        content: "Terminal output here",
        lines: 10,
      });

      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });

      await act(async () => {
        fireEvent.click(screen.getByText("View scrollback"));
      });

      await waitFor(() => {
        expect(screen.getByText("Terminal output here")).toBeInTheDocument();
      });
    });

    it("shows error message on scrollback fetch failure", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          id: "r1",
          status: "completed",
          session_name: "test-sess",
        }),
      ]);
      mockGetSessionScrollback.mockRejectedValue(new Error("Fetch failed"));

      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });

      await act(async () => {
        fireEvent.click(screen.getByText("View scrollback"));
      });

      await waitFor(() => {
        expect(
          screen.getByText("Failed to load scrollback content."),
        ).toBeInTheDocument();
      });
    });

    it("closes scrollback overlay on Close button click", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          id: "r1",
          status: "completed",
          session_name: "test-sess",
        }),
      ]);
      mockGetSessionScrollback.mockResolvedValue({
        content: "Content",
        lines: 1,
      });

      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });

      await act(async () => {
        fireEvent.click(screen.getByText("View scrollback"));
      });

      await waitFor(() => {
        expect(screen.getByText("Close")).toBeInTheDocument();
      });

      fireEvent.click(screen.getByText("Close"));
      expect(
        screen.queryByText("Scrollback: test-sess"),
      ).not.toBeInTheDocument();
    });

    it("shows 'No content' when scrollback content is empty", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          id: "r1",
          status: "completed",
          session_name: "test-sess",
        }),
      ]);
      mockGetSessionScrollback.mockResolvedValue({
        content: "",
        lines: 0,
      });

      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("View scrollback")).toBeInTheDocument();
      });

      await act(async () => {
        fireEvent.click(screen.getByText("View scrollback"));
      });

      await waitFor(() => {
        expect(screen.getByText("No content")).toBeInTheDocument();
      });
    });
  });

  describe("issueId changes", () => {
    it("refetches when issueId changes", async () => {
      mockListSessionHistory.mockResolvedValue([]);
      const { rerender } = render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(mockListSessionHistory).toHaveBeenCalledWith(
          "test-ws-id",
          "issue-1",
        );
      });

      mockListSessionHistory.mockResolvedValue([
        createRecord({ backend: "new-backend" }),
      ]);
      rerender(<SessionHistorySection issueId="issue-2" />);
      await waitFor(() => {
        expect(mockListSessionHistory).toHaveBeenCalledWith(
          "test-ws-id",
          "issue-2",
        );
      });
    });
  });

  describe("relative time formatting", () => {
    it("formats seconds ago", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-20T11:59:30Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("30s ago")).toBeInTheDocument();
      });
    });

    it("formats minutes ago", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-20T11:45:00Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("15m ago")).toBeInTheDocument();
      });
    });

    it("formats hours ago", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-20T09:00:00Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("3h ago")).toBeInTheDocument();
      });
    });

    it("formats days ago", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-17T12:00:00Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("3d ago")).toBeInTheDocument();
      });
    });

    it("formats future timestamps as 'just now'", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({ started_at: "2026-01-20T13:00:00Z" }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("just now")).toBeInTheDocument();
      });
    });
  });

  describe("duration formatting", () => {
    it("formats duration in seconds", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          started_at: "2026-01-20T11:59:30Z",
          ended_at: "2026-01-20T11:59:45Z",
        }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("15s")).toBeInTheDocument();
      });
    });

    it("formats duration in hours and minutes", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          started_at: "2026-01-20T09:00:00Z",
          ended_at: "2026-01-20T10:30:00Z",
        }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("1h 30m")).toBeInTheDocument();
      });
    });

    it("formats exact hours without remaining minutes", async () => {
      mockListSessionHistory.mockResolvedValue([
        createRecord({
          started_at: "2026-01-20T09:00:00Z",
          ended_at: "2026-01-20T11:00:00Z",
        }),
      ]);
      render(<SessionHistorySection issueId="issue-1" />);
      await waitFor(() => {
        expect(screen.getByText("2h")).toBeInTheDocument();
      });
    });
  });
});
