/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";

import { api } from "@/api/common";

import { getTaskWorkflowRuns } from "../taskWorkflowRuns";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);

describe("getTaskWorkflowRuns", () => {
  beforeEach(() => vi.clearAllMocks());

  it("uses the exact workspace and task-scoped workflow run endpoint", async () => {
    const run = {
      workspace_key: "WS",
      run_id: "automation-run-1",
      driver_id: "prompt-agent",
      driver_version_id: "v1",
      status: "completed",
      summary:
        "Repository selection is required before an agent task can start.",
      output: { blocker: "repository_required", skipped: "true" },
      created_at: "2026-07-18T20:00:00Z",
      updated_at: "2026-07-18T20:00:01Z",
    };
    mockApiGet.mockResolvedValueOnce({
      data: { task_id: "TASK-10", subject_ref: "issue:TASK-10", runs: [run] },
      error: undefined,
      response: new Response(),
    } as never);

    await expect(getTaskWorkflowRuns("DESKTOP QA", "TASK-10")).resolves.toEqual(
      [run],
    );
    expect(mockApiGet).toHaveBeenCalledWith(
      "/api/workspaces/{ws}/tasks/{taskId}/workflow-runs",
      { params: { path: { ws: "DESKTOP QA", taskId: "TASK-10" } } },
    );
  });

  it("normalizes a missing runs array to empty", async () => {
    mockApiGet.mockResolvedValueOnce({
      data: { task_id: "TASK-10", subject_ref: "issue:TASK-10" },
      error: undefined,
      response: new Response(),
    } as never);
    await expect(getTaskWorkflowRuns("WS", "TASK-10")).resolves.toEqual([]);
  });
});
