/**
 * @vitest-environment jsdom
 */

import { renderHook, act } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

// Mock modules before imports
vi.mock("@/hooks", () => ({
  useFileTree: vi.fn(),
  useFileContent: vi.fn(),
  useToast: vi.fn(),
  useWorkspaceContext: () => ({ workspaceId: "test-ws-id" }),
  useRegisterEscapeLayer: vi.fn(),
  useKeyboardShortcuts: vi.fn(() => ({
    isCheatsheetOpen: false,
    toggleCheatsheet: vi.fn(),
    closeCheatsheet: vi.fn(),
  })),
  KeyboardShortcutProvider: ({ children }: { children: React.ReactNode }) =>
    children,
  LAYER_CONFIRM_DIALOG: 60,
  LAYER_TOAST: 50,
  LAYER_CHEATSHEET: 45,
  LAYER_MODAL: 40,
  LAYER_TERMINAL_PANEL: 30,
  LAYER_AGENT_PANEL: 20,
  LAYER_ISSUE_PANEL: 10,
}));

vi.mock("@/api/workspace", () => ({
  writeWorktreeFile: vi.fn(),
}));

import { useFileTree, useFileContent, useToast } from "@/hooks";
import { writeWorktreeFile } from "@/api/workspace";
import type { UseFileTreeReturn, UseFileContentReturn } from "@/hooks";
import type { FileReadData } from "@/api/workspace";
import { useFileEditor } from "../useFileEditor";

const mockUseFileTree = vi.mocked(useFileTree);
const mockUseFileContent = vi.mocked(useFileContent);
const mockUseToast = vi.mocked(useToast);
const mockWriteWorktreeFile = vi.mocked(writeWorktreeFile);

function createMockTree(
  overrides?: Partial<UseFileTreeReturn>,
): UseFileTreeReturn {
  return {
    expanded: new Set<string>(),
    treeData: new Map(),
    selectedPath: null,
    isLoading: false,
    error: null,
    filterText: "",
    debouncedFilterText: "",
    toggle: vi.fn(),
    loadDir: vi.fn(),
    selectFile: vi.fn(),
    setFilterText: vi.fn(),
    ...overrides,
  };
}

function createMockFileContent(
  overrides?: Partial<UseFileContentReturn>,
): UseFileContentReturn {
  return {
    fileData: null,
    isLoading: false,
    error: null,
    fetchFile: vi.fn(),
    clearFile: vi.fn(),
    ...overrides,
  };
}

function createMockToast() {
  return {
    toasts: [],
    showToast: vi.fn(),
    dismissToast: vi.fn(),
    dismissAll: vi.fn(),
  };
}

async function flushPromises(): Promise<void> {
  await act(async () => {
    await Promise.resolve();
  });
}

