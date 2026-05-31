/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import { useRouteView } from "@/hooks";
import {
  useCancelWorkflowRun,
  useStartWorkflowRun,
  useWorkflowDefinitions,
  useWorkflowRunEventSnapshots,
  useWorkflowRunEvents,
  useWorkflowRuns,
  type WorkflowDefinition,
  type WorkflowRun,
  type WorkflowRunEvent,
} from "@/hooks/workflows";

import { WorkflowsPage } from "../WorkflowsPage";

vi.mock("@/hooks", () => ({
  useRouteView: vi.fn(),
}));

vi.mock("@/hooks/workflows", () => ({
  isWorkflowRunLive: (status: string) =>
    status === "queued" || status === "running" || status === "waiting",
  useCancelWorkflowRun: vi.fn(),
  useStartWorkflowRun: vi.fn(),
  useWorkflowDefinitions: vi.fn(),
  useWorkflowRunEventSnapshots: vi.fn(),
  useWorkflowRunEvents: vi.fn(),
  useWorkflowRuns: vi.fn(),
}));

const mockUseRouteView = vi.mocked(useRouteView);
const mockUseCancelWorkflowRun = vi.mocked(useCancelWorkflowRun);
const mockUseStartWorkflowRun = vi.mocked(useStartWorkflowRun);
const mockUseWorkflowDefinitions = vi.mocked(useWorkflowDefinitions);
const mockUseWorkflowRunEventSnapshots = vi.mocked(
  useWorkflowRunEventSnapshots,
);
const mockUseWorkflowRunEvents = vi.mocked(useWorkflowRunEvents);
const mockUseWorkflowRuns = vi.mocked(useWorkflowRuns);

