/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
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
  diffScopedFile: vi.fn(() => Promise.resolve({ path: "main.ts", patch: "" })),
  blameScopedFile: vi.fn(() =>
    Promise.resolve({ path: "main.ts", skipped: false, lines: [] }),
  ),
  historyScopedFile: vi.fn(() =>
    Promise.resolve({ path: "main.ts", entries: [] }),
  ),
  gitStatusScoped: vi.fn(() => Promise.resolve({})),
  indexScopedFiles: vi.fn(() =>
    Promise.resolve({ paths: [] as string[], truncated: false }),
  ),
  searchScopedFiles: vi.fn(() =>
    Promise.resolve({ results: [], limitHit: false }),
  ),
  scrollApplied: vi.fn(),
  dndOnDragEnd: undefined as
    | ((event: {
        active: { data: { current?: unknown } };
        over?: { data: { current?: unknown } } | null;
      }) => void)
    | undefined,
  fileMap: {} as Record<string, FileReadData>,
  headFileMap: {} as Record<string, FileReadData>,
  rootEntries: [] as FileEntry[],
}));

vi.mock("@dnd-kit/core", async () => {
  const React = await import("react");
  return {
    DndContext: ({
      onDragEnd,
      children,
    }: {
      onDragEnd: NonNullable<typeof mocks.dndOnDragEnd>;
      children: React.ReactNode;
    }) => {
      mocks.dndOnDragEnd = onDragEnd;
      return <>{children}</>;
    },
    useDraggable: () => ({
      setNodeRef: vi.fn(),
      listeners: {},
      isDragging: false,
      transform: null,
    }),
    useDroppable: () => ({
      setNodeRef: vi.fn(),
      isOver: false,
    }),
    PointerSensor: function PointerSensor() {},
    useSensor: (sensor: unknown) => sensor,
    useSensors: (...sensors: unknown[]) => sensors,
  };
});

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
      onSymbolsChange,
      gitGutterMarks,
      blameEnabled,
      blameLines,
      onBlameCommitClick,
    }: {
      value: string;
      onChange?: (value: string) => void;
      readOnly?: boolean;
      scrollToLine?: number;
      scrollToLineKey?: number | string;
      onScrollToLineApplied?: () => void;
      onSymbolsChange?: (state: {
        symbols: Array<{ name: string; kind: string; line: number }>;
        trail: Array<{ name: string; kind: string; line: number }>;
      }) => void;
      gitGutterMarks?: Array<{ line: number; kind: string }>;
      blameEnabled?: boolean;
      blameLines?: Array<{ sha: string }>;
      onBlameCommitClick?: (sha: string) => void;
    }) => {
      React.useEffect(() => {
        if (value.includes("function jumpTarget")) {
          onSymbolsChange?.({
            symbols: [
              {
                name: "jumpTarget",
                kind: "function",
                line: 3,
              },
            ],
            trail: [
              {
                name: "jumpTarget",
                kind: "function",
                line: 3,
              },
            ],
          });
        } else {
          onSymbolsChange?.({ symbols: [], trail: [] });
        }
      }, [onSymbolsChange, value]);
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
        <div>
          <textarea
            data-testid="mock-codemirror"
            data-readonly={readOnly ? "true" : "false"}
            data-scroll-line={scrollToLine ?? ""}
            data-gutter-marks={JSON.stringify(gitGutterMarks ?? [])}
            data-blame-enabled={blameEnabled ? "true" : "false"}
            value={value}
            readOnly={readOnly}
            onChange={(event) => onChange?.(event.target.value)}
          />
          {blameEnabled && blameLines?.[0] && (
            <button
              type="button"
              onClick={() => onBlameCommitClick?.(blameLines[0]?.sha ?? "")}
            >
              Blame {blameLines[0].sha}
            </button>
          )}
        </div>
      );
    },
  };
});

