import { describe, it, expect, vi, beforeEach } from "vitest";

import { fetchDiffCommits, fetchDiffFiles, fetchDiffFile } from "../diff";
import { ApiError, get } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
  };
});

const mockGet = get as ReturnType<typeof vi.fn>;

describe("diff API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe("fetchDiffCommits", () => {
    it("returns commits array on success", async () => {
      const commits = [
        {
          hash: "abc123def456",
          short_hash: "abc123d",
          subject: "Fix bug",
          author: "Test User",
          email: "test@example.com",
          date: "2026-03-10T12:00:00Z",
        },
      ];
      mockGet.mockResolvedValue({
        success: true,
        data: { commits },
      });

      const result = await fetchDiffCommits("test-ws-id", "ember");

      expect(result).toEqual(commits);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/commits",
      );
    });

    it("passes limit query param when provided", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { commits: [] },
      });

      await fetchDiffCommits("test-ws-id", "ember", 10);

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/commits?limit=10",
      );
    });

    it("omits limit param when not provided", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { commits: [] },
      });

      await fetchDiffCommits("test-ws-id", "ember");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/commits",
      );
    });

    it("throws on API failure response", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "agent not found",
      });

      await expect(fetchDiffCommits("missing")).rejects.toThrow(ApiError);
      await expect(fetchDiffCommits("missing")).rejects.toThrow(
        "agent not found",
      );
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { commits: [] },
      });

      await fetchDiffCommits("test-ws-id", "agent/with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/agent%2Fwith%20spaces/diff/commits",
      );
    });
  });

  describe("fetchDiffFiles", () => {
    it("returns files array on success", async () => {
      const files = [
        { path: "main.go", status: "M" as const, additions: 5, deletions: 2 },
        { path: "new.go", status: "A" as const, additions: 30, deletions: 0 },
        {
          path: "renamed.go",
          status: "R" as const,
          old_path: "old_name.go",
          additions: 0,
          deletions: 0,
        },
      ];
      mockGet.mockResolvedValue({
        success: true,
        data: { files },
      });

      const result = await fetchDiffFiles(
        "test-ws-id",
        "ember",
        "def456",
        "abc123",
      );

      expect(result).toEqual(files);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/files?to=def456&from=abc123",
      );
    });

    it("omits from param for root commit", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { files: [] },
      });

      await fetchDiffFiles("test-ws-id", "ember", "def456");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/files?to=def456",
      );
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { files: [] },
      });

      await fetchDiffFiles("test-ws-id", "agent/special", "def", "abc");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/agent%2Fspecial/diff/files?to=def&from=abc",
      );
    });

    it("encodes from and to params", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { files: [] },
      });

      await fetchDiffFiles(
        "test-ws-id",
        "ember",
        "other&ref",
        "ref/with spaces",
      );

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/files?to=other%26ref&from=ref%2Fwith%20spaces",
      );
    });

    it("throws on API failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "invalid commit range",
      });

      await expect(fetchDiffFiles("ember", "range", "bad")).rejects.toThrow(
        ApiError,
      );
    });
  });

  describe("fetchDiffFile", () => {
    it("returns diff patch on success", async () => {
      const patch = {
        patch: "--- a/main.go\n+++ b/main.go\n@@ -1,3 +1,5 @@\n+new line",
        is_binary: false,
        is_too_large: false,
        additions: 3,
        deletions: 1,
      };
      mockGet.mockResolvedValue({
        success: true,
        data: patch,
      });

      const result = await fetchDiffFile(
        "test-ws-id",
        "ember",
        "main.go",
        "def456",
        "abc123",
      );

      expect(result).toEqual(patch);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/file?path=main.go&to=def456&from=abc123",
      );
    });

    it("omits from param for root commit", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          patch: "+new file content",
          is_binary: false,
          is_too_large: false,
          additions: 1,
          deletions: 0,
        },
      });

      await fetchDiffFile("test-ws-id", "ember", "main.go", "def456");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/file?path=main.go&to=def456",
      );
    });

    it("encodes path and hash params", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          patch: "",
          is_binary: false,
          is_too_large: false,
          additions: 0,
          deletions: 0,
        },
      });

      await fetchDiffFile(
        "test-ws-id",
        "ember",
        "src/path with spaces/file.go",
        "def=456",
        "abc&123",
      );

      expect(mockGet).toHaveBeenCalledWith(
        "/api/workspaces/test-ws-id/agents/ember/diff/file?path=src%2Fpath%20with%20spaces%2Ffile.go&to=def%3D456&from=abc%26123",
      );
    });

    it("returns binary flag correctly", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          patch: "",
          is_binary: true,
          is_too_large: false,
          additions: 0,
          deletions: 0,
        },
      });

      const result = await fetchDiffFile(
        "test-ws-id",
        "ember",
        "image.png",
        "def",
        "abc",
      );

      expect(result.is_binary).toBe(true);
      expect(result.patch).toBe("");
    });

    it("returns is_too_large flag correctly", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          patch: "",
          is_binary: false,
          is_too_large: true,
          additions: 0,
          deletions: 0,
        },
      });

      const result = await fetchDiffFile(
        "test-ws-id",
        "ember",
        "huge.sql",
        "def",
        "abc",
      );

      expect(result.is_too_large).toBe(true);
    });

    it("throws on API failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "file not found",
      });

      await expect(
        fetchDiffFile("ember", "missing.go", "def", "abc"),
      ).rejects.toThrow(ApiError);
      await expect(
        fetchDiffFile("ember", "missing.go", "def", "abc"),
      ).rejects.toThrow("file not found");
    });
  });
});
