/**
 * @vitest-environment jsdom
 */

import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceData } from "../workspace";

vi.mock("@/api/common", () => ({
  api: { GET: vi.fn() },
  apiErrorFromResponse: vi.fn(),
  get: vi.fn(),
  post: vi.fn(),
  patch: vi.fn(),
  put: vi.fn(),
  del: vi.fn(),
  wsUrl: (workspaceId: string, path: string) =>
    `/api/workspaces/${encodeURIComponent(workspaceId)}${path}`,
  ApiError: class extends Error {},
}));

let addWorkspaceRepos: typeof import("../workspace").addWorkspaceRepos;
let mockPost: ReturnType<typeof vi.fn>;

describe("addWorkspaceRepos", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();

    const common = await import("@/api/common");
    mockPost = vi.mocked(common.post);
    ({ addWorkspaceRepos } = await import("../workspace"));
  });

  it("returns an async job for remote clone URLs without extending the request timeout", async () => {
    const request = { clone_urls: ["https://github.com/acme/example.git"] };
    mockPost.mockResolvedValueOnce({
      success: true,
      job_id: "add-repos-job-123",
    });

    await expect(addWorkspaceRepos("test workspace", request)).resolves.toEqual(
      {
        kind: "async",
        jobId: "add-repos-job-123",
      },
    );

    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/test%20workspace/repos",
      request,
    );
  });

  it("returns the workspace synchronously for local-path repositories", async () => {
    const request = { repos: ["/workspace/source-repo"] };
    const workspace = { id: "local" } as WorkspaceData;
    mockPost.mockResolvedValueOnce({ success: true, data: workspace });

    await expect(addWorkspaceRepos("local", request)).resolves.toEqual({
      kind: "sync",
      data: workspace,
    });

    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/local/repos",
      request,
    );
  });
});
