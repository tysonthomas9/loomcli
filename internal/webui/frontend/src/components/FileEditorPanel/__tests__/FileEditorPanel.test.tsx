/**
 * @vitest-environment jsdom
 */

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, cleanup, fireEvent } from "@testing-library/react";

// Mock useFileEditor to control all state
vi.mock("../useFileEditor", () => ({
  useFileEditor: vi.fn(),
}));

// Mock CodeMirrorEditor with a simple div
vi.mock("@/components/CodeMirrorEditor", () => ({
  CodeMirrorEditor: (props: {
    value: string;
    onChange?: (v: string) => void;
    language?: string;
    readOnly?: boolean;
  }) => (
    <div
      data-testid="codemirror-editor"
      data-value={props.value}
      data-language={props.language}
      data-readonly={props.readOnly || undefined}
    />
  ),
}));

import { useFileEditor } from "../useFileEditor";
import type { UseFileEditorReturn } from "../useFileEditor";
import type { UseFileTreeReturn, UseFileContentReturn } from "@/hooks";
import type { FileEntry, FileReadData } from "@/api/files";
import { FileEditorPanel } from "../FileEditorPanel";

const mockUseFileEditor = vi.mocked(useFileEditor);

function createMockTree(
  overrides?: Partial<UseFileTreeReturn>,
): UseFileTreeReturn {
  return {
    expanded: new Set([""] as string[]),
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

function createMockEditorReturn(
  overrides?: Partial<UseFileEditorReturn>,
): UseFileEditorReturn {
  return {
    tree: createMockTree(),
    fileContent: createMockFileContent(),
    content: "",
    language: undefined,
    isDirty: false,
    isSaving: false,
    pendingAction: null,
    handleFileSelect: vi.fn(),
    handleContentChange: vi.fn(),
    save: vi.fn(),
    confirmDiscard: vi.fn(),
    cancelDiscard: vi.fn(),
    ...overrides,
  };
}

function createFileEntry(name: string, isDir: boolean): FileEntry {
  return { name, is_dir: isDir, size: 100, mod_time: "2026-01-01T00:00:00Z" };
}

describe("FileEditorPanel", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    cleanup();
  });

  describe("rendering", () => {
    it("renders container with sidebar and editor area", () => {
      mockUseFileEditor.mockReturnValue(createMockEditorReturn());

      const { getByTestId, getByPlaceholderText, getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByTestId("file-editor-panel")).toBeDefined();
      expect(getByPlaceholderText("Filter files...")).toBeDefined();
      expect(getByText("Select a file to edit")).toBeDefined();
    });

    it("shows empty state when no file selected", () => {
      mockUseFileEditor.mockReturnValue(createMockEditorReturn());

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Select a file to edit")).toBeDefined();
    });

    it("shows file path and editor when file selected", () => {
      const fileData: FileReadData = {
        path: "src/main.go",
        content: "package main",
        size: 12,
        binary: false,
      };
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "src/main.go" }),
          fileContent: createMockFileContent({ fileData }),
          content: "package main",
          language: "go",
        }),
      );

      const { getByText, getByTestId } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("src/main.go")).toBeDefined();
      const editor = getByTestId("codemirror-editor");
      expect(editor.getAttribute("data-value")).toBe("package main");
      expect(editor.getAttribute("data-language")).toBe("go");
    });

    it("shows binary file message when file is binary", () => {
      const fileData: FileReadData = {
        path: "image.png",
        size: 1024,
        binary: true,
      };
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "image.png" }),
          fileContent: createMockFileContent({ fileData }),
        }),
      );

      const { queryByTestId, container } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(queryByTestId("codemirror-editor")).toBeNull();
      expect(container.textContent).toContain("Binary file");
    });

    it("shows dirty indicator when isDirty is true", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({
            fileData: { path: "f.go", content: "x", size: 1, binary: false },
          }),
          content: "modified",
          isDirty: true,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Modified")).toBeDefined();
    });

    it("shows Saving... on save button when isSaving", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({
            fileData: { path: "f.go", content: "x", size: 1, binary: false },
          }),
          isSaving: true,
          isDirty: true,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Saving...")).toBeDefined();
    });

    it("shows loading message in tree when loading", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ isLoading: true }),
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Loading...")).toBeDefined();
    });

    it("shows tree error when tree has error", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ error: "Failed to load" }),
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Failed to load")).toBeDefined();
    });

    it("shows file loading state", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({ isLoading: true }),
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("Loading file...")).toBeDefined();
    });

    it("shows file content error", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({ error: "File too large" }),
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByText("File too large")).toBeDefined();
    });
  });

  describe("file tree interaction", () => {
    it("clicking a file calls handleFileSelect", () => {
      const handleFileSelect = vi.fn();
      const entries: FileEntry[] = [createFileEntry("main.go", false)];
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({
            treeData: new Map([["", entries]]),
          }),
          handleFileSelect,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.click(getByText("main.go"));
      expect(handleFileSelect).toHaveBeenCalledWith("main.go");
    });

    it("clicking a directory calls tree.toggle", () => {
      const toggle = vi.fn();
      const entries: FileEntry[] = [createFileEntry("src", true)];
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({
            treeData: new Map([["", entries]]),
            toggle,
          }),
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.click(getByText("src"));
      expect(toggle).toHaveBeenCalledWith("src");
    });

    it("filter input calls tree.setFilterText", () => {
      const setFilterText = vi.fn();
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ setFilterText }),
        }),
      );

      const { getByPlaceholderText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.change(getByPlaceholderText("Filter files..."), {
        target: { value: "main" },
      });
      expect(setFilterText).toHaveBeenCalledWith("main");
    });
  });

  describe("discard dialog", () => {
    it("shows dialog when pendingAction is not null", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          pendingAction: { type: "switch", path: "b.go" },
        }),
      );

      const { getByTestId, getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(getByTestId("discard-dialog")).toBeDefined();
      expect(
        getByText("You have unsaved changes. Discard them?"),
      ).toBeDefined();
    });

    it("does not show dialog when pendingAction is null", () => {
      mockUseFileEditor.mockReturnValue(createMockEditorReturn());

      const { queryByTestId } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      expect(queryByTestId("discard-dialog")).toBeNull();
    });

    it("Discard Changes button calls confirmDiscard", () => {
      const confirmDiscard = vi.fn();
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          pendingAction: { type: "switch", path: "b.go" },
          confirmDiscard,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.click(getByText("Discard Changes"));
      expect(confirmDiscard).toHaveBeenCalled();
    });

    it("Keep Editing button calls cancelDiscard", () => {
      const cancelDiscard = vi.fn();
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          pendingAction: { type: "switch", path: "b.go" },
          cancelDiscard,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.click(getByText("Keep Editing"));
      expect(cancelDiscard).toHaveBeenCalled();
    });

    it("dialog has correct accessibility attributes", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          pendingAction: { type: "switch", path: "b.go" },
        }),
      );

      const { getByTestId } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      const dialog = getByTestId("discard-dialog");
      expect(dialog.getAttribute("role")).toBe("dialog");
      expect(dialog.getAttribute("aria-modal")).toBe("true");
    });
  });

  describe("save button", () => {
    it("is disabled when not dirty", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({
            fileData: { path: "f.go", content: "x", size: 1, binary: false },
          }),
          isDirty: false,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      const saveBtn = getByText("Save") as HTMLButtonElement;
      expect(saveBtn.disabled).toBe(true);
    });

    it("is disabled when saving", () => {
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({
            fileData: { path: "f.go", content: "x", size: 1, binary: false },
          }),
          isDirty: true,
          isSaving: true,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      const saveBtn = getByText("Saving...") as HTMLButtonElement;
      expect(saveBtn.disabled).toBe(true);
    });

    it("calls save on click when enabled", () => {
      const save = vi.fn();
      mockUseFileEditor.mockReturnValue(
        createMockEditorReturn({
          tree: createMockTree({ selectedPath: "f.go" }),
          fileContent: createMockFileContent({
            fileData: { path: "f.go", content: "x", size: 1, binary: false },
          }),
          isDirty: true,
          save,
        }),
      );

      const { getByText } = render(
        <FileEditorPanel agentName="agent-1" isActive={true} />,
      );

      fireEvent.click(getByText("Save"));
      expect(save).toHaveBeenCalled();
    });
  });
});
