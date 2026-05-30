/**
 * @vitest-environment jsdom
 */

import { describe, expect, it, vi, beforeEach } from "vitest";

import { get, post } from "@/api/common";

import {
  cancelWorkflowRun,
  getWorkflowRunEvents,
  isWorkflowRunLive,
  listWorkflowRuns,
} from "../workflowRuns";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    post: vi.fn(),
  };
});

const mockGet = vi.mocked(get);
const mockPost = vi.mocked(post);

describe("workflow run API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists workflow runs with task-scoped query parameters", async () => {
    const item = {
      run: {
        workspace_key: "WS",
        run_id: "wrun-1",
        workflow_name: "epic-runner",
        workflow_version: "v1",
        status: "waiting",
        created_at: "2026-01-01T00:00:00Z",
        updated_at: "2026-01-01T00:00:00Z",
      },
      task_runs: [],
    };
    mockGet.mockResolvedValueOnce({ success: true, data: [item], total: 1 });

    const result = await listWorkflowRuns("WS", {
      workItemId: "TASK 1",
      status: "waiting",
      limit: 25,
    });

    expect(result).toEqual([item]);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/WS/workflow-runs?work_item_id=TASK+1&status=waiting&limit=25",
    );
  });

  it("fetches workflow run events", async () => {
    const event = {
      workspace_key: "WS",
      event_id: "evt-1",
      workflow_run_id: "wrun-1",
      event_index: 1,
      type: "workflow_log",
      created_at: "2026-01-01T00:00:00Z",
    };
    mockGet.mockResolvedValueOnce({ success: true, data: [event], total: 1 });

    const result = await getWorkflowRunEvents("WS", "wrun-1");

    expect(result).toEqual([event]);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/WS/workflow-runs/wrun-1/events",
    );
  });

  it("cancels workflow runs", async () => {
    const run = {
      workspace_key: "WS",
      run_id: "wrun-1",
      workflow_name: "epic-runner",
      workflow_version: "v1",
      status: "cancelled",
      created_at: "2026-01-01T00:00:00Z",
      updated_at: "2026-01-01T00:00:00Z",
    };
    mockPost.mockResolvedValueOnce(run);

    const result = await cancelWorkflowRun("WS", "wrun-1");

    expect(result).toEqual(run);
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/WS/workflow-runs/wrun-1/cancel",
      {},
    );
  });

  it("identifies live workflow run statuses", () => {
    expect(isWorkflowRunLive("queued")).toBe(true);
    expect(isWorkflowRunLive("running")).toBe(true);
    expect(isWorkflowRunLive("waiting")).toBe(true);
    expect(isWorkflowRunLive("completed")).toBe(false);
    expect(isWorkflowRunLive("failed")).toBe(false);
    expect(isWorkflowRunLive("cancelled")).toBe(false);
  });
});
