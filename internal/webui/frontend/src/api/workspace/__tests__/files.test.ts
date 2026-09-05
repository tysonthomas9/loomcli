import { describe, it, expect, vi, beforeEach } from "vitest";

import {
  deleteScopedPath,
  getFileCapabilities,
  gitStatusScoped,
  indexScopedFiles,
  listScopedDir,
  mkdirScoped,
  moveScopedPath,
  readScopedFile,
  searchScopedFiles,
  statScopedPath,
  writeScopedFile,
} from "../files";
import { del, get, patch, post, put } from "@/api/common";

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

  describe("scoped file API", () => {
    it("lists workspace scope without a target", async () => {
      mockGet.mockResolvedValue({ path: ".", entries: [] });

      await listScopedDir("test-ws-id", { scope: "workspace" });

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/tree?scope=workspace",
      );
    });

    it("lists repo scope with target and path", async () => {
      mockGet.mockResolvedValue({ path: "src/my dir", entries: [] });

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
        truncated: false,
        version: "opaque",
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

    it("forwards explicit write preconditions", async () => {
      mockPut.mockResolvedValue({ success: true, version: "sha256:new" });

      await writeScopedFile(
        "test-ws-id",
        { scope: "workspace" },
        "new.txt",
        "body",
        { ifMatch: "sha256:old" },
      );

      expect(mockPut).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files?scope=workspace&path=new.txt",
        { content: "body" },
        { headers: { "If-Match": '"sha256:old"' } },
      );
    });

    it("stats a scoped path for its mutation version", async () => {
      mockGet.mockResolvedValue({
        path: "dir",
        is_dir: true,
        version: "dir-sha256:x",
      });

      await statScopedPath("test-ws-id", { scope: "workspace" }, "dir");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/stat?scope=workspace&path=dir",
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

    it("sends the source version when deleting", async () => {
      mockDel.mockResolvedValue({ success: true });

      await deleteScopedPath(
        "test-ws-id",
        { scope: "workspace" },
        "file.txt",
        false,
        "sha256:current",
      );

      expect(mockDel).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files?scope=workspace&path=file.txt",
        { headers: { "If-Match": '"sha256:current"' } },
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

    it("sends source and destination versions when moving", async () => {
      mockPatch.mockResolvedValue({ success: true, version: "sha256:source" });

      await moveScopedPath(
        "test-ws-id",
        { scope: "workspace" },
        "old.txt",
        "new.txt",
        true,
        "sha256:source",
        "sha256:destination",
      );

      expect(mockPatch).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/files/move?scope=workspace",
        {
          from: "old.txt",
          to: "new.txt",
          overwrite: true,
          source_version: "sha256:source",
          destination_version: "sha256:destination",
        },
      );
    });

    it("indexes scoped files", async () => {
      const data = {
        paths: ["src/main.go"],
        truncated: true,
        partial_reasons: ["file_count"],
      };
      mockGet.mockResolvedValue(data);

      await expect(
        indexScopedFiles("test-ws-id", {
          scope: "agent",
          target: "atlas",
        }),
      ).resolves.toEqual(data);

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
      const data = {
        results: [],
        limitHit: true,
        partial_reasons: ["result_count"],
      };
      mockPost.mockResolvedValue(data);

      await expect(
        searchScopedFiles(
          "test-ws-id",
          { scope: "repo", target: "loomcli" },
          {
            query: "needle",
            regex: true,
            include: ["src/*.go"],
            exclude: [],
            caseSensitive: true,
          },
        ),
      ).resolves.toEqual(data);

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
