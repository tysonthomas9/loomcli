/**
 * @vitest-environment jsdom
 */

import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  getWorkflowRunEvents,
  type WorkflowRun,
  type WorkflowRunEvent,
} from "@/api/workflows";

import { useWorkflowRunEventSnapshots } from "../useWorkflowRunEventSnapshots";

vi.mock("@/hooks/workspace", () => ({
  useWorkspaceContext: () => ({ workspaceId: "WS" }),
}));

vi.mock("@/api/workflows", () => ({
  getWorkflowRunEvents: vi.fn(),
  isWorkflowRunLive: (status: string) =>
    status === "queued" || status === "running" || status === "waiting",
}));

const mockGetWorkflowRunEvents = vi.mocked(getWorkflowRunEvents);

const runningRun: WorkflowRun = {
  workspace_key: "WS",
  run_id: "wrun-1",
  workflow_name: "route-runner",
  workflow_version: "v1",
  status: "running",
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

const completedRun: WorkflowRun = {
  ...runningRun,
  run_id: "wrun-2",
  status: "completed",
};

const workflowEvent: WorkflowRunEvent = {
  workspace_key: "WS",
  event_id: "evt-1",
  workflow_run_id: "wrun-1",
  event_index: 1,
  type: "workflow_started",
  created_at: "2026-01-01T00:00:00Z",
};

describe("useWorkflowRunEventSnapshots", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("fetches compared run events keyed by run ID", async () => {
    mockGetWorkflowRunEvents.mockImplementation(async (_workspaceId, runId) => [
      { ...workflowEvent, workflow_run_id: runId, event_id: `evt-${runId}` },
    ]);

    const { result } = renderHook(() =>
      useWorkflowRunEventSnapshots([runningRun, completedRun]),
    );

    await waitFor(() =>
      expect(Object.keys(result.current.eventsByRunId)).toEqual([
        "wrun-1",
        "wrun-2",
      ]),
    );
    expect(mockGetWorkflowRunEvents).toHaveBeenCalledWith("WS", "wrun-1");
    expect(mockGetWorkflowRunEvents).toHaveBeenCalledWith("WS", "wrun-2");
    expect(result.current.eventsByRunId["wrun-1"]?.[0]?.event_id).toBe(
      "evt-wrun-1",
    );
    expect(result.current.error).toBeNull();
  });

  it("keeps successful snapshots when another run fails", async () => {
    mockGetWorkflowRunEvents.mockImplementation(async (_workspaceId, runId) => {
      if (runId === "wrun-2") throw new Error("missing run");
      return [workflowEvent];
    });

    const { result } = renderHook(() =>
      useWorkflowRunEventSnapshots([runningRun, completedRun]),
    );

    await waitFor(() =>
      expect(result.current.eventsByRunId["wrun-1"]).toEqual([workflowEvent]),
    );
    expect(result.current.eventsByRunId["wrun-2"]).toBeUndefined();
    expect(result.current.error?.message).toContain("missing run");
  });
});
