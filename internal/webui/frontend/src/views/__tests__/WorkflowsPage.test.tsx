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
  useWorkflowRunEvents: vi.fn(),
  useWorkflowRuns: vi.fn(),
}));

const mockUseRouteView = vi.mocked(useRouteView);
const mockUseCancelWorkflowRun = vi.mocked(useCancelWorkflowRun);
const mockUseStartWorkflowRun = vi.mocked(useStartWorkflowRun);
const mockUseWorkflowDefinitions = vi.mocked(useWorkflowDefinitions);
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
      isStreamComplete: false,
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
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: vi.fn(),
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
      isStreamComplete: false,
      isLoading: false,
      error: null,
      refetch: refetchEvents,
    });

    render(<WorkflowsPage />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(cancelRun).toHaveBeenCalledWith("wrun-1"));
    expect(refetchRuns).toHaveBeenCalled();
    expect(refetchEvents).toHaveBeenCalled();
  });
});
