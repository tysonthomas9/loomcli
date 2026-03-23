/**
 * @vitest-environment jsdom
 */
import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { useDiff } from "../useDiff";
import * as diffApi from "../../api/diff";

// Mock the diff API
vi.mock("../../api/diff", () => ({
  fetchDiffFiles: vi.fn(),
  fetchDiffFile: vi.fn(),
}));

describe("useDiff", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initialization", () => {
    it("returns expected shape", () => {
      const { result } = renderHook(() =>
        useDiff({ agentId: null, toCommit: "" }),
      );

      expect(result.current).toHaveProperty("files");
      expect(result.current).toHaveProperty("isLoading");
      expect(result.current).toHaveProperty("error");
      expect(result.current).toHaveProperty("patchCache");
      expect(result.current).toHaveProperty("patchErrors");
      expect(result.current).toHaveProperty("fetchPatch");
      expect(result.current).toHaveProperty("clearPatchError");
    });
  });

  describe("per-file patch errors", () => {
    it("fetchPatch sets per-file error, not global error", async () => {
      vi.mocked(diffApi.fetchDiffFiles).mockResolvedValue([
        {
          path: "src/a.ts",
          status: "M",
          additions: 5,
          deletions: 2,
        },
      ]);
      vi.mocked(diffApi.fetchDiffFile).mockRejectedValue(
        new Error("Not found"),
      );

      const { result } = renderHook(() =>
        useDiff({ agentId: "agent-1", toCommit: "abc123" }),
      );

      await waitFor(() => {
        expect(result.current.files).toHaveLength(1);
      });

      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });

      // Per-file error should be set
      expect(result.current.patchErrors.get("src/a.ts")?.message).toBe(
        "Not found",
      );
      // Global error should NOT be set
      expect(result.current.error).toBeNull();
    });

    it("fetchPatch error is isolated per file", async () => {
      vi.mocked(diffApi.fetchDiffFiles).mockResolvedValue([
        { path: "src/a.ts", status: "M", additions: 1, deletions: 0 },
        { path: "src/b.ts", status: "M", additions: 2, deletions: 1 },
      ]);

      // a.ts fails, b.ts succeeds
      vi.mocked(diffApi.fetchDiffFile)
        .mockRejectedValueOnce(new Error("Failed for a.ts"))
        .mockResolvedValueOnce({
          patch: "diff content",
          is_binary: false,
          is_too_large: false,
          additions: 2,
          deletions: 1,
        });

      const { result } = renderHook(() =>
        useDiff({ agentId: "agent-1", toCommit: "abc123" }),
      );

      await waitFor(() => {
        expect(result.current.files).toHaveLength(2);
      });

      // Fetch both
      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
        await result.current.fetchPatch("src/b.ts");
      });

      // a.ts has error
      expect(result.current.patchErrors.has("src/a.ts")).toBe(true);
      // b.ts is in cache, no error
      expect(result.current.patchCache.has("src/b.ts")).toBe(true);
      expect(result.current.patchErrors.has("src/b.ts")).toBe(false);
    });

    it("successful retry clears patchError", async () => {
      vi.mocked(diffApi.fetchDiffFiles).mockResolvedValue([
        { path: "src/a.ts", status: "M", additions: 1, deletions: 0 },
      ]);

      // First call fails, second succeeds
      vi.mocked(diffApi.fetchDiffFile)
        .mockRejectedValueOnce(new Error("Transient error"))
        .mockResolvedValueOnce({
          patch: "diff content",
          is_binary: false,
          is_too_large: false,
          additions: 1,
          deletions: 0,
        });

      const { result } = renderHook(() =>
        useDiff({ agentId: "agent-1", toCommit: "abc123" }),
      );

      await waitFor(() => {
        expect(result.current.files).toHaveLength(1);
      });

      // First fetch fails
      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });
      expect(result.current.patchErrors.has("src/a.ts")).toBe(true);

      // Clear error and retry
      act(() => {
        result.current.clearPatchError("src/a.ts");
      });
      expect(result.current.patchErrors.has("src/a.ts")).toBe(false);

      // Retry succeeds
      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });
      expect(result.current.patchCache.has("src/a.ts")).toBe(true);
      expect(result.current.patchErrors.has("src/a.ts")).toBe(false);
    });

    it("patchErrors reset on agent change", async () => {
      vi.mocked(diffApi.fetchDiffFiles).mockResolvedValue([
        { path: "src/a.ts", status: "M", additions: 1, deletions: 0 },
      ]);
      vi.mocked(diffApi.fetchDiffFile).mockRejectedValue(
        new Error("Error"),
      );

      const { result, rerender } = renderHook(
        ({ agentId }) =>
          useDiff({ agentId, toCommit: "abc123" }),
        { initialProps: { agentId: "agent-1" } },
      );

      await waitFor(() => {
        expect(result.current.files).toHaveLength(1);
      });

      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });
      expect(result.current.patchErrors.size).toBe(1);

      // Change agent — state should reset
      rerender({ agentId: "agent-2" });

      expect(result.current.patchErrors.size).toBe(0);
      expect(result.current.patchCache.size).toBe(0);
    });
  });
});
