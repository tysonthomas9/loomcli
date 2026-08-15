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

import type { AgentServiceDTO, DriverRunDTO } from "@/api/agentServices";
import { ApiError } from "@/types/common";

import { AgentServiceDetail } from "../AgentServiceDetail";

const mocks = vi.hoisted(() => ({
  runs: [] as DriverRunDTO[],
  refreshRuns: vi.fn<() => Promise<void>>(),
  listRunEvents: vi.fn(),
  getAgentServiceJournal: vi.fn(),
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
