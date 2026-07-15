// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { WorkflowRun } from "@/api/workflows";
import type { SessionRecord } from "@/types/agent";

import { RunDetailCard } from "../WorkflowAgentDetail";

const mocks = vi.hoisted(() => ({
  getWorkflowRun: vi.fn(),
  useTaskSessions: vi.fn(),
}));

vi.mock("@/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api")>();
  return { ...actual, getWorkflowRun: mocks.getWorkflowRun };
});

vi.mock("@/hooks/terminal", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/terminal")>();
  return { ...actual, useTaskSessions: mocks.useTaskSessions };
});

vi.mock("@/components/SessionRunDetail/SessionRunDetail", () => ({
  SessionRunDetail: ({
    taskId,
    session,
  }: {
    taskId: string;
    session: SessionRecord;
  }) => (
    <div data-testid="session-run-detail">
      {taskId}:{session.session_id}
    </div>
  ),
}));

function session(taskId: string, sessionId: string): SessionRecord {
  return {
    session_id: sessionId,
    task_id: taskId,
    agent_name: "reviewer",
    backend: "codex",
    started_at: "2026-07-14T20:00:00Z",
    ended_at: null,
    duration_s: 0,
    status: "running",
    exit_code: 0,
    input_tokens: 0,
    output_tokens: 0,
    cache_read_tokens: 0,
    cache_write_tokens: 0,
    estimated_cost_usd: 0,
    files_changed: 0,
    lines_added: 0,
    lines_removed: 0,
    files_touched: [],
    attempt_num: 0,
    is_active: true,
    has_transcript: true,
    has_diff: false,
  };
}

function enrichedRun(overrides: Partial<WorkflowRun> = {}): WorkflowRun {
  return {
    workspace_key: "WS",
    run_id: "run-1",
    driver_id: "review-loop-agent",
    driver_version_id: "v1",
    status: "running",
    output: {
      taskRunId: "task-run-2",
      sessionId: "session-2",
      taskId: "TASK-2",
    },
    steps: [
      {
        id: "step-1",
        step_kind: "task_run",
        task_run_id: "task-run-1",
        task_id: "TASK-1",
        status: "completed",
      },
      {
        id: "step-2",
        step_kind: "task_run",
        task_run_id: "task-run-2",
        task_id: "TASK-2",
        status: "running",
      },
    ],
    created_at: "2026-07-14T20:00:00Z",
    updated_at: "2026-07-14T20:00:01Z",
    ...overrides,
  };
}

describe("RunDetailCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useTaskSessions.mockImplementation((taskId: string | null) => ({
      sessions:
        taskId === "TASK-1"
          ? [session("TASK-1", "flue-task-run-1")]
          : taskId === "TASK-2"
            ? [session("TASK-2", "session-2")]
            : [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    }));
  });

  it("exposes every task session from a multi-step workflow run", async () => {
    const run = enrichedRun();
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<RunDetailCard workspaceId="WS" run={run} />);

    const selector = await screen.findByTestId(
      "workflow-agent-session-selector",
    );
    expect(selector).toHaveTextContent("TASK-1");
    expect(selector).toHaveTextContent("TASK-2");
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-1:flue-task-run-1",
    );

    fireEvent.click(screen.getByRole("tab", { name: "TASK-2" }));

    await waitFor(() =>
      expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
        "TASK-2:session-2",
      ),
    );
  });

  it("keeps enriched session links on a sparse terminal refresh and retries a transient detail failure", async () => {
    const enriched = enrichedRun();
    mocks.getWorkflowRun.mockResolvedValue(enriched);
    const rendered = render(<RunDetailCard workspaceId="WS" run={enriched} />);
    await waitFor(() => expect(mocks.getWorkflowRun).toHaveBeenCalledTimes(1));
    expect(await screen.findAllByRole("tab")).toHaveLength(2);

    mocks.getWorkflowRun.mockRejectedValueOnce(
      new Error("temporary detail outage"),
    );
    const sparseTerminal = enrichedRun({
      status: "completed",
      output: undefined,
      steps: undefined,
      finished_at: "2026-07-14T20:05:00Z",
      updated_at: "2026-07-14T20:05:00Z",
    });
    rendered.rerender(<RunDetailCard workspaceId="WS" run={sparseTerminal} />);

    expect(
      await screen.findByTestId("workflow-agent-run-detail-error"),
    ).toHaveTextContent("temporary detail outage");
    expect(screen.getByText("Completed")).toBeInTheDocument();
    expect(screen.getAllByRole("tab")).toHaveLength(2);
    expect(screen.getByTestId("session-run-detail")).toHaveTextContent(
      "TASK-1:flue-task-run-1",
    );

    mocks.getWorkflowRun.mockResolvedValueOnce(sparseTerminal);
    fireEvent.click(screen.getByTestId("workflow-agent-run-detail-retry"));

    await waitFor(() => expect(mocks.getWorkflowRun).toHaveBeenCalledTimes(3));
    await waitFor(() =>
      expect(
        screen.queryByTestId("workflow-agent-run-detail-error"),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getAllByRole("tab")).toHaveLength(2);
  });
});
