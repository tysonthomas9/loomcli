/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, beforeEach, vi } from "vitest";

vi.mock("@/api/common", async () => {
  const actual =
    await vi.importActual<typeof import("@/api/common")>("@/api/common");
  return {
    ...actual,
    patch: vi.fn(),
    api: { PATCH: vi.fn() },
    apiErrorFromResponse: vi.fn((err) => new Error(String(err))),
    ApiError: class extends Error {
      status: number;
      statusText: string;
      constructor(status: number, statusText: string) {
        super(`API Error: ${status} ${statusText}`);
        this.status = status;
        this.statusText = statusText;
      }
    },
  };
});

let updateRepoDefaultBranch: typeof import("../workspaceConfig").updateRepoDefaultBranch;
let mockPatch: ReturnType<typeof vi.fn>;

describe("updateRepoDefaultBranch", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();
    const common = await import("@/api/common");
    mockPatch = common.patch as unknown as ReturnType<typeof vi.fn>;
    const mod = await import("../workspaceConfig");
    updateRepoDefaultBranch = mod.updateRepoDefaultBranch;
  });

  it("sends PATCH to the repo-scoped path with branch body", async () => {
    mockPatch.mockResolvedValueOnce({
      success: true,
      data: {
        name: "ws",
        path: "/tmp",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [],
        default_workspace: "",
      },
    });
    await updateRepoDefaultBranch("ws-uuid", "backend", "develop");
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/ws-uuid/repos/backend/default-branch",
      { branch: "develop" },
    );
  });

  it("URL-encodes workspace and repo name", async () => {
    mockPatch.mockResolvedValueOnce({
      success: true,
      data: {
        name: "ws",
        path: "/tmp",
        repos: [],
        groups: [],
        agents: [],
        workspaces: [],
        default_workspace: "",
      },
    });
    await updateRepoDefaultBranch("ws with space", "repo/name", "main");
    expect(mockPatch).toHaveBeenCalledWith(
      "/api/workspaces/ws%20with%20space/repos/repo%2Fname/default-branch",
      { branch: "main" },
    );
  });

  it("throws ApiError when response is not success", async () => {
    mockPatch.mockResolvedValueOnce({ success: false, error: "not found" });
    await expect(
      updateRepoDefaultBranch("ws", "backend", "develop"),
    ).rejects.toThrow(/not found/);
  });
});