vi.mock("@/hooks/api", () => ({
  deleteScopedPath: mocks.deleteScopedPath,
  blameScopedFile: mocks.blameScopedFile,
  diffScopedFile: mocks.diffScopedFile,
  gitStatusScoped: mocks.gitStatusScoped,
  historyScopedFile: mocks.historyScopedFile,
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
    useEventContext: () => ({
      state: "connected",
      reconnectAttempts: 0,
      lastError: null,
      isConnected: true,
      subscribe: () => () => {},
      retryNow: vi.fn(),
      disconnect: vi.fn(),
    }),
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
    mocks.dndOnDragEnd = undefined;
    mocks.rootEntries = [
      entry("main.ts"),
      entry("symbols.ts"),
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
      "symbols.ts": {
        path: "symbols.ts",
        content: "const before = true;\n\nfunction jumpTarget() {}\n",
        size: 47,
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
    mocks.headFileMap = {
      "main.ts": {
        path: "main.ts",
        content: "console.log('old')\n",
        size: 19,
        binary: false,
      },
      "symbols.ts": mocks.fileMap["symbols.ts"],
    };
    mocks.readScopedFile.mockImplementation(
      (_, __, path: string, rev?: string) =>
        Promise.resolve(
          (rev === "HEAD" ? mocks.headFileMap[path] : mocks.fileMap[path]) ??
            mocks.fileMap[path],
        ),
    );
    mocks.diffScopedFile.mockResolvedValue({
      path: "main.ts",
      patch:
        "diff --git a/main.ts b/main.ts\n--- a/main.ts\n+++ b/main.ts\n@@ -1 +1 @@\n-old\n+new\n",
    });
    mocks.blameScopedFile.mockResolvedValue({
      path: "main.ts",
      skipped: false,
      lines: [
        {
          line: 1,
          lines: 1,
          sha: "abc1234",
          author: "Test User",
          time: "2026-01-01T00:00:00Z",
          summary: "initial",
        },
      ],
    });
    mocks.historyScopedFile.mockResolvedValue({
      path: "main.ts",
      entries: [],
    });
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
    mocks.gitStatusScoped.mockResolvedValue({});
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

  it("moves a dragged tree node onto a folder and guards self-drop", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    await waitFor(() => expect(mocks.dndOnDragEnd).toBeDefined());
    mocks.dndOnDragEnd?.({
      active: {
        data: { current: { type: "file-tree-node", path: "main.ts" } },
      },
      over: { data: { current: { type: "file-tree-folder", path: "src" } } },
    });

    await waitFor(() => {
      expect(mocks.moveScopedPath).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "src/main.ts",
        false,
      );
    });

    mocks.moveScopedPath.mockClear();
    mocks.dndOnDragEnd?.({
      active: { data: { current: { type: "file-tree-node", path: "src" } } },
      over: { data: { current: { type: "file-tree-folder", path: "src" } } },
    });

    expect(mocks.moveScopedPath).not.toHaveBeenCalled();
  });

  it("opens the in-file symbol palette with Cmd+Shift+O and jumps to the symbol line", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(screen.getByLabelText("symbols.ts"));
    await screen.findByDisplayValue(/function jumpTarget/);

    fireEvent.keyDown(window, { key: "o", metaKey: true, shiftKey: true });
    const dialog = await screen.findByRole("dialog", {
      name: "Go to symbol in file",
    });
    expect(await within(dialog).findByText("jumpTarget")).toBeInTheDocument();
    fireEvent.keyDown(dialog, { key: "Enter" });

    await waitFor(() => {
      expect(mocks.scrollApplied).toHaveBeenCalledWith(
        expect.objectContaining({ requested: 3, applied: 3 }),
      );
    });
  });

  it("opens a unified diff editor from the SCM panel", async () => {
    mocks.gitStatusScoped.mockResolvedValue({ "main.ts": " M" });

    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(
      await screen.findByRole("button", {
        name: "Open diff for main.ts (M)",
      }),
    );

    await waitFor(() => {
      expect(mocks.diffScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "HEAD",
        undefined,
      );
    });
    expect(await screen.findByText("-old")).toBeInTheDocument();
    expect(screen.getByText("+new")).toBeInTheDocument();
  });

  it("toggles blame and opens the clicked commit diff", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(screen.getByLabelText("main.ts"));
    await screen.findByDisplayValue(/console\.log/);
    fireEvent.click(screen.getByRole("button", { name: "Toggle blame" }));

    await waitFor(() => {
      expect(mocks.blameScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
      );
    });
    expect(
      await screen.findByRole("button", { name: "Blame abc1234" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Blame abc1234" }));

    await waitFor(() => {
      expect(mocks.diffScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "abc1234^",
        "abc1234",
      );
    });
  });

  it("shows Timeline commits and saves, and restores a browser save", async () => {
    mocks.historyScopedFile.mockResolvedValue({
      path: "main.ts",
      entries: [
        {
          kind: "commit",
          sha: "def5678",
          author: "Test User",
          time: "2026-01-02T00:00:00Z",
          summary: "commit summary",
        },
        {
          kind: "save",
          id: "save-1",
          author: "browser",
          time: "2026-01-03T00:00:00Z",
          summary: "Browser save",
          content: "previous\n",
        },
      ],
    });

    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(screen.getByLabelText("main.ts"));
    expect(await screen.findByText("commit summary")).toBeInTheDocument();
    fireEvent.click(screen.getByText("Browser save"));

    expect(
      await screen.findByRole("button", { name: "Restore" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));

    await waitFor(() => {
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "previous\n",
      );
    });
  });

  it("groups SCM changes by checkout and status", async () => {
    mocks.gitStatusScoped.mockResolvedValue({
      "repo-a/src/a.ts": " M",
      "repo-a/staged.ts": "A ",
      "repo-a/conflict.txt": "UU",
      "worktrees/repo-a/agent-a/b.ts": "??",
    });

    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    expect(await screen.findByText("repo-a")).toBeInTheDocument();
    expect(screen.getByText("worktrees/repo-a/agent-a")).toBeInTheDocument();
    expect(screen.getByText("Merge conflicts")).toBeInTheDocument();
    expect(screen.getByText("Staged")).toBeInTheDocument();
    expect(screen.getByText("Changes")).toBeInTheDocument();
    expect(screen.getByText("Untracked")).toBeInTheDocument();
  });

  it("passes quick-diff gutter marks from HEAD to the visible editor", async () => {
    render(<WorkspaceFileBrowser scopeRef={{ scope: "workspace" }} />);

    fireEvent.click(screen.getByLabelText("main.ts"));
    const editor = await screen.findByTestId("mock-codemirror");

    await waitFor(() => {
      const marks = JSON.parse(
        editor.getAttribute("data-gutter-marks") ?? "[]",
      ) as Array<{ kind: string }>;
      expect(marks.some((mark) => mark.kind === "changed")).toBe(true);
    });
  });
});
