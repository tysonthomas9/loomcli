import { beforeEach, describe, expect, it, vi } from "vitest";

import { get } from "@/api/common";

import {
  buildWorkspaceSessionsUrl,
  getWorkspaceTraceRun,
  listWorkspaceSessions,
} from "../workspaceSessions";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return { ...actual, get: vi.fn() };
});

const mockGet = vi.mocked(get);

describe("workspace Traces API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("serializes run and repeatable AND tag filters", () => {
    const url = buildWorkspaceSessionsUrl("TEAM A", {
      task_run_id: "run-1",
      tags: ["security", "frontend"],
      kind: "judge",
      limit: 200,
    });

    expect(url).toBe(
      "/api/workspaces/TEAM%20A/sessions?task_run_id=run-1&tag=security&tag=frontend&kind=judge&limit=200",
    );
  });

  it("preserves score dimensions returned for the full filtered range", async () => {
    mockGet.mockResolvedValueOnce({
      success: true,
      data: {
        sessions: [],
        total: 3,
        limit: 2,
        score_dimensions: ["efficiency", "outcome_success"],
      },
    });

    await expect(listWorkspaceSessions("WS", { limit: 2 })).resolves.toEqual({
      sessions: [],
      total: 3,
      limit: 2,
      score_dimensions: ["efficiency", "outcome_success"],
    });
  });

  it("loads the backend-composed run view", async () => {
    const run = {
      task_run_id: "run/one",
      task_run_missing: false,
      attempt_count: 2,
      files_changed: 4,
      total_tokens: 1200,
      duration_seconds: 30,
      sessions: [],
    };
    mockGet.mockResolvedValueOnce({ success: true, data: run });

    await expect(getWorkspaceTraceRun("WS", "run/one")).resolves.toEqual(run);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/WS/traces/runs/run%2Fone",
    );
  });
});
