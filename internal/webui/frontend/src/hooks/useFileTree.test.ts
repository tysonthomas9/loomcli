/**
 * @vitest-environment jsdom
 */
import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { listWorktreeDir } from "@/api/files";
import type { FileEntry, DirListData } from "@/api/files";

import { useFileTree } from "./useFileTree";

vi.mock("@/api/files", () => ({
  listWorktreeDir: vi.fn(),
}));

const mockListDir = vi.mocked(listWorktreeDir);

function createEntry(overrides: Partial<FileEntry> = {}): FileEntry {
  return {
    name: overrides.name ?? "file.ts",
    is_dir: overrides.is_dir ?? false,
    size: overrides.size ?? 100,
    mod_time: overrides.mod_time ?? "2024-01-01T00:00:00Z",
    ...overrides,
  };
}

function createDirList(path: string, entries: FileEntry[] = []): DirListData {
  return { path, entries };
}

const rootEntries: FileEntry[] = [
  createEntry({ name: "src", is_dir: true }),
  createEntry({ name: "README.md", is_dir: false }),
];

const srcEntries: FileEntry[] = [
  createEntry({ name: "main.ts", is_dir: false }),
  createEntry({ name: "utils", is_dir: true }),
];

/** Helper to render the hook and wait for the initial root load to complete. */
async function renderAndWaitForRoot(agentName = "agent-1") {
  const hookResult = renderHook(
    ({ agent }: { agent: string }) => useFileTree("test-ws-id", agent),
    { initialProps: { agent: agentName } },
  );

  await waitFor(() => {
    expect(hookResult.result.current.isLoading).toBe(false);
  });

  return hookResult;
}

