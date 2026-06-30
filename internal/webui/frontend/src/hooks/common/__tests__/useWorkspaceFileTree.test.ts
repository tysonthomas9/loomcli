/**
 * @vitest-environment jsdom
 */
import { renderHook, act, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { listWorkspaceDir } from "@/api/workspace";
import type { FileEntry, DirListData } from "@/api/workspace";

import { useWorkspaceFileTree } from "../useFileTree";

vi.mock("@/api/workspace", () => ({
  listWorkspaceDir: vi.fn(),
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

const mockListDir = vi.mocked(listWorkspaceDir);

function entry(name: string, isDir = false): FileEntry {
  return { name, is_dir: isDir, size: 1, mod_time: "2024-01-01T00:00:00Z" };
}
function dirList(path: string, entries: FileEntry[]): DirListData {
  return { path, entries };
}

describe("useWorkspaceFileTree", () => {
  beforeEach(() => vi.clearAllMocks());

  it("loads the workspace root on mount (no agent name required)", async () => {
    mockListDir.mockResolvedValue(
      dirList("", [entry("repo-a", true), entry("README.md")]),
    );
    const { result } = renderHook(() => useWorkspaceFileTree());

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(mockListDir).toHaveBeenCalledWith("test-ws-id");
    expect(result.current.treeData.get("")).toHaveLength(2);
    expect(result.current.expanded.has("")).toBe(true);
  });

  it("revealPath loads and expands every directory along the path", async () => {
    mockListDir.mockImplementation((_ws: string, path?: string) => {
      switch (path) {
        case undefined:
        case "":
          return Promise.resolve(dirList("", [entry("src", true)]));
        case "src":
          return Promise.resolve(dirList("src", [entry("api", true)]));
        case "src/api":
          return Promise.resolve(dirList("src/api", [entry("handler.go")]));
        default:
          return Promise.reject(new Error("not found"));
      }
    });

    const { result } = renderHook(() => useWorkspaceFileTree());
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.revealPath("src/api");
    });

    expect(result.current.expanded.has("src")).toBe(true);
    expect(result.current.expanded.has("src/api")).toBe(true);
    expect(result.current.treeData.get("src/api")).toHaveLength(1);
  });

  it("revealPath stops at a non-directory segment without throwing", async () => {
    mockListDir.mockImplementation((_ws: string, path?: string) => {
      if (!path) return Promise.resolve(dirList("", [entry("src", true)]));
      if (path === "src")
        return Promise.resolve(dirList("src", [entry("main.go")]));
      // Descending into a file errors.
      return Promise.reject(new Error("path is not a directory"));
    });

    const { result } = renderHook(() => useWorkspaceFileTree());
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    await act(async () => {
      await result.current.revealPath("src/main.go");
    });

    // Landed as deep as possible: src is expanded, the file segment is not.
    expect(result.current.expanded.has("src")).toBe(true);
    expect(result.current.expanded.has("src/main.go")).toBe(false);
  });

  it("surfaces the backend error when the workspace is not checked out", async () => {
    mockListDir.mockRejectedValue(
      new Error("workspace not checked out on this machine"),
    );
    const { result } = renderHook(() => useWorkspaceFileTree());

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.error).toContain("not checked out");
  });
});
