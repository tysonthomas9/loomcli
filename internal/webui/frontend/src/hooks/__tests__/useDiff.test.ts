/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { fetchDiffFiles, fetchDiffFile } from "@/api/diff";
import type { DiffFile, DiffFilePatch } from "@/api/diff";

import { useDiff } from "../useDiff";

vi.mock("@/api/diff", () => ({
  fetchDiffFiles: vi.fn(),
  fetchDiffFile: vi.fn(),
}));

const mockFetchDiffFiles = vi.mocked(fetchDiffFiles);
const mockFetchDiffFile = vi.mocked(fetchDiffFile);

function createMockFiles(count = 3): DiffFile[] {
  return Array.from({ length: count }, (_, i) => ({
    path: `src/file${i}.ts`,
    status: "M" as const,
    additions: (i + 1) * 10,
    deletions: (i + 1) * 5,
  }));
}

function createMockPatch(
  overrides: Partial<DiffFilePatch> = {},
): DiffFilePatch {
  return {
    patch: "--- a/file.ts\n+++ b/file.ts\n@@ -1,3 +1,4 @@",
    is_binary: false,
    is_too_large: false,
    additions: 5,
    deletions: 2,
    ...overrides,
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useDiff", () => {
  beforeEach(() => {
    mockFetchDiffFiles.mockReset();
    mockFetchDiffFile.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("Initial state", () => {
    it("returns empty files, not loading, no error when disabled", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      expect(result.current.files).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("returns empty files when agentName is null", () => {
      const { result } = renderHook(() =>
        useDiff({ workspaceId: "test-ws-id", agentName: null, enabled: true }),
      );

      expect(result.current.files).toEqual([]);
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });

    it("returns empty viewedFiles Set and patchCache Map", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      expect(result.current.viewedFiles).toBeInstanceOf(Set);
      expect(result.current.viewedFiles.size).toBe(0);
      expect(result.current.patchCache).toBeInstanceOf(Map);
      expect(result.current.patchCache.size).toBe(0);
    });
  });

  describe("File list fetching", () => {
    it("fetches file list when enabled with agent name", async () => {
      const mockFiles = createMockFiles();
      mockFetchDiffFiles.mockResolvedValue(mockFiles);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(mockFetchDiffFiles).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-1",
        "HEAD",
      );
      expect(result.current.files).toEqual(mockFiles);
      expect(result.current.isLoading).toBe(false);
    });

    it("does not fetch when disabled", () => {
      renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      expect(mockFetchDiffFiles).not.toHaveBeenCalled();
    });

    it("does not fetch when agentName is null", () => {
      renderHook(() =>
        useDiff({ workspaceId: "test-ws-id", agentName: null, enabled: true }),
      );

      expect(mockFetchDiffFiles).not.toHaveBeenCalled();
    });

    it("sets error on fetch failure", async () => {
      mockFetchDiffFiles.mockRejectedValue(new Error("Network error"));

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error!.message).toBe("Network error");
      expect(result.current.isLoading).toBe(false);
    });

    it("wraps non-Error thrown values", async () => {
      mockFetchDiffFiles.mockRejectedValue("string error");

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.error).toBeInstanceOf(Error);
      expect(result.current.error!.message).toBe("string error");
    });
  });

  describe("Patch fetching", () => {
    it("fetchPatch calls fetchDiffFile and adds result to patchCache", async () => {
      const mockPatch = createMockPatch();
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile.mockResolvedValue(mockPatch);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      expect(mockFetchDiffFile).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-1",
        "src/main.ts",
        "HEAD",
      );
      expect(result.current.patchCache.get("src/main.ts")).toEqual(mockPatch);
    });

    it("fetchPatch returns immediately on cache hit", async () => {
      const mockPatch = createMockPatch();
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile.mockResolvedValue(mockPatch);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      expect(mockFetchDiffFile).toHaveBeenCalledTimes(1);

      // Second call should be a cache hit
      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      expect(mockFetchDiffFile).toHaveBeenCalledTimes(1);
    });

    it("fetchPatch sets per-file error on failure, not global error", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile.mockRejectedValue(new Error("Patch failed"));

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      expect(result.current.error).toBeNull();
      expect(result.current.patchErrors.get("src/main.ts")).toBeInstanceOf(
        Error,
      );
      expect(result.current.patchErrors.get("src/main.ts")!.message).toBe(
        "Patch failed",
      );
      expect(result.current.patchCache.has("src/main.ts")).toBe(false);
    });

    it("fetchPatch error is isolated per file", async () => {
      const mockPatch = createMockPatch();
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile
        .mockRejectedValueOnce(new Error("File A failed"))
        .mockResolvedValueOnce(mockPatch);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });
      await act(async () => {
        await result.current.fetchPatch("src/b.ts");
      });

      expect(result.current.patchErrors.has("src/a.ts")).toBe(true);
      expect(result.current.patchErrors.has("src/b.ts")).toBe(false);
      expect(result.current.patchCache.has("src/b.ts")).toBe(true);
      expect(result.current.error).toBeNull();
    });

    it("successful retry clears per-file error", async () => {
      const mockPatch = createMockPatch();
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile
        .mockRejectedValueOnce(new Error("Temporary failure"))
        .mockResolvedValueOnce(mockPatch);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });

      expect(result.current.patchErrors.has("src/a.ts")).toBe(true);
      expect(result.current.patchCache.has("src/a.ts")).toBe(false);

      // Retry succeeds — path is not in cache, so fetchPatch will call API again
      await act(async () => {
        await result.current.fetchPatch("src/a.ts");
      });

      expect(result.current.patchErrors.has("src/a.ts")).toBe(false);
      expect(result.current.patchCache.has("src/a.ts")).toBe(true);
    });

    it("fetchPatch does nothing when path is empty", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("");
      });

      expect(mockFetchDiffFile).not.toHaveBeenCalled();
    });

    it("fetchPatch does nothing when agentName is null", async () => {
      const { result } = renderHook(() =>
        useDiff({ workspaceId: "test-ws-id", agentName: null, enabled: false }),
      );

      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      expect(mockFetchDiffFile).not.toHaveBeenCalled();
    });
  });

  describe("Viewed files", () => {
    it("markViewed adds file path to viewedFiles Set", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      act(() => {
        result.current.markViewed("src/main.ts");
      });

      expect(result.current.viewedFiles.has("src/main.ts")).toBe(true);
    });

    it("markViewed toggles — calling twice removes the path", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      act(() => {
        result.current.markViewed("src/main.ts");
      });

      expect(result.current.viewedFiles.has("src/main.ts")).toBe(true);

      act(() => {
        result.current.markViewed("src/main.ts");
      });

      expect(result.current.viewedFiles.has("src/main.ts")).toBe(false);
    });

    it("viewedFiles starts empty", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      expect(result.current.viewedFiles.size).toBe(0);
    });
  });

  describe("Summary stats", () => {
    it("computes filesChanged, additions, deletions from files", async () => {
      const mockFiles = createMockFiles(3);
      mockFetchDiffFiles.mockResolvedValue(mockFiles);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      expect(result.current.summaryStats).toEqual({
        filesChanged: 3,
        additions: 10 + 20 + 30, // (1+2+3)*10
        deletions: 5 + 10 + 15, // (1+2+3)*5
      });
    });

    it("returns zeros when files array is empty", () => {
      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      expect(result.current.summaryStats).toEqual({
        filesChanged: 0,
        additions: 0,
        deletions: 0,
      });
    });

    it("updates when files change via agent change", async () => {
      const files1 = [createMockFiles(1)[0]];
      const files2 = createMockFiles(2);

      mockFetchDiffFiles.mockResolvedValueOnce(files1);

      const { result, rerender } = renderHook(
        ({ agentName }: { agentName: string }) =>
          useDiff({ workspaceId: "test-ws-id", agentName, enabled: true }),
        { initialProps: { agentName: "agent-1" } },
      );

      await flushPromises();

      expect(result.current.summaryStats.filesChanged).toBe(1);

      mockFetchDiffFiles.mockResolvedValueOnce(files2);
      rerender({ agentName: "agent-2" });
      await flushPromises();

      expect(result.current.summaryStats.filesChanged).toBe(2);
    });
  });

  describe("Agent change", () => {
    it("resets all state when agentName changes", async () => {
      const mockFiles = createMockFiles();
      const mockPatch = createMockPatch();
      mockFetchDiffFiles.mockResolvedValue(mockFiles);
      mockFetchDiffFile.mockResolvedValue(mockPatch);

      const { result, rerender } = renderHook(
        ({ agentName }: { agentName: string }) =>
          useDiff({ workspaceId: "test-ws-id", agentName, enabled: true }),
        { initialProps: { agentName: "agent-1" } },
      );

      await flushPromises();

      // Populate state
      await act(async () => {
        await result.current.fetchPatch("src/file0.ts");
      });
      act(() => {
        result.current.markViewed("src/file0.ts");
      });

      expect(result.current.files.length).toBeGreaterThan(0);
      expect(result.current.patchCache.size).toBe(1);
      expect(result.current.viewedFiles.size).toBe(1);

      // Change agent — state should reset
      mockFetchDiffFiles.mockResolvedValue([]);
      rerender({ agentName: "agent-2" });
      await flushPromises();

      expect(result.current.files).toEqual([]);
      expect(result.current.patchCache.size).toBe(0);
      expect(result.current.patchErrors.size).toBe(0);
      expect(result.current.viewedFiles.size).toBe(0);
      expect(result.current.error).toBeNull();
    });

    it("resets patchErrors on agent change", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile.mockRejectedValue(new Error("fail"));

      const { result, rerender } = renderHook(
        ({ agentName }: { agentName: string }) =>
          useDiff({ workspaceId: "test-ws-id", agentName, enabled: true }),
        { initialProps: { agentName: "agent-1" } },
      );

      await flushPromises();

      await act(async () => {
        await result.current.fetchPatch("src/file0.ts");
      });

      expect(result.current.patchErrors.size).toBe(1);

      mockFetchDiffFiles.mockResolvedValue([]);
      rerender({ agentName: "agent-2" });
      await flushPromises();

      expect(result.current.patchErrors.size).toBe(0);
    });

    it("fetches new file list after agent change", async () => {
      const files1 = createMockFiles(1);
      const files2 = createMockFiles(2);

      mockFetchDiffFiles.mockResolvedValueOnce(files1);

      const { result, rerender } = renderHook(
        ({ agentName }: { agentName: string }) =>
          useDiff({ workspaceId: "test-ws-id", agentName, enabled: true }),
        { initialProps: { agentName: "agent-1" } },
      );

      await flushPromises();

      expect(mockFetchDiffFiles).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-1",
        "HEAD",
      );
      expect(result.current.files).toEqual(files1);

      mockFetchDiffFiles.mockResolvedValueOnce(files2);
      rerender({ agentName: "agent-2" });
      await flushPromises();

      expect(mockFetchDiffFiles).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-2",
        "HEAD",
      );
      expect(result.current.files).toEqual(files2);
    });
  });

  describe("Cleanup on unmount", () => {
    it("does not update state after unmount (file list fetch)", async () => {
      let resolvePromise: (value: DiffFile[]) => void;
      const slowPromise = new Promise<DiffFile[]>((resolve) => {
        resolvePromise = resolve;
      });
      mockFetchDiffFiles.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      expect(result.current.isLoading).toBe(true);

      unmount();

      await act(async () => {
        resolvePromise!(createMockFiles());
      });
      // No assertion needed — test passes if no React warnings about updating unmounted component
    });

    it("does not update patchCache after unmount (patch fetch)", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);

      let resolvePromise: (value: DiffFilePatch) => void;
      const slowPromise = new Promise<DiffFilePatch>((resolve) => {
        resolvePromise = resolve;
      });
      mockFetchDiffFile.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      act(() => {
        result.current.fetchPatch("src/main.ts");
      });

      unmount();

      await act(async () => {
        resolvePromise!(createMockPatch());
      });
      // No assertion needed — test passes if no React warnings
    });
  });

  describe("Callback stability", () => {
    it("markViewed is stable across renders", () => {
      const { result, rerender } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: false,
        }),
      );

      const initial = result.current.markViewed;
      rerender();
      expect(result.current.markViewed).toBe(initial);
    });

    it("fetchPatch is stable after cache update", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);
      mockFetchDiffFile.mockResolvedValue(createMockPatch());

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      const initial = result.current.fetchPatch;

      await act(async () => {
        await result.current.fetchPatch("src/main.ts");
      });

      // fetchPatch should be the same reference even after cache was updated
      expect(result.current.fetchPatch).toBe(initial);
    });
  });

  describe("Concurrent fetch guard", () => {
    it("does not fire duplicate requests for the same file", async () => {
      mockFetchDiffFiles.mockResolvedValue([]);

      let resolvePromise: (value: DiffFilePatch) => void;
      const slowPromise = new Promise<DiffFilePatch>((resolve) => {
        resolvePromise = resolve;
      });
      mockFetchDiffFile.mockReturnValue(slowPromise);

      const { result } = renderHook(() =>
        useDiff({
          workspaceId: "test-ws-id",
          agentName: "agent-1",
          enabled: true,
        }),
      );

      await flushPromises();

      // Fire two fetches for the same path concurrently
      act(() => {
        result.current.fetchPatch("src/main.ts");
        result.current.fetchPatch("src/main.ts");
      });

      // Only one network request should have been made
      expect(mockFetchDiffFile).toHaveBeenCalledTimes(1);

      await act(async () => {
        resolvePromise!(createMockPatch());
      });
    });
  });
});
