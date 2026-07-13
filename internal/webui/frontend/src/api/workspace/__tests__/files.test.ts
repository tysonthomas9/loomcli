import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  deleteScopedPath,
  getFileCapabilities,
  gitStatusScoped,
  indexScopedFiles,
  listScopedDir,
  listWorktreeDir,
  mkdirScoped,
  moveScopedPath,
  readScopedFile,
  readWorktreeFile,
  searchScopedFiles,
  writeScopedFile,
  writeWorktreeFile,
} from "../files";
import { ApiError, del, get, patch, post, put } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    del: vi.fn(),
    get: vi.fn(),
    patch: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  };
});

const mockDel = del as ReturnType<typeof vi.fn>;
const mockGet = get as ReturnType<typeof vi.fn>;
const mockPatch = patch as ReturnType<typeof vi.fn>;
const mockPost = post as ReturnType<typeof vi.fn>;
const mockPut = put as ReturnType<typeof vi.fn>;

describe("files API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("getFileCapabilities", () => {
    it("returns effective workspace file permissions", async () => {
      const data = { read: true, write: false, sensitive: false };
      mockGet.mockResolvedValue(data);

      await expect(getFileCapabilities("test-ws-id")).resolves.toEqual(data);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/capabilities",
      );
    });
  });

  // ============= listWorktreeDir =============

  describe("listWorktreeDir", () => {
    it("returns DirListData on success", async () => {
      const data = {
        path: ".",
        entries: [
          {
            name: "src",
            is_dir: true,
            size: 4096,
            mod_time: "2026-03-10T12:00:00Z",
          },
          {
            name: "main.go",
            is_dir: false,
            size: 1234,
            mod_time: "2026-03-10T11:00:00Z",
          },
        ],
      };
      mockGet.mockResolvedValue(data);

      const result = await listWorktreeDir("test-ws-id", "ember");

      expect(result).toEqual(data);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files/tree",
      );
    });

    it("passes path query param when provided", async () => {
      mockGet.mockResolvedValue({ path: "src", entries: [] });

      await listWorktreeDir("test-ws-id", "ember", "src");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files/tree?path=src",
      );
    });

    it("omits path param when not provided", async () => {
      mockGet.mockResolvedValue({ path: ".", entries: [] });

      await listWorktreeDir("test-ws-id", "ember");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files/tree",
      );
    });

    it("omits path param when empty string", async () => {
      mockGet.mockResolvedValue({ path: ".", entries: [] });

      await listWorktreeDir("test-ws-id", "ember", "");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files/tree",
      );
    });

    it("encodes agent name with special characters", async () => {
      mockGet.mockResolvedValue({ path: ".", entries: [] });

      await listWorktreeDir("test-ws-id", "agent/with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/agent%2Fwith%20spaces/files/tree",
      );
    });

    it("encodes path with special characters", async () => {
      mockGet.mockResolvedValue({ path: "src/my dir", entries: [] });

      await listWorktreeDir("test-ws-id", "ember", "src/my dir");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files/tree?path=src%2Fmy%20dir",
      );
    });

    it("throws ApiError on HTTP error", async () => {
      mockGet.mockRejectedValue(new ApiError(404, "Not Found"));

      await expect(listWorktreeDir("missing")).rejects.toThrow(ApiError);
    });
  });

  // ============= readWorktreeFile =============

  describe("readWorktreeFile", () => {
    it("returns FileReadData with content for text files", async () => {
      const data = {
        path: "main.go",
        content: "package main\n",
        size: 13,
        binary: false,
      };
      mockGet.mockResolvedValue(data);

      const result = await readWorktreeFile("test-ws-id", "ember", "main.go");

      expect(result).toEqual(data);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files?path=main.go",
      );
    });

    it("returns FileReadData with binary flag and no content", async () => {
      const data = {
        path: "image.png",
        size: 50000,
        binary: true,
      };
      mockGet.mockResolvedValue(data);

      const result = await readWorktreeFile("test-ws-id", "ember", "image.png");

      expect(result.binary).toBe(true);
      expect(result.content).toBeUndefined();
    });

    it("encodes path in URL", async () => {
      mockGet.mockResolvedValue({
        path: "src/my file.go",
        content: "",
        size: 0,
        binary: false,
      });

      await readWorktreeFile("test-ws-id", "ember", "src/my file.go");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files?path=src%2Fmy%20file.go",
      );
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({
        path: "main.go",
        content: "",
        size: 0,
        binary: false,
      });

      await readWorktreeFile("test-ws-id", "agent/special", "main.go");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/agent%2Fspecial/files?path=main.go",
      );
    });

    it("throws ApiError on HTTP error", async () => {
      mockGet.mockRejectedValue(new ApiError(403, "Forbidden"));

      await expect(readWorktreeFile("ember", "secret.key")).rejects.toThrow(
        ApiError,
      );
    });
  });

  // ============= writeWorktreeFile =============

  describe("writeWorktreeFile", () => {
    it("calls put with correct URL and JSON body", async () => {
      mockPut.mockResolvedValue({ success: true });

      await writeWorktreeFile(
        "test-ws-id",
        "ember",
        "main.go",
        "package main\n",
      );

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/files?path=main.go",
        { content: "package main\n" },
      );
    });

    it("returns void on success", async () => {
      mockPut.mockResolvedValue({ success: true });

      const result = await writeWorktreeFile(
        "test-ws-id",
        "ember",
        "test.txt",
        "hello",
      );

      expect(result).toBeUndefined();
    });

    it("throws ApiError on HTTP error", async () => {
      mockPut.mockRejectedValue(new ApiError(403, "Forbidden"));

      await expect(
        writeWorktreeFile("ember", "secret.env", "data"),
      ).rejects.toThrow(ApiError);
    });

    it("encodes agent name and path in URL", async () => {
      mockPut.mockResolvedValue({ success: true });

      await writeWorktreeFile(
        "test-ws-id",
        "agent/special",
        "src/my file.ts",
        "content",
      );

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/agent%2Fspecial/files?path=src%2Fmy%20file.ts",
        { content: "content" },
      );
    });
  });

  describe("scoped file API", () => {
    it("lists workspace scope without a target", async () => {
      mockGet.mockResolvedValue({ path: ".", entries: [] });

      await listScopedDir("test-ws-id", { scope: "workspace" });

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/tree?scope=workspace",
      );
    });

    it("lists repo scope with target and path", async () => {
      mockGet.mockResolvedValue({ path: "src", entries: [] });

      await listScopedDir(
        "test-ws-id",
        { scope: "repo", target: "loom/cli" },
        "src/my dir",
      );

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/tree?scope=repo&target=loom%2Fcli&path=src%2Fmy%20dir",
      );
    });

    it("reads agent scope with target", async () => {
      mockGet.mockResolvedValue({
        path: "main.go",
        content: "package main\n",
        size: 13,
        binary: false,
      });

      await readScopedFile(
        "test-ws-id",
        { scope: "agent", target: "atlas" },
        "main.go",
      );

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files?scope=agent&target=atlas&path=main.go",
      );
    });

    it("writes scoped file content", async () => {
      mockPut.mockResolvedValue({ success: true });

      await writeScopedFile(
        "test-ws-id",
        { scope: "repo", target: "loomcli" },
        ".env",
        "A=1",
      );

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files?scope=repo&target=loomcli&path=.env",
        { content: "A=1" },
      );
    });

    it("deletes recursively when requested", async () => {
      mockDel.mockResolvedValue({ success: true });

      await deleteScopedPath(
        "test-ws-id",
        { scope: "agent", target: "nova" },
        "dir",
        true,
      );

      expect(mockDel).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files?scope=agent&target=nova&path=dir&recursive=1",
      );
    });

    it("creates directories with scoped mkdir", async () => {
      mockPost.mockResolvedValue({ success: true });

      await mkdirScoped("test-ws-id", { scope: "workspace" }, "new/folder");

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/mkdir?scope=workspace&path=new%2Ffolder",
        undefined,
      );
    });

    it("moves paths with scoped move body", async () => {
      mockPatch.mockResolvedValue({ success: true });

      await moveScopedPath(
        "test-ws-id",
        { scope: "repo", target: "loomcli" },
        "old.txt",
        "new.txt",
        true,
      );

      expect(mockPatch).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/move?scope=repo&target=loomcli",
        { from: "old.txt", to: "new.txt", overwrite: true },
      );
    });

    it("indexes scoped files", async () => {
      mockGet.mockResolvedValue({ paths: ["src/main.go"], truncated: false });

      await indexScopedFiles("test-ws-id", {
        scope: "agent",
        target: "atlas",
      });

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/index?scope=agent&target=atlas",
      );
    });

    it("fetches scoped git status decorations", async () => {
      mockGet.mockResolvedValue({ "src/main.go": " M" });

      await gitStatusScoped("test-ws-id", {
        scope: "repo",
        target: "loomcli",
      });

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/git-status?scope=repo&target=loomcli",
      );
    });

    it("searches scoped files with options", async () => {
      mockPost.mockResolvedValue({ results: [], limitHit: true });

      await searchScopedFiles(
        "test-ws-id",
        { scope: "repo", target: "loomcli" },
        {
          query: "needle",
          regex: true,
          include: ["src/*.go"],
          exclude: [],
          caseSensitive: true,
        },
      );

      expect(mockPost).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/search?scope=repo&target=loomcli",
        {
          query: "needle",
          regex: true,
          include: ["src/*.go"],
          exclude: [],
          caseSensitive: true,
        },
      );
    });
  });
});
