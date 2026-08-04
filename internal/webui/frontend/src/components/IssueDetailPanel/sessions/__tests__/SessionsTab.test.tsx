/**
 * @vitest-environment jsdom
 */

/**
 * Unit tests for SessionsTab component.
 */

import { render, screen } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import "@testing-library/jest-dom";

import type { SessionRecord } from "@/types/agent";

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
          key={s.session_id}
          data-testid={`timeline-row-${s.session_id}`}
          data-selected={selectedId === s.session_id}
          onClick={() => onSelect(s.session_id)}
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
      Detail for {session.session_id} in task {taskId}
    </div>
  ),
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

// Mock the hook
vi.mock("@/hooks/terminal", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/terminal")>(
      "@/hooks/terminal",
    );
  return { ...actual, useTaskSessions: vi.fn() };
});

import { useTaskSessions } from "@/hooks/terminal";

const mockUseTaskSessions = vi.mocked(useTaskSessions);

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
        screen.getByText("Failed to load runs: API unavailable"),
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
        screen.getByText("No agent runs recorded yet"),
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
      expect(screen.getByTestId("session-timeline-mock")).toBeInTheDocument();
      expect(
        screen.getByText("Select a run to view details"),
      ).toBeInTheDocument();
    });

    it("shows detail view when a session is selected", async () => {
      const sessions = [
        createSession({ session_id: "s1", agent_name: "nova" }),
        createSession({
          session_id: "s2",
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
        sessions: [createSession({ session_id: "s1" })],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      // No session selected
      expect(
        screen.getByText("Select a run to view details"),
      ).toBeInTheDocument();
    });

    it("shows a failed count when any run failed", () => {
      mockUseTaskSessions.mockReturnValue({
        sessions: [
          createSession({ status: "failed", error_class: "AuthFailure" }),
        ],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      });
      render(<SessionsTab taskId="task-1" />);
      expect(screen.getByText("1 failed")).toBeInTheDocument();
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
