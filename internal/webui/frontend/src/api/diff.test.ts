import { describe, it, expect, vi, beforeEach } from "vitest";

import { fetchDiffCommits, fetchDiffFiles, fetchDiffFile } from "./diff";
import { ApiError, get } from "./client";

vi.mock("./client", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./client")>();
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
          author_email: "test@example.com",
          date: "2026-03-10T12:00:00Z",
        },
      ];
      mockGet.mockResolvedValue({
        success: true,
        data: { commits },
      });

      const result = await fetchDiffCommits("ember");

      expect(result).toEqual(commits);
      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/diff/commits");
    });

    it("passes limit query param when provided", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { commits: [] },
      });

      await fetchDiffCommits("ember", 10);

      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/ember/diff/commits?limit=10",
      );
    });

    it("omits limit param when not provided", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { commits: [] },
      });

      await fetchDiffCommits("ember");

      expect(mockGet).toHaveBeenCalledWith("/api/agents/ember/diff/commits");
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

      await fetchDiffCommits("agent/with spaces");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/agent%2Fwith%20spaces/diff/commits",
      );
    });
  });

  describe("fetchDiffFiles", () => {
    it("returns files array on success", async () => {
      const files = [
        { path: "main.go", status: "M" as const },
        { path: "new.go", status: "A" as const },
        {
          path: "renamed.go",
          status: "R" as const,
          old_path: "old_name.go",
        },
      ];
      mockGet.mockResolvedValue({
        success: true,
        data: { files },
      });

      const result = await fetchDiffFiles("ember", "abc123", "def456");

      expect(result).toEqual(files);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/ember/diff/files?from=abc123&to=def456",
      );
    });

    it("encodes agent name in URL", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { files: [] },
      });

      await fetchDiffFiles("agent/special", "abc", "def");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/agent%2Fspecial/diff/files?from=abc&to=def",
      );
    });

    it("encodes from and to params", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: { files: [] },
      });

      await fetchDiffFiles("ember", "ref/with spaces", "other&ref");

      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/ember/diff/files?from=ref%2Fwith%20spaces&to=other%26ref",
      );
    });

    it("throws on API failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "invalid commit range",
      });

      await expect(fetchDiffFiles("ember", "bad", "range")).rejects.toThrow(
        ApiError,
      );
    });
  });

  describe("fetchDiffFile", () => {
    it("returns file content on success", async () => {
      const content = {
        old_content: "old code",
        new_content: "new code",
        is_binary: false,
        too_large: false,
      };
      mockGet.mockResolvedValue({
        success: true,
        data: content,
      });

      const result = await fetchDiffFile(
        "ember",
        "main.go",
        "abc123",
        "def456",
      );

      expect(result).toEqual(content);
      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/ember/diff/file?path=main.go&from=abc123&to=def456",
      );
    });

    it("encodes path and hash params", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          old_content: "",
          new_content: "",
          is_binary: false,
          too_large: false,
        },
      });

      await fetchDiffFile(
        "ember",
        "src/path with spaces/file.go",
        "abc&123",
        "def=456",
      );

      expect(mockGet).toHaveBeenCalledWith(
        "/api/agents/ember/diff/file?path=src%2Fpath%20with%20spaces%2Ffile.go&from=abc%26123&to=def%3D456",
      );
    });

    it("returns binary flag correctly", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          old_content: "",
          new_content: "",
          is_binary: true,
          too_large: false,
        },
      });

      const result = await fetchDiffFile("ember", "image.png", "abc", "def");

      expect(result.is_binary).toBe(true);
      expect(result.old_content).toBe("");
      expect(result.new_content).toBe("");
    });

    it("returns too_large flag correctly", async () => {
      mockGet.mockResolvedValue({
        success: true,
        data: {
          old_content: "",
          new_content: "",
          is_binary: false,
          too_large: true,
        },
      });

      const result = await fetchDiffFile("ember", "huge.sql", "abc", "def");

      expect(result.too_large).toBe(true);
    });

    it("throws on API failure", async () => {
      mockGet.mockResolvedValue({
        success: false,
        error: "file not found",
      });

      await expect(
        fetchDiffFile("ember", "missing.go", "abc", "def"),
      ).rejects.toThrow(ApiError);
      await expect(
        fetchDiffFile("ember", "missing.go", "abc", "def"),
      ).rejects.toThrow("file not found");
    });
  });
});