describe("useFileEditor", () => {
  let mockTree: UseFileTreeReturn;
  let mockContent: UseFileContentReturn;
  let mockToast: ReturnType<typeof createMockToast>;

  beforeEach(() => {
    vi.clearAllMocks();
    mockTree = createMockTree();
    mockContent = createMockFileContent();
    mockToast = createMockToast();
    mockUseFileTree.mockReturnValue(mockTree);
    mockUseFileContent.mockReturnValue(mockContent);
    mockUseToast.mockReturnValue(mockToast);
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  describe("initial state", () => {
    it("returns empty content, not dirty, not saving, no pending action", () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.content).toBe("");
      expect(result.current.isDirty).toBe(false);
      expect(result.current.isSaving).toBe(false);
      expect(result.current.pendingAction).toBeNull();
      expect(result.current.language).toBeUndefined();
    });

    it("passes through tree and fileContent from hooks", () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.tree).toBe(mockTree);
      expect(result.current.fileContent).toBe(mockContent);
    });
  });

  describe("file selection (clean state)", () => {
    it("calls tree.selectFile and fileContent.fetchFile on select", () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleFileSelect("src/main.go");
      });

      expect(mockTree.selectFile).toHaveBeenCalledWith("src/main.go");
      expect(mockContent.fetchFile).toHaveBeenCalledWith("src/main.go");
    });

    it("updates content when fileContent.fileData changes", async () => {
      const { result, rerender } = renderHook(() =>
        useFileEditor("agent-1", true),
      );

      // Simulate file data arriving
      const fileData: FileReadData = {
        path: "src/main.go",
        content: "package main",
        size: 12,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      await act(async () => {
        rerender();
      });

      expect(result.current.content).toBe("package main");
      expect(result.current.isDirty).toBe(false);
    });

    it("does not set content for binary files", () => {
      const fileData: FileReadData = {
        path: "image.png",
        size: 1024,
        binary: true,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.content).toBe("");
    });
  });

  describe("language detection", () => {
    it.each([
      ["src/main.go", "go"],
      ["config.json", "json"],
      ["values.yaml", "yaml"],
      ["docker-compose.yml", "yaml"],
      ["README.md", "markdown"],
      ["docs/guide.markdown", "markdown"],
    ])("derives language from %s as %s", (path, expectedLang) => {
      mockTree = createMockTree({ selectedPath: path });
      mockUseFileTree.mockReturnValue(mockTree);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.language).toBe(expectedLang);
    });

    it.each(["file.txt", "script.py", "lib.rs", "Makefile"])(
      "returns undefined for unknown extension %s",
      (path) => {
        mockTree = createMockTree({ selectedPath: path });
        mockUseFileTree.mockReturnValue(mockTree);

        const { result } = renderHook(() => useFileEditor("agent-1", true));

        expect(result.current.language).toBeUndefined();
      },
    );

    it("returns undefined when no file is selected", () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.language).toBeUndefined();
    });
  });

  describe("editing", () => {
    it("handleContentChange updates content", () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("new content");
      });

      expect(result.current.content).toBe("new content");
    });

    it("isDirty becomes true when content differs from saved", () => {
      const fileData: FileReadData = {
        path: "f.go",
        content: "original",
        size: 8,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      expect(result.current.isDirty).toBe(false);

      act(() => {
        result.current.handleContentChange("modified");
      });

      expect(result.current.isDirty).toBe(true);
    });

    it("isDirty becomes false when content matches saved", async () => {
      const { result, rerender } = renderHook(() =>
        useFileEditor("agent-1", true),
      );

      // Simulate file data arriving
      const fileData: FileReadData = {
        path: "f.go",
        content: "original",
        size: 8,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      await act(async () => {
        rerender();
      });

      act(() => {
        result.current.handleContentChange("modified");
      });
      expect(result.current.isDirty).toBe(true);

      act(() => {
        result.current.handleContentChange("original");
      });
      expect(result.current.isDirty).toBe(false);
    });
  });

  describe("save", () => {
    it("calls writeWorktreeFile and updates saved ref on success", async () => {
      mockTree = createMockTree({ selectedPath: "src/main.go" });
      mockUseFileTree.mockReturnValue(mockTree);
      mockWriteWorktreeFile.mockResolvedValue(undefined);

      const { result, rerender } = renderHook(() =>
        useFileEditor("agent-1", true),
      );

      // Type content (savedContentRef starts at "", so any text is dirty)
      act(() => {
        result.current.handleContentChange("new content");
      });
      expect(result.current.isDirty).toBe(true);

      await act(async () => {
        await result.current.save();
      });

      expect(mockWriteWorktreeFile).toHaveBeenCalledWith(
        "test-ws-id",
        "agent-1",
        "src/main.go",
        "new content",
      );
      expect(result.current.isSaving).toBe(false);
      expect(mockToast.showToast).toHaveBeenCalledWith("File saved", {
        type: "success",
      });
      // Force rerender to pick up the ref update (act batches the
      // setIsSaving(true/false) pair, so isDirty may not be re-computed)
      rerender();
      expect(result.current.isDirty).toBe(false);
    });

    it("shows error toast on save failure", async () => {
      mockTree = createMockTree({ selectedPath: "f.go" });
      mockUseFileTree.mockReturnValue(mockTree);
      const fileData: FileReadData = {
        path: "f.go",
        content: "old",
        size: 3,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);
      mockWriteWorktreeFile.mockRejectedValueOnce(
        new Error("Permission denied"),
      );

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("changed");
      });

      await act(async () => {
        await result.current.save();
      });

      expect(result.current.isDirty).toBe(true);
      expect(result.current.isSaving).toBe(false);
      expect(mockToast.showToast).toHaveBeenCalledWith(
        "Failed to save: Permission denied",
        { type: "error" },
      );
    });

    it("is a no-op when not dirty", async () => {
      mockTree = createMockTree({ selectedPath: "f.go" });
      mockUseFileTree.mockReturnValue(mockTree);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      await act(async () => {
        await result.current.save();
      });

      expect(mockWriteWorktreeFile).not.toHaveBeenCalled();
    });

    it("is a no-op when no file selected", async () => {
      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("something");
      });

      await act(async () => {
        await result.current.save();
      });

      expect(mockWriteWorktreeFile).not.toHaveBeenCalled();
    });
  });

  describe("file switch with dirty state", () => {
    it("sets pendingAction when switching with unsaved changes", () => {
      const fileData: FileReadData = {
        path: "a.go",
        content: "original",
        size: 8,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      // Make dirty
      act(() => {
        result.current.handleContentChange("modified");
      });
      expect(result.current.isDirty).toBe(true);

      // Try to switch
      act(() => {
        result.current.handleFileSelect("b.go");
      });

      expect(result.current.pendingAction).toEqual({
        type: "switch",
        path: "b.go",
      });
      // Should NOT have called selectFile
      expect(mockTree.selectFile).not.toHaveBeenCalled();
    });

    it("confirmDiscard clears pendingAction and executes switch", () => {
      const fileData: FileReadData = {
        path: "a.go",
        content: "original",
        size: 8,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("modified");
      });
      act(() => {
        result.current.handleFileSelect("b.go");
      });
      expect(result.current.pendingAction).not.toBeNull();

      act(() => {
        result.current.confirmDiscard();
      });

      expect(result.current.pendingAction).toBeNull();
      expect(mockTree.selectFile).toHaveBeenCalledWith("b.go");
      expect(mockContent.fetchFile).toHaveBeenCalledWith("b.go");
      expect(result.current.content).toBe("");
    });

    it("cancelDiscard clears pendingAction without switching", () => {
      const fileData: FileReadData = {
        path: "a.go",
        content: "original",
        size: 8,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("modified");
      });
      act(() => {
        result.current.handleFileSelect("b.go");
      });

      act(() => {
        result.current.cancelDiscard();
      });

      expect(result.current.pendingAction).toBeNull();
      expect(mockTree.selectFile).not.toHaveBeenCalled();
      expect(result.current.content).toBe("modified");
    });
  });

  describe("agent change", () => {
    it("resets content and pendingAction when agentName changes", () => {
      const fileData: FileReadData = {
        path: "a.go",
        content: "data",
        size: 4,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result, rerender } = renderHook(
        ({ agent }) => useFileEditor(agent, true),
        { initialProps: { agent: "agent-1" } },
      );

      act(() => {
        result.current.handleContentChange("dirty");
      });
      expect(result.current.content).toBe("dirty");

      rerender({ agent: "agent-2" });

      expect(result.current.content).toBe("");
      expect(result.current.pendingAction).toBeNull();
    });
  });

  describe("keyboard shortcuts", () => {
    it("Cmd+S triggers save when active and dirty", async () => {
      mockTree = createMockTree({ selectedPath: "f.go" });
      mockUseFileTree.mockReturnValue(mockTree);
      const fileData: FileReadData = {
        path: "f.go",
        content: "old",
        size: 3,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);
      mockWriteWorktreeFile.mockResolvedValueOnce(undefined);

      const { result } = renderHook(() => useFileEditor("agent-1", true));

      act(() => {
        result.current.handleContentChange("new");
      });

      await act(async () => {
        const event = new KeyboardEvent("keydown", {
          key: "s",
          metaKey: true,
          bubbles: true,
        });
        document.dispatchEvent(event);
        await flushPromises();
      });

      expect(mockWriteWorktreeFile).toHaveBeenCalled();
    });

    it("does not trigger save when isActive is false", () => {
      mockTree = createMockTree({ selectedPath: "f.go" });
      mockUseFileTree.mockReturnValue(mockTree);
      const fileData: FileReadData = {
        path: "f.go",
        content: "old",
        size: 3,
        binary: false,
      };
      mockContent = createMockFileContent({ fileData });
      mockUseFileContent.mockReturnValue(mockContent);

      const { result } = renderHook(() => useFileEditor("agent-1", false));

      act(() => {
        result.current.handleContentChange("new");
      });

      act(() => {
        const event = new KeyboardEvent("keydown", {
          key: "s",
          metaKey: true,
          bubbles: true,
        });
        document.dispatchEvent(event);
      });

      expect(mockWriteWorktreeFile).not.toHaveBeenCalled();
    });

    it("cleans up keydown listener on unmount", () => {
      const removeSpy = vi.spyOn(document, "removeEventListener");

      const { unmount } = renderHook(() => useFileEditor("agent-1", true));

      unmount();

      expect(removeSpy).toHaveBeenCalledWith("keydown", expect.any(Function));
      removeSpy.mockRestore();
    });
  });
});
