import { beforeEach, describe, expect, it, vi } from "vitest";

import { get, post } from "@/api/common";

import {
  activateWorkflowVersion,
  approveWorkflowVersion,
  createWorkflowVersion,
  listWorkflowVersions,
  listWorkflows,
  rollbackWorkflow,
  syncBuiltinWorkflow,
  unapproveWorkflowVersion,
} from "../versions";

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

describe("workflow versions API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("lists workflows and unwraps the envelope", async () => {
    mockGet.mockResolvedValueOnce({ workflows: [{ driver_id: "epic-runner" }] });
    const result = await listWorkflows("DESKTOP QA");
    expect(result).toEqual([{ driver_id: "epic-runner" }]);
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/DESKTOP%20QA/workflows",
    );
  });

  it("returns [] when the workflows envelope is empty", async () => {
    mockGet.mockResolvedValueOnce({});
    expect(await listWorkflows("W")).toEqual([]);
  });

  it("lists versions for a workflow", async () => {
    mockGet.mockResolvedValueOnce({ driver_id: "epic-runner", versions: [] });
    await listWorkflowVersions("W", "epic-runner");
    expect(mockGet).toHaveBeenCalledWith(
      "/api/workspaces/W/workflows/epic-runner/versions",
    );
  });

  it("creates (authors) a version, defaulting activate off", async () => {
    mockPost.mockResolvedValueOnce({});
    await createWorkflowVersion("W", "my-flow", {
      files: { "workflows/my-flow.ts": "export {}" },
    });
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/W/workflows/my-flow/versions",
      { files: { "workflows/my-flow.ts": "export {}" } },
    );
  });

  it("approves and unapproves a version", async () => {
    mockPost.mockResolvedValue({});
    await approveWorkflowVersion("W", "epic-runner", "v/1");
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/W/workflows/epic-runner/versions/v%2F1/approve",
      {},
    );
    await unapproveWorkflowVersion("W", "epic-runner", "v1");
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/W/workflows/epic-runner/versions/v1/unapprove",
      {},
    );
  });

  it("activates a version, sending track only when provided", async () => {
    mockPost.mockResolvedValue({});
    await activateWorkflowVersion("W", "epic-runner", "v1");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/versions/v1/activate",
      {},
    );
    await activateWorkflowVersion("W", "epic-runner", "v1", "auto");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/versions/v1/activate",
      { track: "auto" },
    );
  });

  it("rolls back with and without an explicit target", async () => {
    mockPost.mockResolvedValue({});
    await rollbackWorkflow("W", "epic-runner");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/rollback",
      {},
    );
    await rollbackWorkflow("W", "epic-runner", "v1");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/rollback",
      { version_id: "v1" },
    );
  });

  it("syncs a built-in, sending track only when provided", async () => {
    mockPost.mockResolvedValue({});
    await syncBuiltinWorkflow("W", "epic-runner");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/builtin/sync",
      {},
    );
    await syncBuiltinWorkflow("W", "epic-runner", "auto");
    expect(mockPost).toHaveBeenLastCalledWith(
      "/api/workspaces/W/workflows/epic-runner/builtin/sync",
      { track: "auto" },
    );
  });
});
