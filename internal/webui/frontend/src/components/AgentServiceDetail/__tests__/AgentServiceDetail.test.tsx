// @vitest-environment jsdom

import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AgentServiceDTO,
  DriverRunDTO,
  TaskRunDTO,
} from "@/api/agentServices";
import { ApiError } from "@/types/common";

import { AgentServiceDetail } from "../AgentServiceDetail";

const mocks = vi.hoisted(() => ({
  runs: [] as DriverRunDTO[],
  refreshRuns: vi.fn<() => Promise<void>>(),
  listRunEvents: vi.fn(),
  getAgentServiceJournal: vi.fn(),
  listAgentServiceRunTasks: vi.fn(),
  getTaskRunLog: vi.fn(),
  getDriverRunLog: vi.fn(),
}));

vi.mock("@/hooks/workspace", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/hooks/workspace")>();
  return {
    ...actual,
    useAgentServiceRuns: () => ({
      runs: mocks.runs,
      total: mocks.runs.length,
      loading: false,
      initialized: true,
      error: null,
      notFound: false,
      refresh: mocks.refreshRuns,
    }),
  };
});

vi.mock("@/api/agentServices", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/agentServices")>();
  return {
    ...actual,
    listRunEvents: mocks.listRunEvents,
    getAgentServiceJournal: mocks.getAgentServiceJournal,
    listAgentServiceRunTasks: mocks.listAgentServiceRunTasks,
    getTaskRunLog: mocks.getTaskRunLog,
    getDriverRunLog: mocks.getDriverRunLog,
  };
});

vi.mock("@/components/RolePromptCard", () => ({
  RolePromptCard: ({ roleName }: { roleName: string }) => (
    <div data-testid="role-prompt-card" data-role={roleName} />
  ),
}));

const service: AgentServiceDTO = {
  id: "scout",
  name: "Scout",
  kind: "scripted",
  enabled: true,
  behavior: { driverId: "scout", driverVersionId: "scout-v1" },
  bindings: [],
  nextFireAt: null,
  lastRunStatus: "completed",
  consecutiveFailures: 0,
  errors: [],
  createdAt: "2026-08-14T00:00:00Z",
  updatedAt: "2026-08-14T00:00:00Z",
};

function completedRun(overrides: Partial<DriverRunDTO> = {}): DriverRunDTO {
  return {
    workspaceKey: "WS",
    runId: "run-1",
    driverId: "scout",
    driverVersionId: "scout-v1",
    agentServiceId: "scout",
    status: "completed",
    summary: "Reviewed every candidate and recorded the final recommendation.",
    startedAt: "2026-08-14T10:00:00Z",
    finishedAt: "2026-08-14T10:01:30Z",
    createdAt: "2026-08-14T10:00:00Z",
    updatedAt: "2026-08-14T10:01:30Z",
    ...overrides,
  };
}

function expandRun(runId = "run-1"): void {
  fireEvent.click(
    within(screen.getByTestId(`agent-service-run-${runId}`)).getByRole(
      "button",
    ),
  );
}

function taskRun(overrides: Partial<TaskRunDTO> = {}): TaskRunDTO {
  return {
    taskRunId: "task-1",
    taskId: "WS-1",
    status: "completed",
    runner: "scout-task-runner",
    startedAt: "2026-08-14T10:00:10Z",
    finishedAt: "2026-08-14T10:00:40Z",
    logsAvailable: true,
    ...overrides,
  };
}

