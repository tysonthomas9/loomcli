/**
 * @vitest-environment jsdom
 */
import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { readWorktreeFile } from "@/api/workspace";
import type { FileReadData } from "@/api/workspace";

import { useFileContent } from "../useFileContent";

vi.mock("@/api/workspace", () => ({
  readWorktreeFile: vi.fn(),
}));

vi.mock("@/hooks/workspace", async () => {
  const actual =
    await vi.importActual<typeof import("@/hooks/workspace")>(
      "@/hooks/workspace",
    );
  return {
    ...actual,
    useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  };
});

const mockReadFile = vi.mocked(readWorktreeFile);

function createFileData(overrides: Partial<FileReadData> = {}): FileReadData {
  return {
    path: overrides.path ?? "src/main.ts",
    content: overrides.content ?? 'console.log("hello")',
    size: overrides.size ?? 20,
    binary: overrides.binary ?? false,
    ...overrides,
  };
}

describe("useFileContent", () => {
  beforeEach(() => {
    mockReadFile.mockReset();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("Initial state", () => {
    it("returns expected shape with all properties", () => {
      const { result } = renderHook(() => useFileContent("agent-1"));

      expect(result.current).toHaveProperty("fileData");
      expect(result.current).toHaveProperty("isLoading");
      expect(result.current).toHaveProperty("error");
      expect(result.current).toHaveProperty("fetchFile");
      expect(result.current).toHaveProperty("clearFile");

      expect(typeof result.current.fetchFile).toBe("function");
      expect(typeof result.current.clearFile).toBe("function");
    });

    it("starts with null fileData, not loading, no error", () => {
      const { result } = renderHook(() => useFileContent("agent-1"));

      expect(result.current.fileData).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(result.current.error).toBeNull();
    });
  });

  describe("Successful fetch flow", () => {
    it("fetches file content when fetchFile is called", async () => {
      const testFile = createFileData({ path: "src/app.ts" });
      mockReadFile.mockResolvedValue(testFile);

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("src/app.ts");
      });

      expect(result.current.fileData).toEqual(testFile);
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(false);
      expect(mockReadFile).toHaveBeenCalledTimes(1);
      expect(mockReadFile).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-1",
        "src/app.ts",
      );
    });

    it("sets isLoading to true during fetch", async () => {
      let resolvePromise: (value: FileReadData) => void;
      const slowPromise = new Promise<FileReadData>((resolve) => {
        resolvePromise = resolve;
      });
      mockReadFile.mockReturnValue(slowPromise);

      const { result } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("src/main.ts");
      });

      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolvePromise!(createFileData());
      });

      expect(result.current.isLoading).toBe(false);
    });

    it("clears previous error on new fetch", async () => {
      mockReadFile.mockRejectedValueOnce(new Error("Not found"));

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("bad-path");
      });

      expect(result.current.error).toBe("Not found");

      mockReadFile.mockResolvedValueOnce(createFileData());

      await act(async () => {
        await result.current.fetchFile("good-path");
      });

      expect(result.current.error).toBeNull();
    });
  });

  describe("Validation", () => {
    it("does not fetch when path is empty", async () => {
      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("");
      });

      expect(mockReadFile).not.toHaveBeenCalled();
      expect(result.current.isLoading).toBe(false);
    });

    it("does not fetch when agentName is empty", async () => {
      const { result } = renderHook(() => useFileContent(""));

      await act(async () => {
        await result.current.fetchFile("src/main.ts");
      });

      expect(mockReadFile).not.toHaveBeenCalled();
      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("Error handling", () => {
    it("sets error on fetch failure with Error object", async () => {
      mockReadFile.mockRejectedValue(new Error("Network error"));

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("src/main.ts");
      });

      expect(result.current.error).toBe("Network error");
      expect(result.current.isLoading).toBe(false);
    });

    it("converts non-Error thrown values to string", async () => {
      mockReadFile.mockRejectedValue("string error");

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("src/main.ts");
      });

      expect(result.current.error).toBe("string error");
    });

    it("clears existing fileData on error so stale content is not shown", async () => {
      const testFile = createFileData({ path: "src/app.ts" });
      mockReadFile.mockResolvedValueOnce(testFile);

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("src/app.ts");
      });

      expect(result.current.fileData).toEqual(testFile);

      mockReadFile.mockRejectedValueOnce(new Error("Network error"));

      await act(async () => {
        await result.current.fetchFile("src/other.ts");
      });

      // The previously-open file's content must not linger behind the error.
      expect(result.current.fileData).toBeNull();
      expect(result.current.error).toBe("Network error");
    });
  });

  describe("Clearing file", () => {
    it("clearFile resets all state", async () => {
      const testFile = createFileData();
      mockReadFile.mockResolvedValueOnce(testFile);

      const { result } = renderHook(() => useFileContent("agent-1"));

      await act(async () => {
        await result.current.fetchFile("src/main.ts");
      });

      expect(result.current.fileData).toEqual(testFile);

      act(() => {
        result.current.clearFile();
      });

      expect(result.current.fileData).toBeNull();
      expect(result.current.error).toBeNull();
      expect(result.current.isLoading).toBe(false);
    });

    it("clearFile cancels in-flight requests", async () => {
      let resolvePromise: (value: FileReadData) => void;
      const slowPromise = new Promise<FileReadData>((resolve) => {
        resolvePromise = resolve;
      });
      mockReadFile.mockReturnValue(slowPromise);

      const { result } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("src/main.ts");
      });

      expect(result.current.isLoading).toBe(true);

      act(() => {
        result.current.clearFile();
      });

      expect(result.current.isLoading).toBe(false);
      expect(result.current.fileData).toBeNull();

      await act(async () => {
        resolvePromise!(
          createFileData({ path: "src/main.ts", content: "should not appear" }),
        );
      });

      expect(result.current.fileData).toBeNull();
    });
  });

  describe("Concurrent fetch handling (latest request wins)", () => {
    it("only uses result from latest request", async () => {
      const firstFile = createFileData({
        path: "first.ts",
        content: "first",
      });
      const secondFile = createFileData({
        path: "second.ts",
        content: "second",
      });

      let resolveFirst: (value: FileReadData) => void;
      let resolveSecond: (value: FileReadData) => void;

      const firstPromise = new Promise<FileReadData>((resolve) => {
        resolveFirst = resolve;
      });
      const secondPromise = new Promise<FileReadData>((resolve) => {
        resolveSecond = resolve;
      });

      mockReadFile
        .mockReturnValueOnce(firstPromise)
        .mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("first.ts");
      });

      act(() => {
        result.current.fetchFile("second.ts");
      });

      await act(async () => {
        resolveSecond!(secondFile);
      });

      expect(result.current.fileData).toEqual(secondFile);
      expect(result.current.isLoading).toBe(false);

      await act(async () => {
        resolveFirst!(firstFile);
      });

      expect(result.current.fileData).toEqual(secondFile);
    });

    it("only latest request controls loading state", async () => {
      let resolveFirst: (value: FileReadData) => void;
      let resolveSecond: (value: FileReadData) => void;

      const firstPromise = new Promise<FileReadData>((resolve) => {
        resolveFirst = resolve;
      });
      const secondPromise = new Promise<FileReadData>((resolve) => {
        resolveSecond = resolve;
      });

      mockReadFile
        .mockReturnValueOnce(firstPromise)
        .mockReturnValueOnce(secondPromise);

      const { result } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("first.ts");
      });
      expect(result.current.isLoading).toBe(true);

      act(() => {
        result.current.fetchFile("second.ts");
      });
      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveFirst!(createFileData({ path: "first.ts" }));
      });

      // Still loading because second request is pending
      expect(result.current.isLoading).toBe(true);

      await act(async () => {
        resolveSecond!(createFileData({ path: "second.ts" }));
      });

      expect(result.current.isLoading).toBe(false);
    });
  });

  describe("Cleanup on unmount", () => {
    it("does not update state after unmount", async () => {
      let resolvePromise: (value: FileReadData) => void;
      const slowPromise = new Promise<FileReadData>((resolve) => {
        resolvePromise = resolve;
      });
      mockReadFile.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("src/main.ts");
      });

      expect(result.current.isLoading).toBe(true);

      unmount();

      await act(async () => {
        resolvePromise!(createFileData());
      });
    });

    it("does not set error after unmount", async () => {
      let rejectPromise: (error: Error) => void;
      const slowPromise = new Promise<FileReadData>((_, reject) => {
        rejectPromise = reject;
      });
      mockReadFile.mockReturnValue(slowPromise);

      const { result, unmount } = renderHook(() => useFileContent("agent-1"));

      act(() => {
        result.current.fetchFile("src/main.ts");
      });

      unmount();

      await act(async () => {
        rejectPromise!(new Error("Network error"));
      });
    });
  });

  describe("Callback stability", () => {
    it("fetchFile is stable across renders", () => {
      const { result, rerender } = renderHook(() => useFileContent("agent-1"));

      const initialFetchFile = result.current.fetchFile;
      rerender();
      expect(result.current.fetchFile).toBe(initialFetchFile);
    });

    it("clearFile is stable across renders", () => {
      const { result, rerender } = renderHook(() => useFileContent("agent-1"));

      const initialClearFile = result.current.clearFile;
      rerender();
      expect(result.current.clearFile).toBe(initialClearFile);
    });
  });
});
