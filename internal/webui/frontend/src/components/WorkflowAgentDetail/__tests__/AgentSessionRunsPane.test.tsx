// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { AgentHistorySession } from "@/api/agents";
import type { SessionRecord } from "@/types/agent";

import { AgentSessionRunsPane } from "../AgentSessionRunsPane";

const mocks = vi.hoisted(() => ({
  useAgentHistory: vi.fn(),
  useTaskSessions: vi.fn(),
}));

vi.mock("@/hooks/agents", () => ({
  useAgentHistory: mocks.useAgentHistory,
}));

vi.mock("@/hooks/terminal", () => ({
  useTaskSessions: mocks.useTaskSessions,
}));

vi.mock("@/components/SessionRunDetail", () => ({
  SessionRunDetail: ({
    taskId,
    agentId,
    session,
    retryTranscriptUnavailable,
    exitCodeKnown,
    telemetryKnown,
  }: {
    taskId: string;
    agentId?: string;
    session: SessionRecord;
    retryTranscriptUnavailable?: boolean;
    exitCodeKnown?: boolean;
    telemetryKnown?: boolean;
  }) => (
    <div
      data-testid="session-run-detail"
      data-retry={String(retryTranscriptUnavailable === true)}
      data-status={session.status}
      data-error={session.last_error ?? ""}
      data-agent-id={agentId ?? ""}
      data-exit-known={String(exitCodeKnown !== false)}
      data-telemetry-known={String(telemetryKnown !== false)}
      data-patch-back-status={session.patch_back_status ?? ""}
      data-head-sha={session.head_sha ?? ""}
    >
      {taskId}:{session.session_id}
    </div>
  ),
}));

function historySession(
  sessionId: string,
  taskId?: string,
  overrides: Partial<AgentHistorySession> = {},
): AgentHistorySession {
  return {
    workspace_key: "WS",
    session_id: sessionId,
    agent_id: "coder",
    kind: taskId ? "task" : "terminal",
    ...(taskId ? { task_id: taskId } : {}),
    status: "completed",
    started_at: "2026-07-23T00:00:00Z",
    finished_at: "2026-07-23T00:00:05Z",
    created_at: "2026-07-23T00:00:00Z",
    updated_at: "2026-07-23T00:00:05Z",
    ...overrides,
  };
}

function canonicalSession(taskId: string, sessionId: string): SessionRecord {
  return {
    session_id: sessionId,
    task_id: taskId,
    agent_name: "coder",
    backend: "codex",
    started_at: "2026-07-23T00:00:00Z",
    status: "completed",
    exit_code: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    attempt_num: 1,
    is_active: false,
    has_transcript: true,
    has_diff: false,
  };
}

