import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { getTaskLogPhases, getTaskLogContent } from "../logs";
import { ApiError, api } from "@/api/common";

vi.mock("@/api/common", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/api/common")>();
  return {
    ...actual,
    api: {
      GET: vi.fn(),
      POST: vi.fn(),
      PATCH: vi.fn(),
      PUT: vi.fn(),
      DELETE: vi.fn(),
      use: vi.fn(),
    },
  };
});

const mockApiGet = vi.mocked(api.GET);

describe("logs API", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("getTaskLogPhases", () => {
    it("returns phases on successful response", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { phases: ["planning", "implementation"] },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const result = await getTaskLogPhases("test-ws-id", "issue-abc");

      expect(result).toEqual(["planning", "implementation"]);
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{id}/logs",
        {
          params: { path: { ws: "test-ws-id", id: "issue-abc" } },
        },
      );
    });

    it("returns empty array on 404", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const result = await getTaskLogPhases("test-ws-id", "nonexistent");

      expect(result).toEqual([]);
    });

    it("throws on non-404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(getTaskLogPhases("test-ws-id", "issue-abc")).rejects.toThrow(
        ApiError,
      );
    });
  });

  describe("getTaskLogContent", () => {
    it("returns log snapshot content on success", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { lines: ["a", "b"], line_count: 2 },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "issue-abc",
        "planning",
        25,
      );
      expect(content).toEqual({ lines: ["a", "b"], lineCount: 2 });
      expect(mockApiGet).toHaveBeenCalledWith(
        "/api/workspaces/{ws}/tasks/{id}/logs/{phase}",
        {
          params: {
            path: { ws: "test-ws-id", id: "issue-abc", phase: "planning" },
            query: { lines: 25 },
          },
        },
      );
    });

    it("returns empty content for 404 responses", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Not Found" },
        response: new Response(null, { status: 404, statusText: "Not Found" }),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "missing",
        "implementation",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });

    it("throws on non-404 error", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: undefined,
        error: { error: "Internal Server Error" },
        response: new Response(null, {
          status: 500,
          statusText: "Internal Server Error",
        }),
      } as never);

      await expect(
        getTaskLogContent("test-ws-id", "issue-abc", "planning"),
      ).rejects.toThrow(ApiError);
    });

    it("normalizes missing data fields", async () => {
      mockApiGet.mockResolvedValueOnce({
        data: {
          data: { lines: null, line_count: null },
        },
        error: undefined,
        response: new Response(),
      } as never);

      const content = await getTaskLogContent(
        "test-ws-id",
        "issue-abc",
        "planning",
      );
      expect(content).toEqual({ lines: [], lineCount: 0 });
    });
  });
});
