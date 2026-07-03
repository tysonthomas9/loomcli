/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { FileEntry, FileReadData } from "@/api/workspace";

const mocks = vi.hoisted(() => ({
  showToast: vi.fn(),
  loadDir: vi.fn(() => Promise.resolve()),
  revealPath: vi.fn(() => Promise.resolve()),
  writeScopedFile: vi.fn(() => Promise.resolve()),
  mkdirScoped: vi.fn(() => Promise.resolve()),
  moveScopedPath: vi.fn(() => Promise.resolve()),
  deleteScopedPath: vi.fn(() => Promise.resolve()),
  readScopedFile: vi.fn(() => Promise.resolve({})),
  indexScopedFiles: vi.fn(() =>
    Promise.resolve({ paths: [] as string[], truncated: false }),
  ),
  searchScopedFiles: vi.fn(() =>
    Promise.resolve({ results: [], limitHit: false }),
  ),
  scrollApplied: vi.fn(),
  fileMap: {} as Record<string, FileReadData>,
  rootEntries: [] as FileEntry[],
}));

vi.mock("@/components/CodeMirrorEditor", async () => {
  const React = await import("react");
  return {
    CodeMirrorEditor: ({
      value,
      onChange,
      readOnly,
      scrollToLine,
      scrollToLineKey,
      onScrollToLineApplied,
    }: {
      value: string;
      onChange?: (value: string) => void;
      readOnly?: boolean;
      scrollToLine?: number;
      scrollToLineKey?: number | string;
      onScrollToLineApplied?: () => void;
    }) => {
      React.useEffect(() => {
        if (!scrollToLine) return;
        const lineCount = value.split("\n").length;
        mocks.scrollApplied({
          requested: scrollToLine,
          applied: Math.min(Math.max(1, Math.floor(scrollToLine)), lineCount),
          value,
        });
        onScrollToLineApplied?.();
        // Match CodeMirrorEditor: scrolling runs for target/key changes, not
        // merely because the document value changes later.
        // eslint-disable-next-line react-hooks/exhaustive-deps
      }, [scrollToLine, scrollToLineKey]);
      return (
        <textarea
          data-testid="mock-codemirror"
          data-readonly={readOnly ? "true" : "false"}
          data-scroll-line={scrollToLine ?? ""}
          value={value}
          readOnly={readOnly}
          onChange={(event) => onChange?.(event.target.value)}
        />
      );
    },
  };
});

vi.mock("@/hooks/api", () => ({
  deleteScopedPath: mocks.deleteScopedPath,
  indexScopedFiles: mocks.indexScopedFiles,
  mkdirScoped: mocks.mkdirScoped,
  moveScopedPath: mocks.moveScopedPath,
  readScopedFile: mocks.readScopedFile,
  searchScopedFiles: mocks.searchScopedFiles,
  writeScopedFile: mocks.writeScopedFile,
}));

vi.mock("@/hooks", async () => {
  const React = await import("react");
  const stores = await import("@/stores");
  return {
    FileBrowserStoreProvider: stores.FileBrowserStoreProvider,
    fileBrowserTabsStorageKey: stores.fileBrowserTabsStorageKey,
    useFileBrowserStore: stores.useFileBrowserStore,
    useFileBrowserStoreInstance: stores.useFileBrowserStoreInstance,
    useWorkspaceContext: () => ({ workspaceId: "ws-1" }),
    useToast: () => ({ showToast: mocks.showToast }),
    useFileTree: vi.fn(),
    useFileContent: vi.fn(),
    useScopedFileTree: () => ({
      expanded: new Set([""]),
      treeData: new Map<string, FileEntry[]>([["", mocks.rootEntries]]),
      isLoading: false,
      error: null,
      filterText: "",
      debouncedFilterText: "",
      toggle: vi.fn(() => Promise.resolve()),
      loadDir: mocks.loadDir,
      revealPath: mocks.revealPath,
      setFilterText: vi.fn(),
      selectedPath: null,
      selectFile: vi.fn(),
      isWorkspaceTree: false,
    }),
    useScopedFileContent: () => {
      const [fileData, setFileData] = React.useState<FileReadData | null>(null);
      const [isLoading, setIsLoading] = React.useState(false);
      return {
        fileData,
        isLoading,
        error: null,
        fetchFile: async (path: string) => {
          setIsLoading(true);
          setFileData(mocks.fileMap[path] ?? null);
          setIsLoading(false);
        },
        clearFile: () => setFileData(null),
      };
    },
  };
});

import { WorkspaceFileBrowser } from "../WorkspaceFileBrowser";

function entry(name: string, isDir = false): FileEntry {
  return {
    name,
    is_dir: isDir,
    size: 1,
    mod_time: "2026-01-01T00:00:00Z",
  };
}