describe("AgentServiceDetail", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.useRealTimers();
    mocks.runs = [completedRun()];
    mocks.refreshRuns.mockResolvedValue();
    mocks.listRunEvents.mockResolvedValue({ events: [] });
    mocks.getAgentServiceJournal.mockResolvedValue({
      serviceId: "scout",
      filename: "history.md",
      content: "# Scout journal",
      modifiedAt: "2026-08-14T12:00:00Z",
      truncated: false,
    });
    mocks.listAgentServiceRunTasks.mockResolvedValue({
      data: [],
      total: 0,
    });
    mocks.getTaskRunLog.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error: "task log is not available yet",
      }),
    );
    mocks.getDriverRunLog.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error: "run log is not available yet",
      }),
    );
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("explains that scripted behavior comes from the driver", () => {
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expect(screen.getByTestId("scripted-agent-prompt-note")).toHaveTextContent(
      "Scripted agent — behavior comes from its driver, not a role prompt.",
    );
    expect(screen.queryByTestId("role-prompt-card")).not.toBeInTheDocument();
  });

  it("renders the role prompt card for prompt-kind services", () => {
    render(
      <AgentServiceDetail
        workspaceId="WS"
        service={{
          ...service,
          id: "reviewer",
          kind: "prompt",
          behavior: { roleName: "reviewer" },
        }}
      />,
    );
    expect(screen.getByTestId("role-prompt-card")).toHaveAttribute(
      "data-role",
      "reviewer",
    );
    expect(
      screen.queryByTestId("scripted-agent-prompt-note"),
    ).not.toBeInTheDocument();
  });

  it("expands a run with full details, stdout tail, logs reference, and newest-last events", async () => {
    mocks.runs = [
      completedRun({
        output: {
          flue_stdout_tail: "line one\nline two",
          logs_ref: "runtime/scout/run-1.log",
        },
      }),
    ];
    mocks.listRunEvents.mockResolvedValueOnce({
      events: [
        {
          id: "2-0",
          timestamp: "2026-08-14T10:01:30Z",
          actor: "driver",
          action: "driver_run.finish",
          entity_type: "driver_run",
          entity_id: "run-1",
          workspace_id: "WS",
        },
        {
          id: "1-0",
          timestamp: "2026-08-14T10:00:00Z",
          actor: "api",
          action: "driver_run.create",
          entity_type: "driver_run",
          entity_id: "run-1",
          workspace_id: "WS",
        },
      ],
    });

    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    const panel = document.getElementById("agent-service-run-panel-run-1");
    expect(panel).not.toBeNull();
    const detail = within(panel as HTMLElement);
    expect(
      detail.getByText(
        "Reviewed every candidate and recorded the final recommendation.",
      ),
    ).toBeInTheDocument();
    expect(detail.getByText("1m 30s")).toBeInTheDocument();
    expect(
      detail.getByRole("heading", { name: "Output (tail)" }),
    ).toBeInTheDocument();
    expect(panel?.querySelector("pre")?.textContent).toBe("line one\nline two");
    expect(detail.getByText(/runtime\/scout\/run-1\.log/)).toBeInTheDocument();

    await waitFor(() => {
      expect(detail.getByText("driver_run.finish")).toBeInTheDocument();
    });
    expect(
      detail.getAllByRole("listitem").map((item) => item.textContent),
    ).toEqual([
      expect.stringContaining("driver_run.create"),
      expect.stringContaining("driver_run.finish"),
    ]);
    expect(mocks.listRunEvents).toHaveBeenCalledWith("WS", "run-1");
  });

  it("handles a run without output", async () => {
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    expect(
      screen.getByText("No output was captured for this run."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Output (tail)" }),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.listRunEvents).toHaveBeenCalledOnce());
  });

  it("renders task runs with runner, status, and duration", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    const section = await screen.findByTestId("task-logs-section");
    expect(within(section).getByText("scout-task-runner")).toBeInTheDocument();
    expect(within(section).getByText("Completed")).toBeInTheDocument();
    expect(within(section).getByText("30s")).toBeInTheDocument();
    expect(mocks.listAgentServiceRunTasks).toHaveBeenCalledWith(
      "WS",
      "scout",
      "run-1",
    );
  });

  it("lazily loads and displays a truncated task AI log", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    mocks.getTaskRunLog.mockResolvedValueOnce({
      content: "repo discovery\ncodex CLI exit=0\nbackend output",
      modifiedAt: "2026-08-14T10:00:40Z",
      truncated: true,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    const taskToggle = await screen.findByRole("button", {
      name: /scout-task-runner.*Completed.*30s/,
    });
    expect(mocks.getTaskRunLog).not.toHaveBeenCalled();
    fireEvent.click(taskToggle);

    expect(
      (await screen.findByTestId("task-log-content-task-1")).textContent,
    ).toBe("repo discovery\ncodex CLI exit=0\nbackend output");
    expect(screen.getByRole("status")).toHaveTextContent("last 1 MiB");
    expect(
      screen.queryByTestId("task-log-view-toggle"),
    ).not.toBeInTheDocument();
    expect(mocks.getTaskRunLog).toHaveBeenCalledWith("WS", "task-1");
  });

  it("defaults Codex task logs to Pretty and supports disclosures and Raw view", async () => {
    const rawLog = [
      "codex CLI exit=1",
      '{"type":"item.started","item":{"id":"item_1","type":"command_execution","status":"in_progress"}}',
      '{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/bash -lc \\"make gate\\"","exit_code":1,"status":"failed","aggregated_output":"gate failed\\n"}}',
      '{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"{\\"recommendations\\":[]}"}}',
      '{"type":"turn.completed","usage":{"input_tokens":349798,"cached_input_tokens":305920,"output_tokens":3342}}',
    ].join("\n");
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    mocks.getTaskRunLog.mockResolvedValueOnce({
      content: rawLog,
      modifiedAt: "2026-08-14T10:00:40Z",
      truncated: false,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );

    const viewToggle = await screen.findByTestId("task-log-view-toggle");
    const prettyButton = within(viewToggle).getByRole("button", {
      name: "Pretty",
    });
    const rawButton = within(viewToggle).getByRole("button", { name: "Raw" });
    expect(prettyButton).toHaveAttribute("aria-pressed", "true");
    expect(rawButton).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByTestId("transcript-view")).toBeInTheDocument();
    expect(screen.getByTestId("transcript-command")).toHaveTextContent(
      '$ /bin/bash -lc "make gate"',
    );
    expect(screen.getByTestId("transcript-command")).toHaveTextContent(
      "exit 1",
    );
    expect(screen.getByTestId("transcript-message")).toHaveTextContent(
      '"recommendations": []',
    );
    expect(screen.getByTestId("transcript-turn-completed")).toHaveTextContent(
      "349,798 input tokens (305,920 cached) · 3,342 output",
    );
    expect(screen.queryByText("gate failed")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("transcript-output-toggle"));
    expect(screen.getByText("gate failed")).toBeInTheDocument();

    fireEvent.click(rawButton);
    expect(rawButton).toHaveAttribute("aria-pressed", "true");
    expect(prettyButton).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByTestId("transcript-view")).not.toBeInTheDocument();
    expect(screen.getByTestId("task-log-content-task-1").textContent).toBe(
      rawLog,
    );
  });

  it("shows an empty state when a task AI log is absent", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );

    expect(await screen.findByTestId("task-log-empty")).toHaveTextContent(
      "No AI log is available",
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("lazily replaces a dangling logs reference with the harness log", async () => {
    mocks.runs = [
      completedRun({
        output: { logs_ref: "driver-run://run-1/flue-local" },
      }),
    ];
    mocks.getDriverRunLog.mockResolvedValueOnce({
      content: "===== stdout =====\nworkflow output\n\n===== stderr =====\n",
      modifiedAt: "2026-08-14T10:01:30Z",
      truncated: true,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    expect(mocks.getDriverRunLog).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Harness log" }));

    expect((await screen.findByTestId("harness-log-content")).textContent).toBe(
      "===== stdout =====\nworkflow output\n\n===== stderr =====\n",
    );
    expect(
      screen.queryByText(/driver-run:\/\/run-1\/flue-local/),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("last 1 MiB");
  });

  it("refreshes a live task list and its expanded AI log every five seconds", async () => {
    vi.useFakeTimers();
    mocks.runs = [
      completedRun({
        status: "running",
        finishedAt: null,
        updatedAt: "2026-08-14T10:00:10Z",
      }),
    ];
    mocks.listAgentServiceRunTasks.mockResolvedValue({
      data: [
        taskRun({
          status: "running",
          finishedAt: null,
        }),
      ],
      total: 1,
    });
    mocks.getTaskRunLog.mockResolvedValue({
      content: "live AI output",
      modifiedAt: "2026-08-14T10:00:20Z",
      truncated: false,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    fireEvent.click(
      screen.getByRole("button", {
        name: /scout-task-runner.*Running/,
      }),
    );
    await act(async () => {
      await vi.advanceTimersByTimeAsync(0);
    });
    expect(mocks.listAgentServiceRunTasks).toHaveBeenCalledTimes(1);
    expect(mocks.getTaskRunLog).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    expect(mocks.listAgentServiceRunTasks).toHaveBeenCalledTimes(2);
    expect(mocks.getTaskRunLog).toHaveBeenCalledTimes(2);
  });

  it("polls an expanded running run and stops after it becomes completed", async () => {
    vi.useFakeTimers();
    mocks.runs = [
      completedRun({
        status: "running",
        finishedAt: null,
        updatedAt: "2026-08-14T10:00:10Z",
      }),
    ];
    const { rerender } = render(
      <AgentServiceDetail workspaceId="WS" service={service} />,
    );
    expandRun();
    await act(async () => {
      await Promise.resolve();
    });
    expect(mocks.listRunEvents).toHaveBeenCalledTimes(1);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });
    expect(mocks.listRunEvents).toHaveBeenCalledTimes(2);
    expect(mocks.refreshRuns).toHaveBeenCalledTimes(1);

    mocks.runs = [completedRun({ status: "completed" })];
    rerender(<AgentServiceDetail workspaceId="WS" service={service} />);
    await act(async () => {
      await Promise.resolve();
    });
    expect(mocks.getAgentServiceJournal).toHaveBeenCalledWith("WS", "scout");

    const eventCalls = mocks.listRunEvents.mock.calls.length;
    const runRefreshes = mocks.refreshRuns.mock.calls.length;
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000);
    });
    expect(mocks.listRunEvents).toHaveBeenCalledTimes(eventCalls);
    expect(mocks.refreshRuns).toHaveBeenCalledTimes(runRefreshes);
  });

  it("renders journal markdown, updated time, and truncation notice", async () => {
    mocks.getAgentServiceJournal.mockResolvedValueOnce({
      serviceId: "scout",
      filename: "history.md",
      content: "# Scout findings\n\nAn **important** recommendation.",
      modifiedAt: "2026-08-14T12:00:00Z",
      truncated: true,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);

    fireEvent.click(screen.getByRole("tab", { name: "Journal" }));

    expect(
      await screen.findByRole("heading", { name: "Scout findings" }),
    ).toBeInTheDocument();
    expect(screen.getByText("important").tagName).toBe("STRONG");
    expect(screen.getByText(/^Updated /)).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("last 512 KiB");
  });

  it("renders a friendly empty state for a missing journal", async () => {
    mocks.getAgentServiceJournal.mockRejectedValueOnce(
      new ApiError(404, "Not Found", {
        error: "no journal yet — the scout has not completed a run",
      }),
    );
    render(<AgentServiceDetail workspaceId="WS" service={service} />);

    fireEvent.click(screen.getByRole("tab", { name: "Journal" }));

    expect(await screen.findByTestId("journal-empty")).toHaveTextContent(
      "No journal yet",
    );
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("surfaces journal errors", async () => {
    mocks.getAgentServiceJournal.mockRejectedValueOnce(
      new ApiError(500, "Internal Server Error", {
        error: "read agent service journal failed",
      }),
    );
    render(<AgentServiceDetail workspaceId="WS" service={service} />);

    fireEvent.click(screen.getByRole("tab", { name: "Journal" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Journal unavailable: read agent service journal failed",
    );
  });
});