describe("useFileTree", () => {
  beforeEach(() => {
    mockListDir.mockReset();
    // Default: root loads successfully
    mockListDir.mockResolvedValue(createDirList("", rootEntries));
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("Initial state and root auto-load", () => {
    it("starts loading and auto-loads root directory", async () => {
      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      // Initially loading
      expect(result.current.isLoading).toBe(true);
      expect(result.current.selectedPath).toBeNull();
      expect(result.current.error).toBeNull();
      expect(result.current.filterText).toBe("");

      // Wait for root to load
      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      expect(result.current.treeData.get("")).toEqual(rootEntries);
      expect(result.current.expanded.has("")).toBe(true);
      expect(mockListDir).toHaveBeenCalledWith("test-ws-id", "agent-1");
    });

    it("does not load when agentName is empty", () => {
      const { result } = renderHook(() => useFileTree("test-ws-id", ""));

      expect(result.current.isLoading).toBe(false);
      expect(mockListDir).not.toHaveBeenCalled();
    });
  });

  describe("Agent change", () => {
    it("resets state and reloads root when agentName changes", async () => {
      const { result, rerender } = await renderAndWaitForRoot("agent-1");

      expect(result.current.treeData.get("")).toEqual(rootEntries);

      // Change agent
      const newEntries = [createEntry({ name: "lib", is_dir: true })];
      mockListDir.mockResolvedValueOnce(createDirList("", newEntries));

      rerender({ agent: "agent-2" });

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      expect(result.current.treeData.get("")).toEqual(newEntries);
      expect(mockListDir).toHaveBeenCalledWith("test-ws-id", "agent-2");
    });
  });

  describe("toggle", () => {
    it("expands a directory and fetches its contents", async () => {
      const { result } = await renderAndWaitForRoot();

      mockListDir.mockResolvedValueOnce(createDirList("src", srcEntries));

      await act(async () => {
        await result.current.toggle("src");
      });

      expect(result.current.expanded.has("src")).toBe(true);
      expect(result.current.treeData.get("src")).toEqual(srcEntries);
      expect(mockListDir).toHaveBeenCalledWith("test-ws-id", "agent-1", "src");
    });

    it("collapses a directory without clearing cache", async () => {
      const { result } = await renderAndWaitForRoot();

      mockListDir.mockResolvedValueOnce(createDirList("src", srcEntries));

      await act(async () => {
        await result.current.toggle("src");
      });

      expect(result.current.expanded.has("src")).toBe(true);

      // Collapse
      await act(async () => {
        await result.current.toggle("src");
      });

      expect(result.current.expanded.has("src")).toBe(false);
      // Cache preserved
      expect(result.current.treeData.get("src")).toEqual(srcEntries);
    });

    it("re-expands from cache without fetching again", async () => {
      const { result } = await renderAndWaitForRoot();

      mockListDir.mockResolvedValueOnce(createDirList("src", srcEntries));

      // Expand
      await act(async () => {
        await result.current.toggle("src");
      });

      const callCountAfterExpand = mockListDir.mock.calls.length;

      // Collapse
      await act(async () => {
        await result.current.toggle("src");
      });

      // Re-expand
      await act(async () => {
        await result.current.toggle("src");
      });

      expect(result.current.expanded.has("src")).toBe(true);
      // No additional fetch call
      expect(mockListDir.mock.calls.length).toBe(callCountAfterExpand);
    });
  });

  describe("loadDir", () => {
    it("fetches and caches directory contents", async () => {
      const { result } = await renderAndWaitForRoot();

      mockListDir.mockResolvedValueOnce(createDirList("src", srcEntries));

      await act(async () => {
        await result.current.loadDir("src");
      });

      expect(result.current.treeData.get("src")).toEqual(srcEntries);
      expect(result.current.error).toBeNull();
    });

    it("clears error on successful load", async () => {
      // Make root fail first
      mockListDir.mockReset();
      mockListDir.mockRejectedValueOnce(new Error("fail"));

      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      expect(result.current.error).toBe("fail");

      mockListDir.mockResolvedValueOnce(createDirList("src", srcEntries));

      await act(async () => {
        await result.current.loadDir("src");
      });

      expect(result.current.error).toBeNull();
    });
  });

  describe("selectFile", () => {
    it("sets selectedPath", async () => {
      const { result } = await renderAndWaitForRoot();

      act(() => {
        result.current.selectFile("src/main.ts");
      });

      expect(result.current.selectedPath).toBe("src/main.ts");
    });

    it("clears selectedPath with null", async () => {
      const { result } = await renderAndWaitForRoot();

      act(() => {
        result.current.selectFile("src/main.ts");
      });

      act(() => {
        result.current.selectFile(null);
      });

      expect(result.current.selectedPath).toBeNull();
    });
  });

  describe("filterText", () => {
    it("updates filterText immediately", () => {
      vi.useFakeTimers();
      mockListDir.mockImplementation(() => new Promise(() => {}));
      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      act(() => {
        result.current.setFilterText("main");
      });

      expect(result.current.filterText).toBe("main");
      vi.useRealTimers();
    });

    it("debouncedFilterText updates after delay", () => {
      vi.useFakeTimers();
      mockListDir.mockImplementation(() => new Promise(() => {}));

      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      act(() => {
        result.current.setFilterText("main");
      });

      expect(result.current.filterText).toBe("main");
      expect(result.current.debouncedFilterText).toBe("");

      act(() => {
        vi.advanceTimersByTime(200);
      });

      expect(result.current.debouncedFilterText).toBe("main");

      vi.useRealTimers();
    });
  });

  describe("Error handling", () => {
    it("sets error on root load failure", async () => {
      mockListDir.mockReset();
      mockListDir.mockRejectedValueOnce(new Error("Connection refused"));

      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      expect(result.current.error).toBe("Connection refused");
    });

    it("sets error on directory fetch failure", async () => {
      const { result } = await renderAndWaitForRoot();

      mockListDir.mockRejectedValueOnce(new Error("Permission denied"));

      await act(async () => {
        await result.current.loadDir("restricted");
      });

      expect(result.current.error).toBe("Permission denied");
    });

    it("converts non-Error thrown values to string", async () => {
      mockListDir.mockReset();
      mockListDir.mockRejectedValueOnce("string error");

      const { result } = renderHook(() => useFileTree("test-ws-id", "agent-1"));

      await waitFor(() => {
        expect(result.current.isLoading).toBe(false);
      });

      expect(result.current.error).toBe("string error");
    });
  });

  describe("Cleanup on unmount", () => {
    it("does not update state after unmount", async () => {
      let resolvePromise: (value: DirListData) => void;
      const slowPromise = new Promise<DirListData>((resolve) => {
        resolvePromise = resolve;
      });
      mockListDir.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() =>
        useFileTree("test-ws-id", "agent-1"),
      );

      expect(result.current.isLoading).toBe(true);

      unmount();

      await act(async () => {
        resolvePromise!(createDirList("", rootEntries));
      });

      // No React state update warning should occur
    });
  });
});
