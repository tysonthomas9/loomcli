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
  listFileCheckouts: vi.fn(() => Promise.resolve({ checkouts: [] })),
  listScopedDir: vi.fn(() => Promise.resolve({ path: "", entries: [] })),
  searchScopedFiles: vi.fn(() =>
    Promise.resolve({ results: [], limitHit: false }),
  ),
  scrollApplied: vi.fn(),
  fileMap: {} as Record<string, FileReadData>,
  headFileMap: {} as Record<string, FileReadData>,
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
  listFileCheckouts: mocks.listFileCheckouts,
  listScopedDir: mocks.listScopedDir,
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
    useWorkspaceContext: () => ({
      workspaceId: "ws-1",
      repos: [
        {
          name: "loomcli",
          path: "/tmp/loomcli",
          default_branch: "main",
          remote: "origin",
          groups: [],
        },
      ],
      agents: [
        {
          name: "atlas",
          repos: ["loomcli"],
          repo_groups: [],
          cross_repo: false,
        },
      ],
    }),
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

async function expandWorkspaceFiles(): Promise<void> {
  fireEvent.click(
    await screen.findByRole("button", { name: /^Workspace files$/ }),
  );
  await screen.findByLabelText("main.ts");
}

async function expandRepoRoot(): Promise<void> {
  fireEvent.click(await screen.findByRole("button", { name: /^loomcli$/ }));
  await screen.findByLabelText("main.ts");
}

describe("WorkspaceFileBrowser", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
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
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
        {
          kind: "repo",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
      ],
    });
    mocks.listScopedDir.mockImplementation(
      (_workspaceId: string, _scopeRef: unknown, path?: string) =>
        Promise.resolve({
          path: path ?? "",
          entries: path ? [] : mocks.rootEntries,
        }),
    );
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
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandRepoRoot();

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
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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

  it("moves a tree node through Move to... and guards self-targets", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Move to..." }));
    const dialog = await screen.findByRole("dialog", { name: /move to/i });
    fireEvent.click(within(dialog).getByRole("button", { name: "src" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

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
    fireEvent.contextMenu(screen.getByLabelText("src"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Move to..." }));
    const selfDialog = await screen.findByRole("dialog", {
      name: /move to/i,
    });

    expect(
      within(selfDialog).getByRole("button", { name: "src" }),
    ).toBeDisabled();
    expect(mocks.moveScopedPath).not.toHaveBeenCalled();
  });

  it("opens the in-file symbol palette with Cmd+Shift+O and jumps to the symbol line", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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

  it("toggles the Changes lens, aggregates checkout counts, and persists per workspace", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 2,
        },
        {
          kind: "repo",
          repo: "loomcli",
          exists: true,
          change_count: 3,
        },
      ],
    });
    mocks.gitStatusScoped.mockResolvedValue({ "main.ts": " M" });

    render(<WorkspaceFileBrowser mode="workspace" />);

    const changesTab = await screen.findByRole("tab", {
      name: /Changes\s+5/,
    });
    expect(screen.queryByText("Source Control")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^Workspace files$/ }),
    ).toBeInTheDocument();

    fireEvent.click(changesTab);

    expect(
      screen.queryByRole("button", { name: /^Workspace files$/ }),
    ).not.toBeInTheDocument();
    expect(await screen.findByText("atlas · loomcli · 2")).toBeInTheDocument();
    expect(
      screen.getByText("loomcli · shared checkout · 3"),
    ).toBeInTheDocument();
    expect(localStorage.getItem("loom:ws-1:file-explorer-lens")).toBe(
      "changes",
    );
  });

  it("shows an all-zero Changes empty state", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+0/ }));

    expect(
      await screen.findByText("No uncommitted changes across this workspace."),
    ).toBeInTheDocument();
  });

  it("opens a checkout-qualified unified diff from the Changes lens", async () => {
    mocks.fileMap["src/main.ts"] = {
      path: "src/main.ts",
      content: "console.log('changed')\n",
      size: 23,
      binary: false,
    };
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 1,
        },
        {
          kind: "repo",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
      ],
    });
    mocks.gitStatusScoped.mockImplementation(
      (_workspaceId: string, ref: { scope: string }) =>
        Promise.resolve(ref.scope === "agent" ? { "src/main.ts": " M" } : {}),
    );

    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+1/ }));
    expect(await screen.findByText("Modified")).toBeInTheDocument();
    expect(screen.getByText("src")).toBeInTheDocument();
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Open diff for src/main.ts (Modified)",
      }),
    );

    await waitFor(() => {
      expect(mocks.diffScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "agent", target: "atlas", repo: "loomcli" },
        "src/main.ts",
        "HEAD",
        undefined,
      );
    });
    expect(screen.getByText("atlas · loomcli")).toBeInTheDocument();
    expect(await screen.findByText("-old")).toBeInTheDocument();
    expect(screen.getByText("+new")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open file" }));
    expect(
      await screen.findByDisplayValue(/console\.log\('changed'\)/),
    ).toBeInTheDocument();
  });

  it("keeps deleted files diff-only in the Changes lens", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "repo",
          repo: "loomcli",
          exists: true,
          change_count: 1,
        },
      ],
    });
    mocks.gitStatusScoped.mockResolvedValue({ "gone.ts": " D" });

    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+1/ }));
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Open diff for gone.ts (Deleted)",
      }),
    );

    await waitFor(() => {
      expect(mocks.diffScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "repo", target: "loomcli" },
        "gone.ts",
        "HEAD",
        undefined,
      );
    });
    expect(screen.queryByRole("button", { name: "Open file" })).toBeNull();
  });

  it("toggles blame and opens the clicked commit diff", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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

  it("passes quick-diff gutter marks from HEAD to the visible editor", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

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
