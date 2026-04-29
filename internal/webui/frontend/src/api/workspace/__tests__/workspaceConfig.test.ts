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
    post: vi.fn(),
    del: vi.fn(),
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
let addRepoToWorkspace: typeof import("../workspaceConfig").addRepoToWorkspace;
let removeRepoFromWorkspace: typeof import("../workspaceConfig").removeRepoFromWorkspace;
let mockPatch: ReturnType<typeof vi.fn>;
let mockPost: ReturnType<typeof vi.fn>;
let mockDel: ReturnType<typeof vi.fn>;

describe("updateRepoDefaultBranch", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();
    const common = await import("@/api/common");
    mockPatch = common.patch as unknown as ReturnType<typeof vi.fn>;
    mockPost = common.post as unknown as ReturnType<typeof vi.fn>;
    mockDel = common.del as unknown as ReturnType<typeof vi.fn>;
    const mod = await import("../workspaceConfig");
    updateRepoDefaultBranch = mod.updateRepoDefaultBranch;
    addRepoToWorkspace = mod.addRepoToWorkspace;
    removeRepoFromWorkspace = mod.removeRepoFromWorkspace;
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

describe("addRepoToWorkspace", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();
    const common = await import("@/api/common");
    mockPost = common.post as unknown as ReturnType<typeof vi.fn>;
    const mod = await import("../workspaceConfig");
    addRepoToWorkspace = mod.addRepoToWorkspace;
  });

  it("sends POST with full repo body", async () => {
    mockPost.mockResolvedValueOnce({
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
    await addRepoToWorkspace("ws-uuid", {
      name: "backend",
      path: "/abs/path",
      default_branch: "main",
      remote: "origin",
      groups: ["svc"],
    });
    expect(mockPost).toHaveBeenCalledWith("/api/workspaces/ws-uuid/repos", {
      name: "backend",
      path: "/abs/path",
      default_branch: "main",
      remote: "origin",
      groups: ["svc"],
    });
  });

  it("URL-encodes workspace id", async () => {
    mockPost.mockResolvedValueOnce({
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
    await addRepoToWorkspace("ws with space", { path: "/p" });
    expect(mockPost).toHaveBeenCalledWith(
      "/api/workspaces/ws%20with%20space/repos",
      { path: "/p" },
    );
  });

  it("throws ApiError when response is not success", async () => {
    mockPost.mockResolvedValueOnce({ success: false, error: "duplicate" });
    await expect(addRepoToWorkspace("ws", { path: "/p" })).rejects.toThrow(
      /duplicate/,
    );
  });
});

describe("removeRepoFromWorkspace", () => {
  beforeEach(async () => {
    vi.clearAllMocks();
    vi.resetModules();
    const common = await import("@/api/common");
    mockDel = common.del as unknown as ReturnType<typeof vi.fn>;
    const mod = await import("../workspaceConfig");
    removeRepoFromWorkspace = mod.removeRepoFromWorkspace;
  });

  it("sends DELETE to repo-scoped path", async () => {
    mockDel.mockResolvedValueOnce({
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
    await removeRepoFromWorkspace("ws-uuid", "backend");
    expect(mockDel).toHaveBeenCalledWith(
      "/api/workspaces/ws-uuid/repos/backend",
    );
  });

  it("URL-encodes workspace id and repo name", async () => {
    mockDel.mockResolvedValueOnce({
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
    await removeRepoFromWorkspace("ws with space", "repo/name");
    expect(mockDel).toHaveBeenCalledWith(
      "/api/workspaces/ws%20with%20space/repos/repo%2Fname",
    );
  });

  it("throws ApiError when response is not success", async () => {
    mockDel.mockResolvedValueOnce({ success: false, error: "not found" });
    await expect(removeRepoFromWorkspace("ws", "x")).rejects.toThrow(
      /not found/,
    );
  });
});