const workflowDefinition: WorkflowDefinition = {
  workspace_key: "WS",
  name: "route-runner",
  version: "v1",
  status: "active",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const liveRun: WorkflowRun = {
  workspace_key: "WS",
  run_id: "wrun-1",
  workflow_name: "route-runner",
  workflow_version: "v1",
  status: "running",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const waitingRun: WorkflowRun = {
  ...liveRun,
  run_id: "wrun-2",
  status: "waiting",
  created_at: "2026-01-01T00:01:00Z",
  updated_at: "2026-01-01T00:01:00Z",
};

const routeEvent: WorkflowRunEvent = {
  workspace_key: "WS",
  event_id: "evt-1",
  workflow_run_id: "wrun-1",
  event_index: 1,
  type: "workflow_route_admitted",
  message: "Route admission accepted",
  data: { route: "/workflows/route-runner" },
  created_at: "2026-01-01T00:00:01Z",
};

const waitingEvent: WorkflowRunEvent = {
  ...routeEvent,
  event_id: "evt-2",
  workflow_run_id: "wrun-2",
  event_index: 2,
  type: "workflow_waiting",
  message: "Waiting for trigger follow-up",
  created_at: "2026-01-01T00:01:01Z",
};

describe("WorkflowsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseRouteView.mockReturnValue({
      view: "workflows",
      setView: vi.fn(),
      navigateToView: vi.fn(),
    });
    mockUseCancelWorkflowRun.mockReturnValue(vi.fn());
    mockUseStartWorkflowRun.mockReturnValue(vi.fn());
    mockUseWorkflowDefinitions.mockReturnValue({
      definitions: [workflowDefinition],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mockUseWorkflowRuns.mockReturnValue({
      runs: [{ run: liveRun, task_runs: [] }],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [routeEvent],
      streamCompletion: null,
      streamStatus: "connected",
      reconnectCount: 0,
      lastEventIndex: 1,
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      retryStream: vi.fn(),
    });
    mockUseWorkflowRunEventSnapshots.mockReturnValue({
      eventsByRunId: {},
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it("renders workflow runs and selected run events", () => {
    render(<WorkflowsPage />);

    expect(screen.getByTestId("workflows-page")).toBeInTheDocument();
    expect(screen.getByTestId("workflow-run-row-wrun-1")).toHaveTextContent(
      "route-runner",
    );
    expect(
      screen.getByTestId("workflow-event-workflow_route_admitted"),
    ).toHaveTextContent("Route admission accepted");
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
  });

  it("starts a selected workflow with JSON input and refetches runs", async () => {
    const refetchRuns = vi.fn();
    const startRun = vi.fn().mockResolvedValue({
      run: {
        ...liveRun,
        run_id: "wrun-new",
        status: "queued",
      },
    });
    mockUseStartWorkflowRun.mockReturnValue(startRun);
    mockUseWorkflowRuns.mockReturnValue({
      runs: [],
      isLoading: false,
      error: null,
      refetch: refetchRuns,
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [],
      streamCompletion: null,
      streamStatus: "idle",
      reconnectCount: 0,
      lastEventIndex: null,
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
      retryStream: vi.fn(),
    });

    render(<WorkflowsPage />);

    await waitFor(() =>
      expect(screen.getByRole("button", { name: "Start" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "Start" }));

    await waitFor(() =>
      expect(startRun).toHaveBeenCalledWith("route-runner", {
        input: {},
        once: true,
        wait: false,
      }),
    );
    expect(refetchRuns).toHaveBeenCalled();
  });

  it("cancels live workflow runs and refetches run details", async () => {
    const refetchRuns = vi.fn();
    const refetchEvents = vi.fn();
    const cancelRun = vi.fn().mockResolvedValue({
      ...liveRun,
      status: "cancelled",
    });
    mockUseCancelWorkflowRun.mockReturnValue(cancelRun);
    mockUseWorkflowRuns.mockReturnValue({
      runs: [{ run: liveRun }],
      isLoading: false,
      error: null,
      refetch: refetchRuns,
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [routeEvent],
      streamCompletion: null,
      streamStatus: "connected",
      reconnectCount: 0,
      lastEventIndex: 1,
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: refetchEvents,
      retryStream: vi.fn(),
    });
    mockUseWorkflowRunEventSnapshots.mockReturnValue({
      eventsByRunId: {
        "wrun-1": [routeEvent],
        "wrun-2": [waitingEvent],
      },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<WorkflowsPage />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(cancelRun).toHaveBeenCalledWith("wrun-1"));
    expect(refetchRuns).toHaveBeenCalled();
    expect(refetchEvents).toHaveBeenCalled();
  });

  it("shows stream reconnect state and retries the selected event stream", () => {
    const retryStream = vi.fn();
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [routeEvent],
      streamCompletion: null,
      streamStatus: "reconnecting",
      reconnectCount: 2,
      lastEventIndex: 1,
      isStreamComplete: false,
      isLoading: false,
      error: new Error("stream interrupted"),
      refetch: vi.fn(),
      retryStream,
    });

    render(<WorkflowsPage />);

    expect(screen.getByText("Reconnecting 2 #1")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Retry stream" }));

    expect(retryStream).toHaveBeenCalledTimes(1);
  });

  it("compares selected workflow runs and batch cancels live selections", async () => {
    const refetchRuns = vi.fn();
    const refetchEvents = vi.fn();
    const cancelRun = vi.fn().mockResolvedValue({
      ...liveRun,
      status: "cancelled",
    });
    mockUseCancelWorkflowRun.mockReturnValue(cancelRun);
    mockUseWorkflowRuns.mockReturnValue({
      runs: [{ run: liveRun }, { run: waitingRun }],
      isLoading: false,
      error: null,
      refetch: refetchRuns,
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [routeEvent],
      streamCompletion: null,
      streamStatus: "connected",
      reconnectCount: 0,
      lastEventIndex: 1,
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: refetchEvents,
      retryStream: vi.fn(),
    });
    mockUseWorkflowRunEventSnapshots.mockReturnValue({
      eventsByRunId: {
        "wrun-1": [routeEvent],
        "wrun-2": [waitingEvent],
      },
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<WorkflowsPage />);
    fireEvent.click(screen.getByLabelText("Compare run wrun-1"));
    fireEvent.click(screen.getByLabelText("Compare run wrun-2"));

    expect(screen.getByTestId("workflow-comparison-bar")).toHaveTextContent(
      "Comparing 2 runs",
    );
    expect(
      screen.getByTestId("workflow-comparison-run-wrun-1"),
    ).toHaveTextContent("Running");
    expect(
      screen.getByTestId("workflow-comparison-run-wrun-2"),
    ).toHaveTextContent("Waiting");
    expect(
      screen.getByTestId("workflow-comparison-timeline-wrun-1"),
    ).toHaveTextContent("workflow_route_admitted");
    expect(
      screen.getByTestId("workflow-comparison-timeline-wrun-2"),
    ).toHaveTextContent("workflow_waiting");
    expect(
      screen.getByTestId("workflow-comparison-event-wrun-2-2"),
    ).toHaveTextContent("Waiting for trigger follow-up");

    fireEvent.click(
      screen.getByRole("button", { name: "Cancel selected live" }),
    );

    await waitFor(() => expect(cancelRun).toHaveBeenCalledTimes(2));
    expect(cancelRun).toHaveBeenCalledWith("wrun-1");
    expect(cancelRun).toHaveBeenCalledWith("wrun-2");
    expect(refetchRuns).toHaveBeenCalled();
    expect(refetchEvents).toHaveBeenCalled();
  });
});
