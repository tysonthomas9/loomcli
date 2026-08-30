// @vitest-environment jsdom

import {
  act,
  fireEvent,
  render as testingLibraryRender,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { ReactElement } from "react";
import "@testing-library/jest-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type {
  AgentServiceDTO,
  DriverRunDTO,
  TaskRunDTO,
} from "@/api/agentServices";
import { ApiError } from "@/types/common";
import { KeyboardShortcutProvider } from "@/hooks/ui";

import { AgentServiceDetail } from "../AgentServiceDetail";

function render(ui: ReactElement) {
  const result = testingLibraryRender(
    <KeyboardShortcutProvider>{ui}</KeyboardShortcutProvider>,
  );
  return {
    ...result,
    rerender: (next: ReactElement) =>
      result.rerender(
        <KeyboardShortcutProvider>{next}</KeyboardShortcutProvider>,
      ),
  };
}

const mocks = vi.hoisted(() => ({
  runs: [] as DriverRunDTO[],
  refreshRuns: vi.fn<() => Promise<void>>(),
  listRunEvents: vi.fn(),
  getAgentServiceJournal: vi.fn(),
  listAgentServiceRunTasks: vi.fn(),
  getTaskRunLog: vi.fn(),
  getTaskRunTranscript: vi.fn(),
  getDriverRunLog: vi.fn(),
  getIssue: vi.fn(),
  patchAgentService: vi.fn(),
  removeAgentService: vi.fn(),
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
    useAgentServiceMutations: () => ({
      create: vi.fn(),
      patch: mocks.patchAgentService,
      remove: mocks.removeAgentService,
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
    getTaskRunTranscript: mocks.getTaskRunTranscript,
    getDriverRunLog: mocks.getDriverRunLog,
  };
});

vi.mock("@/api/issues", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/issues")>();
  return {
    ...actual,
    getIssue: mocks.getIssue,
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
  triggerKind: "cron",
  enabled: true,
  behavior: {
    roleName: "scout",
    roleDisplayName: "Scout",
    workflowName: "scout",
    scripted: true,
  },
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
    transcriptAvailable: true,
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
    mocks.getTaskRunTranscript.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error: "task transcript is not available yet",
      }),
    );
    mocks.getDriverRunLog.mockRejectedValue(
      new ApiError(404, "Not Found", {
        error: "run log is not available yet",
      }),
    );
    mocks.getIssue.mockRejectedValue(
      new ApiError(404, "Not Found", { error: "issue not found" }),
    );
    mocks.patchAgentService.mockImplementation(
      async (
        _id: string,
        request: { desiredState?: "running" | "stopped" },
      ) => ({
        ...service,
        enabled:
          request.desiredState === undefined
            ? service.enabled
            : request.desiredState === "running",
      }),
    );
    mocks.removeAgentService.mockResolvedValue(undefined);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("renders the editable role prompt for a scripted role", () => {
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    fireEvent.click(screen.getByRole("tab", { name: "Settings" }));
    expect(screen.getByTestId("role-prompt-card")).toHaveAttribute(
      "data-role",
      "scout",
    );
  });

  it("disables and re-enables a scripted instance through desiredState", async () => {
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    fireEvent.click(screen.getByRole("tab", { name: "Settings" }));

    fireEvent.click(screen.getByRole("button", { name: "Disable agent" }));
    await waitFor(() => {
      expect(mocks.patchAgentService).toHaveBeenCalledWith("scout", {
        desiredState: "stopped",
      });
    });
    expect(
      await screen.findByRole("button", { name: "Enable agent" }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Enable agent" }));
    await waitFor(() => {
      expect(mocks.patchAgentService).toHaveBeenLastCalledWith("scout", {
        desiredState: "running",
      });
    });
    expect(
      await screen.findByRole("button", { name: "Disable agent" }),
    ).toBeInTheDocument();
  });

  it("removes a scripted instance only after confirmation", async () => {
    const onRemoved = vi.fn();
    render(
      <AgentServiceDetail
        workspaceId="WS"
        service={service}
        onRemoved={onRemoved}
      />,
    );

    fireEvent.click(screen.getByRole("tab", { name: "Settings" }));
    fireEvent.click(screen.getByText("Danger zone"));
    fireEvent.click(screen.getByRole("button", { name: "Remove agent" }));
    const dialog = screen.getByRole("alertdialog", {
      name: "Remove scheduled agent",
    });
    expect(within(dialog).getByText("scout")).toBeInTheDocument();
    expect(mocks.removeAgentService).not.toHaveBeenCalled();

    fireEvent.click(within(dialog).getByRole("button", { name: "Remove" }));
    await waitFor(() => {
      expect(mocks.removeAgentService).toHaveBeenCalledWith("scout");
    });
    expect(onRemoved).toHaveBeenCalledOnce();
  });

  it("renders the role prompt card for plain prompt roles", () => {
    render(
      <AgentServiceDetail
        workspaceId="WS"
        service={{
          ...service,
          id: "reviewer",
          triggerKind: "event",
          behavior: { roleName: "reviewer", scripted: false },
        }}
      />,
    );
    fireEvent.click(screen.getByRole("tab", { name: "Settings" }));
    expect(screen.getByTestId("role-prompt-card")).toHaveAttribute(
      "data-role",
      "reviewer",
    );
  });

  it("shows paused instead of next-fire times while the service is disabled", () => {
    render(
      <AgentServiceDetail
        workspaceId="WS"
        service={{
          ...service,
          enabled: false,
          nextFireAt: "2026-08-17T00:00:00Z",
          bindings: [
            {
              id: "binding-1",
              sourceKind: "cron",
              schedule: "@daily",
              enabled: true,
              routeKey: "scout",
            },
          ],
        }}
      />,
    );

    expect(screen.getByText("Next run").nextElementSibling).toHaveTextContent(
      "Paused",
    );
  });

  it("expands a run without repeating its summary and shows harness and newest-last events", async () => {
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
      detail.queryByText(
        "Reviewed every candidate and recorded the final recommendation.",
      ),
    ).not.toBeInTheDocument();
    expect(detail.getByText("1m 30s")).toBeInTheDocument();
    expect(detail.queryByText("line one")).not.toBeInTheDocument();
    expect(
      detail.queryByText(/runtime\/scout\/run-1\.log/),
    ).not.toBeInTheDocument();
    fireEvent.click(detail.getByRole("button", { name: "Harness log" }));
    expect(
      await detail.findByText("No harness log content."),
    ).toBeInTheDocument();

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
      await screen.findByText("No task runs were recorded."),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("heading", { name: "Output (tail)" }),
    ).not.toBeInTheDocument();
    await waitFor(() => expect(mocks.listRunEvents).toHaveBeenCalledOnce());
  });

  it("renders the issue title as primary and runner as the task-run subtitle", async () => {
    mocks.getIssue.mockResolvedValueOnce({
      id: "WS-1",
      title: "Review the suggested dependency cleanup",
      priority: 2,
      created_at: "2026-08-14T00:00:00Z",
      updated_at: "2026-08-14T00:00:00Z",
    });
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun({ transcriptAvailable: false })],
      total: 1,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    const section = await screen.findByTestId("task-logs-section");
    expect(
      await within(section).findByText(
        "Review the suggested dependency cleanup",
      ),
    ).toBeInTheDocument();
    expect(within(section).getByText("scout-task-runner")).toBeInTheDocument();
    expect(within(section).getByText("Completed")).toBeInTheDocument();
    expect(within(section).getByText("30s")).toBeInTheDocument();
    expect(mocks.listAgentServiceRunTasks).toHaveBeenCalledWith(
      "WS",
      "scout",
      "run-1",
    );
  });

  it("renders a declared task title without resolving the taskId as an issue", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [
        taskRun({
          taskId: "scout-analyze",
          taskTitle: "Analyze repositories",
          transcriptAvailable: false,
        }),
      ],
      total: 1,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    const section = await screen.findByTestId("task-logs-section");
    expect(
      await within(section).findByText("Analyze repositories"),
    ).toBeInTheDocument();
    // The scout's taskIds are phase labels, so an issue lookup is a guaranteed
    // 404 — a declared title means it is never attempted.
    expect(mocks.getIssue).not.toHaveBeenCalled();
  });

  it("lazily loads and displays a truncated task AI log", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun({ transcriptAvailable: false })],
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
    mocks.getTaskRunTranscript.mockResolvedValueOnce([
      {
        seq: 1,
        timestamp: "2026-08-14T10:00:20Z",
        role: "assistant",
        type: "tool_use",
        tool_name: "shell",
        tool_input: { command: '/bin/bash -lc "make gate"' },
        output: "[exit 1]\ngate failed\n",
      },
      {
        seq: 2,
        timestamp: "2026-08-14T10:00:30Z",
        role: "assistant",
        type: "text",
        text: '{"recommendations":[]}',
      },
      {
        seq: 3,
        timestamp: "2026-08-14T10:00:40Z",
        role: "system",
        type: "result",
        text: "completed | in=349798 out=3342 cache_read=305920",
        output: JSON.stringify({
          input_tokens: 349_798,
          cache_read_tokens: 305_920,
          output_tokens: 3_342,
        }),
      },
    ]);
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );

    const viewToggle = await screen.findByTestId("task-log-view-toggle");
    expect(mocks.getTaskRunTranscript).toHaveBeenCalledWith("WS", "task-1");
    expect(mocks.getTaskRunLog).not.toHaveBeenCalled();
    const prettyButton = within(viewToggle).getByRole("button", {
      name: "Pretty",
    });
    const rawButton = within(viewToggle).getByRole("button", { name: "Raw" });
    expect(prettyButton).toHaveAttribute("aria-pressed", "true");
    expect(rawButton).toHaveAttribute("aria-pressed", "false");
    expect(screen.getByText("1 tool call")).toBeInTheDocument();
    expect(screen.getByTestId("tool-pill")).toHaveTextContent("shell");
    expect(screen.getByTestId("tool-pill")).toHaveTextContent("make gate");
    expect(screen.getByText('{"recommendations":[]}')).toBeInTheDocument();
    expect(screen.queryByText("gate failed")).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId("tool-pill"));
    expect(screen.getByText(/gate failed/)).toBeInTheDocument();

    fireEvent.click(rawButton);
    expect(rawButton).toHaveAttribute("aria-pressed", "true");
    expect(prettyButton).toHaveAttribute("aria-pressed", "false");
    expect(screen.queryByText("1 tool call")).not.toBeInTheDocument();
    expect(
      (await screen.findByTestId("task-log-content-task-1")).textContent,
    ).toBe(rawLog);
    expect(mocks.getTaskRunLog).toHaveBeenCalledWith("WS", "task-1");
  });

  it("shows an empty state when a task AI log is absent", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun({ transcriptAvailable: false })],
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

  it("skips settled log and transcript fetches when neither artifact exists", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun({ logsAvailable: false, transcriptAvailable: false })],
      total: 1,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );

    expect(screen.getByTestId("task-log-empty")).toHaveTextContent(
      "No AI log is available",
    );
    expect(
      screen.queryByTestId("task-log-view-toggle"),
    ).not.toBeInTheDocument();
    expect(mocks.getTaskRunLog).not.toHaveBeenCalled();
    expect(mocks.getTaskRunTranscript).not.toHaveBeenCalled();
  });

  it("shows transcript empty and error states in Pretty view", async () => {
    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    mocks.getTaskRunTranscript.mockResolvedValueOnce([]);
    const { unmount } = render(
      <AgentServiceDetail workspaceId="WS" service={service} />,
    );
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );
    expect(
      await screen.findByTestId("task-transcript-empty"),
    ).toHaveTextContent("No transcript content");
    unmount();

    mocks.listAgentServiceRunTasks.mockResolvedValueOnce({
      data: [taskRun()],
      total: 1,
    });
    mocks.getTaskRunTranscript.mockRejectedValueOnce(new Error("network down"));
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();
    fireEvent.click(
      await screen.findByRole("button", {
        name: /scout-task-runner.*Completed.*30s/,
      }),
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Transcript unavailable: network down",
    );
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

  it("shows a friendly empty harness state without leaking its artifact URI", async () => {
    mocks.runs = [
      completedRun({
        output: { logs_ref: "artifact://driver-runs/run-1/empty" },
      }),
    ];
    mocks.getDriverRunLog.mockResolvedValueOnce({
      content: "",
      modifiedAt: "2026-08-14T10:01:30Z",
      truncated: false,
    });
    render(<AgentServiceDetail workspaceId="WS" service={service} />);
    expandRun();

    expect(screen.queryByText(/artifact:\/\//)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Harness log" }));

    expect(
      await screen.findByText("No harness log content."),
    ).toBeInTheDocument();
    expect(screen.queryByText(/artifact:\/\//)).not.toBeInTheDocument();
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
          transcriptAvailable: false,
        }),
      ],
      total: 1,
    });
    mocks.getTaskRunTranscript.mockResolvedValue([
      { seq: 1, role: "assistant", type: "text", text: "live AI output" },
    ]);
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
    expect(mocks.getTaskRunTranscript).toHaveBeenCalledTimes(1);
    expect(mocks.getTaskRunLog).not.toHaveBeenCalled();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(5_000);
    });

    expect(mocks.listAgentServiceRunTasks).toHaveBeenCalledTimes(2);
    expect(mocks.getTaskRunTranscript).toHaveBeenCalledTimes(2);
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
