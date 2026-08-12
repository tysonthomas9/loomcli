import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ApiError, get, post } from "@/api/common";

import {
  EPIC_RUNNER_WORKFLOW_NAME,
  activateWorkflowVersion,
  approveWorkflowVersion,
  createWorkflowVersion,
  getWorkflowRun,
  isTerminalWorkflowRunStatus,
  listWorkflowVersions,
  startWorkflowRun,
  unapproveWorkflowVersion,
} from "../workflows";
import {
  clearLocalWorkflowLifecycleSession,
  exchangeLocalOperatorLaunch,
} from "../localOperatorSession";

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

describe("workflows API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    clearLocalWorkflowLifecycleSession();
  });

  afterEach(() => {
    clearLocalWorkflowLifecycleSession();
    vi.restoreAllMocks();
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

  it("gets a workflow run by id", async () => {
    const run = {
      workspace_key: "DESKTOP QA",
      run_id: "run/1",
      driver_id: "driver-1",
      driver_version_id: "version-1",
      status: "running",
      created_at: "2026-01-23T00:00:00Z",
      updated_at: "2026-01-23T00:00:00Z",
    };
    mockGet.mockResolvedValueOnce(run);

    const result = await getWorkflowRun("DESKTOP QA", "run/1");

    expect(result).toEqual(run);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/DESKTOP%20QA/runs/run%2F1",
    );
  });

  it("identifies terminal workflow run statuses", () => {
    expect(isTerminalWorkflowRunStatus("completed")).toBe(true);
    expect(isTerminalWorkflowRunStatus("failed")).toBe(true);
    expect(isTerminalWorkflowRunStatus("needs_review")).toBe(true);
    expect(isTerminalWorkflowRunStatus("cancelled")).toBe(true);
    expect(isTerminalWorkflowRunStatus("queued")).toBe(false);
    expect(isTerminalWorkflowRunStatus("running")).toBe(false);
  });

  it("attaches a local Desktop bearer only to lifecycle mutations", async () => {
    const launchCode = "ab".repeat(32);
    const accessToken = "cd".repeat(32);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          access_token: accessToken,
          token_type: "Bearer",
          workspace: "TEST",
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await exchangeLocalOperatorLaunch({ launchCode, workspace: "TEST" });
    mockPost.mockResolvedValue({});
    mockGet.mockResolvedValue({ versions: [] });

    await approveWorkflowVersion("TEST", "demo", "v1");
    await unapproveWorkflowVersion("TEST", "demo", "v1");
    await activateWorkflowVersion("TEST", "demo", "v1");
    await listWorkflowVersions("TEST", "demo");
    await createWorkflowVersion("TEST", "demo", {
      files: { "workflow.ts": "export default {};" },
      entrypoint: "workflow.ts",
      activate: false,
    });

    const localAuth = { headers: { Authorization: `Bearer ${accessToken}` } };
    expect(mockPost).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/TEST/workflows/demo/versions/v1/approve",
      {},
      localAuth,
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/TEST/workflows/demo/versions/v1/unapprove",
      {},
      localAuth,
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      3,
      "/api/workspaces/TEST/workflows/demo/versions/v1/activate",
      {},
      localAuth,
    );
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/TEST/workflows/demo/versions",
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      4,
      "/api/workspaces/TEST/workflows/demo/versions",
      expect.any(Object),
      { timeout: 300_000 },
    );
  });

  it("does not attach or retain a Desktop bearer across workspaces", async () => {
    const launchCode = "ab".repeat(32);
    const accessToken = "cd".repeat(32);
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(
        JSON.stringify({
          access_token: accessToken,
          token_type: "Bearer",
          workspace: "TEST",
          expires_at: new Date(Date.now() + 60_000).toISOString(),
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    await exchangeLocalOperatorLaunch({ launchCode, workspace: "TEST" });
    mockPost.mockResolvedValue({});

    await approveWorkflowVersion("OTHER", "demo", "v1");
    await approveWorkflowVersion("TEST", "demo", "v1");

    expect(mockPost).toHaveBeenNthCalledWith(
      1,
      "/api/workspaces/OTHER/workflows/demo/versions/v1/approve",
      {},
      undefined,
    );
    expect(mockPost).toHaveBeenNthCalledWith(
      2,
      "/api/workspaces/TEST/workflows/demo/versions/v1/approve",
      {},
      undefined,
    );
  });
});