describe("WorkspaceFileBrowser", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    mocks.rootEntries = [
      entry("main.ts"),
      entry("large.txt"),
      entry("src", true),
    ];
    mocks.fileMap = {
      "main.ts": {
        path: "main.ts",
        content: "console.log('hi')\n",
        size: 18,
        binary: false,
      },
      "src/other.ts": {
        path: "src/other.ts",
        content: "export const other = true;\n",
        size: 27,
        binary: false,
      },
      "large.txt": {
        path: "large.txt",
        content: "preview",
        size: 2_000_000,
        binary: false,
        truncated: true,
      },
    };
    mocks.readScopedFile.mockImplementation((_, __, path: string) =>
      Promise.resolve(mocks.fileMap[path]),
    );
    mocks.indexScopedFiles.mockResolvedValue({
      paths: ["src/recent.ts", "src/other.ts"],
      truncated: false,
    });
    mocks.searchScopedFiles.mockResolvedValue({
      results: [
        {
          path: "main.ts",
          matches: [{ line: 2, col: 1, preview: "console.log('hi')" }],
        },
      ],
      limitHit: false,
    });
  });

  it("calls scoped CRUD APIs from tree context menu actions", async () => {
    render(
      <WorkspaceFileBrowser scopeRef={{ scope: "repo", target: "loomcli" }} />,
    );

    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "New File" }));
    const newFileInput = screen.getByLabelText("File name");
    fireEvent.change(newFileInput, { target: { value: "new.ts" } });
    fireEvent.keyDown(newFileInput, { key: "Enter" });

    await waitFor(() => {
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "repo", target: "loomcli" },
        "new.ts",
        "",
      );
    });

    fireEvent.contextMenu(screen.getByLabelText("src"));
    fireEvent.click(screen.getByRole("menuitem", { name: "New Folder" }));
    const newFolderInput = screen.getByLabelText("File name");
    fireEvent.change(newFolderInput, { target: { value: "nested" } });
    fireEvent.keyDown(newFolderInput, { key: "Enter" });

    await waitFor(() => {
      expect(mocks.mkdirScoped).toHaveBeenCalledWith(
        "ws-1",
        { scope: "repo", target: "loomcli" },
        "src/nested",
      );
    });

    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    const renameInput = screen.getByLabelText("File name");
    fireEvent.change(renameInput, { target: { value: "renamed.ts" } });
    fireEvent.keyDown(renameInput, { key: "Enter" });

    await waitFor(() => {
      expect(mocks.moveScopedPath).toHaveBeenCalledWith(
        "ws-1",
        { scope: "repo", target: "loomcli" },
        "main.ts",
        "renamed.ts",
      );
    });

    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    await waitFor(() => {
      expect(mocks.deleteScopedPath).toHaveBeenCalledWith(
        "ws-1",
        { scope: "repo", target: "loomcli" },
        "main.ts",
        false,
      );
    });
  });

  it("renders truncated files read-only with a banner", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(screen.getByLabelText("large.txt"));

    expect(
      await screen.findByText(/larger than the editable limit/i),
    ).toBeInTheDocument();
    expect(screen.getByTestId("mock-codemirror")).toHaveAttribute(
      "data-readonly",
      "true",
    );
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
  });

  it("opens Quick Open with Cmd+P and Enter opens the highlighted file", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.keyDown(window, { key: "p", metaKey: true });

    const dialog = await screen.findByRole("dialog", { name: "Quick open" });
    await waitFor(() => {
      expect(mocks.indexScopedFiles).toHaveBeenCalledWith("ws-1", {
        scope: "workspace",
      });
    });
    fireEvent.change(
      screen.getByRole("searchbox", { name: "Quick open file" }),
      {
        target: { value: "oth" },
      },
    );
    await screen.findByText("other.ts");
    fireEvent.keyDown(dialog, { key: "Enter" });

    expect(
      await screen.findByDisplayValue(/export const other/),
    ).toBeInTheDocument();
  });

  it("applies search result lines after newly opened file content loads", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.keyDown(window, { key: "f", metaKey: true, shiftKey: true });
    fireEvent.change(screen.getByLabelText("Search files"), {
      target: { value: "console" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    await waitFor(() => {
      expect(mocks.searchScopedFiles).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        expect.objectContaining({ query: "console" }),
      );
    });
    fireEvent.click(await screen.findByText("console.log('hi')"));

    await waitFor(() => {
      expect(mocks.scrollApplied).toHaveBeenCalledWith({
        requested: 2,
        applied: 2,
        value: "console.log('hi')\n",
      });
    });
    expect(mocks.scrollApplied).not.toHaveBeenCalledWith(
      expect.objectContaining({ applied: 1, value: "" }),
    );
  });

  it("previews replacements and writes each affected file through scoped PUT", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.keyDown(window, { key: "f", metaKey: true, shiftKey: true });
    fireEvent.change(screen.getByLabelText("Search files"), {
      target: { value: "console" },
    });
    fireEvent.change(screen.getByLabelText("Replace with"), {
      target: { value: "alert" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    await screen.findByText("console.log('hi')");
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));

    expect(
      await screen.findByText(/- console\.log\('hi'\)/),
    ).toBeInTheDocument();
    expect(screen.getByText(/\+ alert\.log\('hi'\)/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));

    await waitFor(() => {
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "alert.log('hi')\n",
      );
    });
  });
});
