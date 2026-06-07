import { beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, post } from "@/api/common";

import { EPIC_RUNNER_WORKFLOW_NAME, startWorkflowRun } from "../workflows";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    post: vi.fn(),
  };
});

const mockPost = vi.mocked(post);

describe("workflows API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("posts payload to the workspace workflow run endpoint", async () => {
    const run = {
      workspace_key: "DESKTOP QA",
      run_id: "run-1",
      driver_id: "driver-1",
      driver_version_id: "version-1",
      status: "queued",
      created_at: "2026-01-23T00:00:00Z",
      updated_at: "2026-01-23T00:00:00Z",
    };
    mockPost.mockResolvedValueOnce(run);

    const result = await startWorkflowRun(
      "DESKTOP QA",
      EPIC_RUNNER_WORKFLOW_NAME,
      {
        epicId: "epic-1",
        requestedBy: "ui",
      },
    );

    expect(result).toEqual(run);
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/DESKTOP%20QA/workflows/epic-runner",
      {
        epicId: "epic-1",
        requestedBy: "ui",
      },
    );
  });

  it("throws when workflow run creation fails", async () => {
    mockPost.mockRejectedValueOnce(new ApiError(404, "Not Found"));

    await expect(
      startWorkflowRun("DESKTOP", "missing", { epicId: "epic-1" }),
    ).rejects.toThrow(ApiError);
  });
});
