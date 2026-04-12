import { describe, it, expect, vi, beforeEach } from "vitest";

import { listWorktreeDir, readWorktreeFile, writeWorktreeFile } from "../files";
import { ApiError, get, put } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    get: vi.fn(),
    put: vi.fn(),
  };
});

const mockGet = get as ReturnType<typeof vi.fn>;
const mockPut = put as ReturnType<typeof vi.fn>;

describe("files API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
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
});
