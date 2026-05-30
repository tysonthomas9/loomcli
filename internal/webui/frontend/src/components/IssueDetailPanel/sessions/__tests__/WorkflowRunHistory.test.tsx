/**
 * @vitest-environment jsdom
 */

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import "@testing-library/jest-dom";

import {
  useCancelWorkflowRun,
  useStartWorkflowRun,
  useTaskWorkflowRuns,
  useWorkflowDefinitions,
  useWorkflowRunEvents,
} from "@/hooks/workflows";

import { WorkflowRunHistory } from "../WorkflowRunHistory";

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "WS" }),
  };
});

vi.mock("@/hooks/workflows", () => ({
  isWorkflowRunLive: (status: string) =>
    status === "queued" || status === "running" || status === "waiting",
  useCancelWorkflowRun: vi.fn(),
  useStartWorkflowRun: vi.fn(),
  useTaskWorkflowRuns: vi.fn(),
  useWorkflowDefinitions: vi.fn(),
  useWorkflowRunEvents: vi.fn(),
}));

const mockUseCancelWorkflowRun = vi.mocked(useCancelWorkflowRun);
const mockUseStartWorkflowRun = vi.mocked(useStartWorkflowRun);
const mockUseTaskWorkflowRuns = vi.mocked(useTaskWorkflowRuns);
const mockUseWorkflowDefinitions = vi.mocked(useWorkflowDefinitions);
const mockUseWorkflowRunEvents = vi.mocked(useWorkflowRunEvents);

describe("WorkflowRunHistory", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockUseCancelWorkflowRun.mockReturnValue(vi.fn());
    mockUseStartWorkflowRun.mockReturnValue(vi.fn());
    mockUseWorkflowDefinitions.mockReturnValue({
      definitions: [
        {
          workspace_key: "WS",
          name: "run-parent-work-items",
          version: "builtin-v1",
          status: "active",
          created_at: "2026-01-01T00:00:00Z",
          updated_at: "2026-01-01T00:00:00Z",
        },
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it("renders task-scoped workflow runs and event logs", () => {
    mockUseTaskWorkflowRuns.mockReturnValue({
      runs: [
        {
          run: {
            workspace_key: "WS",
            run_id: "wrun-1234567890",
            workflow_name: "epic-runner",
            workflow_version: "v1",
            status: "waiting",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
          task_runs: [
            {
              workspace_key: "WS",
              task_run_id: "trun-1",
              idempotency_key: "key",
              workflow_run_id: "wrun-1234567890",
              work_item_id: "TASK-1",
              role_name: "task",
              status: "queued",
              attempt: 1,
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            },
          ],
        },
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [
        {
          workspace_key: "WS",
          event_id: "evt-1",
          workflow_run_id: "wrun-1234567890",
          event_index: 1,
          type: "workflow_log",
          message: "ready children found",
          created_at: "2026-01-01T00:00:00Z",
        },
      ],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<WorkflowRunHistory taskId="TASK-1" />);

    expect(screen.getByTestId("workflow-run-history")).toBeInTheDocument();
    expect(screen.getAllByText("epic-runner")).toHaveLength(2);
    expect(screen.getByTestId("workflow-event-workflow_log")).toHaveTextContent(
      "ready children found",
    );
  });

  it("cancels live workflow runs and refetches", async () => {
    const refetchRuns = vi.fn();
    const refetchEvents = vi.fn();
    const cancelRun = vi.fn().mockResolvedValue({
      workspace_key: "WS",
      run_id: "wrun-1",
      workflow_name: "epic-runner",
      workflow_version: "v1",
      status: "cancelled",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    });
    mockUseCancelWorkflowRun.mockReturnValue(cancelRun);
    mockUseTaskWorkflowRuns.mockReturnValue({
      runs: [
        {
          run: {
            workspace_key: "WS",
            run_id: "wrun-1",
            workflow_name: "epic-runner",
            workflow_version: "v1",
            status: "waiting",
            created_at: "2026-01-01T00:00:00Z",
            updated_at: "2026-01-01T00:00:00Z",
          },
        },
      ],
      isLoading: false,
      error: null,
      refetch: refetchRuns,
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [],
      isLoading: false,
      error: null,
      refetch: refetchEvents,
    });

    render(<WorkflowRunHistory taskId="TASK-1" />);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    await waitFor(() => expect(cancelRun).toHaveBeenCalledWith("wrun-1"));
    expect(refetchRuns).toHaveBeenCalled();
    expect(refetchEvents).toHaveBeenCalled();
  });

  it("starts a workflow with task-scoped JSON input and refetches", async () => {
    const refetchRuns = vi.fn();
    const startRun = vi.fn().mockResolvedValue({
      run: {
        workspace_key: "WS",
        run_id: "wrun-new",
        workflow_name: "run-parent-work-items",
        workflow_version: "builtin-v1",
        status: "waiting",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
    });
    mockUseStartWorkflowRun.mockReturnValue(startRun);
    mockUseTaskWorkflowRuns.mockReturnValue({
      runs: [],
      isLoading: false,
      error: null,
      refetch: refetchRuns,
    });
    mockUseWorkflowRunEvents.mockReturnValue({
      events: [],
      isLoading: false,
      error: null,
      refetch: vi.fn(),
    });

    render(<WorkflowRunHistory taskId="EPIC-1" />);
    expect(screen.getByText("No workflow runs")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Start" }));

    await waitFor(() =>
      expect(startRun).toHaveBeenCalledWith("run-parent-work-items", {
        input: { parentId: "EPIC-1" },
        once: true,
        wait: false,
      }),
    );
    expect(refetchRuns).toHaveBeenCalled();
  });
});