describe("AgentSessionRunsPane", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useTaskSessions.mockReturnValue({
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it("explains when a supervised agent has not run yet", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("supervised-agent-no-runs")).toHaveTextContent(
      "No sessions yet",
    );
  });

  it("uses canonical task evidence and switches between session rows", () => {
    const first = historySession("session-1", "TASK-1");
    const second = historySession("session-2", "TASK-2");
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [first, second],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.useTaskSessions.mockImplementation((taskId: string | null) => {
      const sessionId =
        taskId === "TASK-1"
          ? "session-1"
          : taskId === "TASK-2"
            ? "session-2"
            : null;
      return {
        sessions:
          taskId && sessionId ? [canonicalSession(taskId, sessionId)] : [],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      };
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-1:session-1",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-retry",
      "false",
    );
    expect(screen.getAllByRole("link", { name: "TASK-1" })).toHaveLength(2);
    expect(screen.getAllByRole("link", { name: "TASK-1" })[0]).toHaveAttribute(
      "href",
      "/ws/WS/issues/TASK-1",
    );
    expect(screen.getByRole("link", { name: "TASK-2" })).toHaveAttribute(
      "href",
      "/ws/WS/issues/TASK-2",
    );
    fireEvent.click(screen.getByTestId("supervised-agent-session-session-2"));
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-2:session-2",
    );
  });

  it("opens an ordinary task-link click through the agent task pane callback", () => {
    const onOpenTask = vi.fn();
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [historySession("session-1", "TASK-1")],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <AgentSessionRunsPane
        workspaceId="WS"
        agentName="coder"
        onOpenTask={onOpenTask}
      />,
    );

    const taskLinks = screen.getAllByRole("link", { name: "TASK-1" });
    expect(taskLinks).toHaveLength(2);
    expect(fireEvent.click(taskLinks[0]!)).toBe(false);
    expect(onOpenTask).toHaveBeenCalledWith("TASK-1");
    expect(taskLinks[0]).toHaveAttribute("href", "/ws/WS/issues/TASK-1");

    onOpenTask.mockClear();
    taskLinks[0]!.addEventListener("click", (event) => event.preventDefault(), {
      once: true,
    });
    fireEvent.click(taskLinks[0]!, { ctrlKey: true });
    expect(onOpenTask).not.toHaveBeenCalled();
  });

  it("uses a normalized task identity for history links and their callback", () => {
    const onOpenTask = vi.fn();
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [historySession("session-1", " TASK-1 ")],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <AgentSessionRunsPane
        workspaceId="WS"
        agentName="coder"
        onOpenTask={onOpenTask}
      />,
    );

    const taskLinks = screen.getAllByRole("link", { name: "TASK-1" });
    expect(taskLinks).toHaveLength(2);
    for (const taskLink of taskLinks) {
      expect(taskLink).toHaveAttribute("href", "/ws/WS/issues/TASK-1");
    }
    fireEvent.click(taskLinks[0]!);
    expect(onOpenTask).toHaveBeenCalledWith("TASK-1");
  });

  it("retries projected transcript evidence when only the durable session exists", () => {
    const only = historySession("session-1", "TASK-1");
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [only],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-retry",
      "true",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-exit-known",
      "false",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-telemetry-known",
      "false",
    );
  });

  it("keeps transcript detail mounted when task-session metadata fails", () => {
    const only = historySession("session-1", "TASK-1");
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [only],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.useTaskSessions.mockReturnValue({
      sessions: [],
      isLoading: false,
      error: new Error("FleetDB unavailable"),
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(
      screen.getByTestId("supervised-agent-session-metadata-warning"),
    ).toHaveTextContent(
      "Task-session metadata is temporarily unavailable. Showing transcript evidence from run history. FleetDB unavailable",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-1:session-1",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-retry",
      "true",
    );
  });

  it("starts fallback transcript detail while task-session metadata loads", () => {
    const only = historySession("session-1", "TASK-1");
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [only],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.useTaskSessions.mockReturnValue({
      sessions: [],
      isLoading: true,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(
      screen.getByTestId("supervised-agent-session-metadata-loading"),
    ).toHaveTextContent(
      "Loading task-session metadata. Showing transcript evidence from run history.",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-1:session-1",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-retry",
      "true",
    );
  });

  it("loads durable transcript evidence for a non-task interactive session", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [historySession("terminal-1")],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      ":terminal-1",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-agent-id",
      "coder",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-retry",
      "true",
    );
    expect(mocks.useTaskSessions).toHaveBeenCalledWith(null);
  });

  it("waits for a task reference instead of using the agent transcript route for a task session", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [
        historySession("task-session-without-task", undefined, {
          kind: "task",
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(
      screen.getByTestId("supervised-agent-task-reference-pending"),
    ).toHaveTextContent("Task reference is still loading");
    expect(screen.queryByTestId("session-run-detail")).not.toBeInTheDocument();
    expect(mocks.useTaskSessions).toHaveBeenCalledWith(null);
  });

  it("drops the prior selected session when switching to another agent", () => {
    mocks.useAgentHistory.mockImplementation(
      (_workspaceId: string, agentName: string) => ({
        runs: [],
        sessions:
          agentName === "agent-one"
            ? [historySession("one-session", "TASK-ONE")]
            : [
                {
                  ...historySession("two-session", "TASK-TWO"),
                  agent_id: "agent-two",
                },
              ],
        isLoading: false,
        error: null,
        refetch: vi.fn(),
      }),
    );

    const { rerender } = render(
      <AgentSessionRunsPane workspaceId="WS" agentName="agent-one" />,
    );
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-ONE:one-session",
    );

    rerender(<AgentSessionRunsPane workspaceId="WS" agentName="agent-two" />);

    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-TWO:two-session",
    );
  });

  it("keeps yielded sessions active and does not treat progress summaries as errors", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [
        historySession("session-1", "TASK-1", {
          status: "yielded",
          summary: "Waiting for operator input",
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-status",
      "running",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-error",
      "",
    );
    expect(
      screen.getAllByText("yielded")[0]?.previousElementSibling,
    ).toHaveAttribute("data-live", "true");
  });

  it("uses a failed session summary as error evidence", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [
        historySession("session-1", "TASK-1", {
          status: "failed",
          summary: "Codex exited unexpectedly",
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-error",
      "Codex exited unexpectedly",
    );
  });

  it("projects patch-back and GitHub head evidence into fallback sessions", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [
        historySession("session-patch", "TASK-PATCH", {
          metadata: {
            patch_back_status: "pushed",
            patch_back_head_sha: "patch-head-sha",
            github_head_sha: "older-github-head-sha",
          },
        }),
        historySession("session-github", "TASK-GITHUB", {
          metadata: {
            patch_back_status: "pull_request_opened",
            github_head_sha: "github-head-sha",
          },
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-patch-back-status",
      "pushed",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-head-sha",
      "patch-head-sha",
    );

    fireEvent.click(
      screen.getByTestId("supervised-agent-session-session-github"),
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-patch-back-status",
      "pull_request_opened",
    );
    expect(screen.getByTestId("session-run-detail")).toHaveAttribute(
      "data-head-sha",
      "github-head-sha",
    );
  });

  it("falls back to created_at when an older server returns a zero started_at", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [
        historySession("session-1", "TASK-1", {
          started_at: "0001-01-01T00:00:00Z",
        }),
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<AgentSessionRunsPane workspaceId="WS" agentName="coder" />);

    expect(screen.getByText("Started").nextElementSibling).toHaveTextContent(
      "2026",
    );
  });

  it("disables history and transcript reads while the Runs tab is hidden", () => {
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(
      <AgentSessionRunsPane
        workspaceId="WS"
        agentName="coder"
        active={false}
      />,
    );

    expect(mocks.useAgentHistory).toHaveBeenCalledWith("WS", "coder", false);
    expect(mocks.useTaskSessions).not.toHaveBeenCalled();
    expect(screen.queryByTestId("session-run-detail")).not.toBeInTheDocument();
  });
});
