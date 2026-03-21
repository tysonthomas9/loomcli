/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionsTab component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord } from "@/types/session";

import { SessionsTab } from "../SessionsTab";

// Mock child components
vi.mock("../SessionTimeline", () => ({
  SessionTimeline: ({
    sessions,
    selectedId,
    onSelect,
  }: {
    sessions: SessionRecord[];
    selectedId: string | null;
    onSelect: (id: string) => void;
  }) => (
    <div data-testid="session-timeline-mock">
      {sessions.map((s) => (
        <button
          key={s.id}
          data-testid={`timeline-row-${s.id}`}
          data-selected={selectedId === s.id}
          onClick={() => onSelect(s.id)}
        >
          {s.agent_name}
        </button>
      ))}
    </div>
  ),
}));

vi.mock("../SessionDetailView", () => ({
  SessionDetailView: ({
    taskId,
    session,
  }: {
    taskId: string;
    session: SessionRecord;
  }) => (
    <div data-testid="session-detail-view-mock">
      Detail for {session.id} in task {taskId}
    </div>
  ),
}));

// Mock the hook
vi.mock("@/hooks/useTaskSessions", () => ({
  useTaskSessions: vi.fn(),
}));

import { useTaskSessions } from "@/hooks/useTaskSessions";

const mockUseTaskSessions = vi.mocked(useTaskSessions);

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

describe("SessionsTab", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("loading state", () => {
    it("shows spinner when loading with no sessions", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [],
        isLoading: true,
        error: null,
        refetch: vi.fn(),
      });
      const { container } = render(<SessionsTab taskId="task-1" />);
      const spinner = container.querySelector('[class*="spinner"]');
      expect(spinner).toBeInTheDocument();
    });

    it("does not show spinner when sessions exist during loading", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [createSession()],
        isLoading: true,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(screen.getByTestId("sessions-tab")).toBeInTheDocument();
    });
  });

  describe("error state", () => {
    it("shows error message when error occurs with no sessions", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [],
        isLoading: false,
        error: new Error("API unavailable"),
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(
        screen.getByText("Failed to load sessions: API unavailable"),
      ).toBeInTheDocument();
    });

    it("shows sessions when error occurs but sessions exist", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [createSession()],
        isLoading: false,
        error: new Error("Refresh failed"),
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(screen.getByTestId("sessions-tab")).toBeInTheDocument();
    });
  });

  describe("empty state", () => {
    it("shows empty state when no sessions and not loading", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(screen.getByTestId("sessions-empty")).toBeInTheDocument();
      expect(
        screen.getByText("No sessions recorded yet"),
      ).toBeInTheDocument();
    });
  });

  describe("sessions loaded", () => {
    it("renders SessionTimeline and detail placeholder", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [createSession()],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(screen.getByTestId("sessions-tab")).toBeInTheDocument();
      expect(
        screen.getByTestId("session-timeline-mock"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Select a session to view details"),
      ).toBeInTheDocument();
    });

    it("shows detail view when a session is selected", async () => {
      const sessions = [
        createSession({ id: "s1", agent_name: "nova" }),
        createSession({
          id: "s2",
          agent_name: "falcon",
          started_at: "2026-01-19T10:00:00Z",
        }),
      ];
      mockUseTaskSessions.mockReturnValue({
        sessions,
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);

      // Click a session in the timeline mock
      const { fireEvent } = await import("@testing-library/react");
      fireEvent.click(screen.getByTestId("timeline-row-s1"));

      expect(
        screen.getByTestId("session-detail-view-mock"),
      ).toBeInTheDocument();
      expect(
        screen.getByText("Detail for s1 in task task-1"),
      ).toBeInTheDocument();
    });

    it("shows placeholder when selected session id does not match any session", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [createSession({ id: "s1" })],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      // No session selected
      expect(
        screen.getByText("Select a session to view details"),
      ).toBeInTheDocument();
    });
  });

  describe("hook invocation", () => {
    it("passes taskId to useTaskSessions", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [],
        isLoading: true,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-42" />);
      expect(mockUseTaskSessions).toHaveBeenCalledWith("task-42");
    });
  });
});
