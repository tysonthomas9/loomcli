// @vitest-environment jsdom

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TriggerBinding, WorkflowRun } from "@/api/workflows";
import type { SessionRecord } from "@/types/agent";

import {
  AgentRecordRunsPane,
  ManageCard,
  RunDetailCard,
} from "../WorkflowAgentDetail";

const mocks = vi.hoisted(() => ({
  getWorkflowRun: vi.fn(),
  useAgentHistory: vi.fn(),
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

vi.mock("@/hooks/agents", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/agents")>();
  return { ...actual, useAgentHistory: mocks.useAgentHistory };
});

vi.mock("@/components/SessionRunDetail/SessionRunDetail", () => ({
  SessionRunDetail: ({
    taskId,
    session,
    retryTranscriptUnavailable,
    exitCodeKnown,
    telemetryKnown,
  }: {
    taskId: string;
    session: SessionRecord;
    retryTranscriptUnavailable?: boolean;
    exitCodeKnown?: boolean;
    telemetryKnown?: boolean;
  }) => (
    <div
      data-testid="session-run-detail"
      data-retry-transcript={String(retryTranscriptUnavailable === true)}
      data-exit-known={String(exitCodeKnown !== false)}
      data-telemetry-known={String(telemetryKnown !== false)}
    >
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

describe("ManageCard", () => {
  it("saves a managed cron name and cadence as separate patches", async () => {
    const binding: TriggerBinding = {
      workspace_key: "WS",
      binding_id: "binding-1",
      name: "Daily docs",
      source_kind: "cron",
      route_key: "binding-1",
      driver_id: "prompt-agent",
      driver_version_id: "v1",
      target_agent_service_id: "agent-1",
      schedule: "*/10 * * * *",
      enabled: true,
    };
    const onUpdate = vi.fn(
      async (_bindingId: string, patch: Partial<TriggerBinding>) => ({
        ...binding,
        ...patch,
      }),
    );
    render(
      <ManageCard
        binding={binding}
        isCron
        onEditConfig={vi.fn()}
        onUpdate={onUpdate}
        onDelete={vi.fn()}
        onDeleted={vi.fn()}
      />,
    );

    fireEvent.change(screen.getByTestId("workflow-agent-edit-name"), {
      target: { value: "Weekday docs" },
    });
    fireEvent.change(screen.getByTestId("workflow-agent-edit-cadence"), {
      target: { value: "daily" },
    });
    fireEvent.click(screen.getByTestId("workflow-agent-save-name"));
    await waitFor(() =>
      expect(onUpdate).toHaveBeenNthCalledWith(1, "binding-1", {
        name: "Weekday docs",
      }),
    );

    fireEvent.click(screen.getByTestId("workflow-agent-save-schedule"));
    await waitFor(() =>
      expect(onUpdate).toHaveBeenNthCalledWith(2, "binding-1", {
        schedule: "0 9 * * *",
      }),
    );
  });
});

describe("RunDetailCard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.useAgentHistory.mockReturnValue({
      runs: [],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
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

  it("renders record-scoped runs returned across attached bindings", async () => {
    const first = enrichedRun({
      run_id: "run-from-review-binding",
      status: "completed",
      output: undefined,
      steps: undefined,
      summary: "Reviewed documentation",
    });
    const second = enrichedRun({
      run_id: "run-from-schedule-binding",
      status: "completed",
      output: undefined,
      steps: undefined,
      summary: "Refreshed documentation",
    });
    mocks.useAgentHistory.mockReturnValue({
      runs: [first, second],
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mocks.getWorkflowRun.mockResolvedValue(
      enrichedRun({
        run_id: first.run_id,
        status: "completed",
        output: undefined,
        steps: [
          {
            id: "step-docs",
            step_kind: "task_run",
            task_run_id: "task-run-docs",
            task_id: "TASK-DOCS",
            status: "completed",
          },
        ],
        summary: first.summary,
      }),
    );

    render(
      <AgentRecordRunsPane
        workspaceId="WS"
        record={{
          id: "agent-record-1",
          name: "Documentation reviewer",
          kind: "prompt",
          enabled: true,
          behavior: { role_name: "documentation" },
          workspace_key: "WS",
        }}
        bindings={[
          {
            workspace_key: "WS",
            binding_id: "binding-review",
            name: "Documentation reviewer",
            source_kind: "internal",
            route_key: "binding-review",
            driver_id: "prompt-agent",
            driver_version_id: "v1",
            target_agent_service_id: "agent-record-1",
            enabled: true,
          },
        ]}
      />,
    );

    expect(mocks.useAgentHistory).toHaveBeenCalledWith(
      "WS",
      "agent-record-1",
      true,
    );
    expect(
      await screen.findByTestId("workflow-agent-run-list"),
    ).toHaveTextContent("Reviewed documentation");
    expect(screen.getByTestId("workflow-agent-run-list")).toHaveTextContent(
      "Refreshed documentation",
    );
    expect(
      within(await screen.findByTestId("workflow-agent-run-detail")).getByRole(
        "link",
        { name: "TASK-DOCS" },
      ),
    ).toHaveAttribute("href", "/ws/WS/issues/TASK-DOCS");
    expect(
      within(screen.getByTestId("workflow-agent-run-list")).queryByRole("link"),
    ).not.toBeInTheDocument();
  });

  it("exposes every task session from a multi-step workflow run", async () => {
    const run = enrichedRun();
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<RunDetailCard workspaceId="WS" run={run} />);

    const selector = await screen.findByTestId(
      "workflow-agent-session-selector",
    );
    const detail = screen.getByTestId("workflow-agent-run-detail");
    expect(selector).toHaveTextContent("TASK-1");
    expect(selector).toHaveTextContent("TASK-2");
    expect(
      within(detail).getByRole("link", { name: "TASK-1" }),
    ).toHaveAttribute("href", "/ws/WS/issues/TASK-1");
    expect(
      within(detail).getByRole("link", { name: "TASK-2" }),
    ).toHaveAttribute("href", "/ws/WS/issues/TASK-2");
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

  it("retries a terminal fallback session while its transcript projection catches up", async () => {
    mocks.useTaskSessions.mockReturnValue({
      sessions: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    const run = enrichedRun({
      status: "completed",
      finished_at: "2026-07-14T20:05:00Z",
      updated_at: "2026-07-14T20:05:00Z",
    });
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<RunDetailCard workspaceId="WS" run={run} />);

    expect(await screen.findByTestId("session-run-detail")).toHaveAttribute(
      "data-retry-transcript",
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

  it("explains why a terminal run with no child task has no transcript", async () => {
    const run = enrichedRun({
      status: "completed",
      output: undefined,
      steps: undefined,
      summary: "local-review: reviewed 0, approved 0, skipped 0 (cap 10)",
      finished_at: "2026-07-14T20:05:00Z",
      updated_at: "2026-07-14T20:05:00Z",
    });
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<RunDetailCard workspaceId="WS" run={run} />);

    expect(
      await screen.findByText(/did not create a child task or invoke a model/i),
    ).toHaveTextContent(/no eligible task was available/i);
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  it("preserves the generic transcript copy while child linkage is ambiguous", async () => {
    const run = enrichedRun({
      status: "running",
      output: undefined,
      steps: undefined,
    });
    mocks.getWorkflowRun.mockResolvedValue(run);

    render(<RunDetailCard workspaceId="WS" run={run} />);

    expect(
      await screen.findByText("No task-run transcript linked to this run yet."),
    ).toBeInTheDocument();
  });
});
