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
  repairFileCheckout: vi.fn(() =>
    Promise.resolve({
      repaired: true,
      method: "repair",
      message: "Repaired checkout",
    }),
  ),
  listScopedDir: vi.fn(() => Promise.resolve({ path: "", entries: [] })),
  searchScopedFiles: vi.fn(() =>
    Promise.resolve({ results: [], limitHit: false }),
  ),
  scrollApplied: vi.fn(),
  agents: [
    {
      name: "atlas",
      repos: ["loomcli"],
      repo_groups: [],
      cross_repo: false,
    },
  ],
  fileMap: {} as Record<string, FileReadData>,
  headFileMap: {} as Record<string, FileReadData>,
  rootEntries: [] as FileEntry[],
  scopedTreeError: null as string | null,
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
  repairFileCheckout: mocks.repairFileCheckout,
  readScopedFile: mocks.readScopedFile,
  searchScopedFiles: mocks.searchScopedFiles,
  writeScopedFile: mocks.writeScopedFile,
}));

vi.mock("@/hooks", async () => {
  const React = await import("react");
  const stores = await import("@/stores");
  return {
    FileBrowserStoreProvider: stores.FileBrowserStoreProvider,
    agentFileBrowserTabsStorageKey: stores.agentFileBrowserTabsStorageKey,
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
      agents: mocks.agents,
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
      error: mocks.scopedTreeError,
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
    mocks.scopedTreeError = null;
    mocks.agents = [
      {
        name: "atlas",
        repos: ["loomcli"],
        repo_groups: [],
        cross_repo: false,
      },
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
    mocks.repairFileCheckout.mockResolvedValue({
      repaired: true,
      method: "repair",
      message: "Repaired checkout",
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

  it("opens, edits, saves, and guards discarding a dirty file", async () => {
    const confirmSpy = vi.spyOn(window, "confirm");
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    const editor = await screen.findByTestId("mock-codemirror");
    expect(editor).toHaveValue("console.log('hi')\n");

    fireEvent.change(editor, { target: { value: "changed\n" } });
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();

    confirmSpy.mockReturnValueOnce(false);
    fireEvent.click(screen.getByLabelText("symbols.ts"));
    expect(confirmSpy).toHaveBeenCalledWith(
      expect.stringContaining("Discard unsaved changes"),
    );
    expect(screen.getByTestId("mock-codemirror")).toHaveValue("changed\n");

    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => {
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "changed\n",
      );
    });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    });

    confirmSpy.mockRestore();
  });

  it("agent mode renders only the selected agent roots and scopes Changes", async () => {
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

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    expect(
      await screen.findByRole("button", { name: /atlas.*loomcli/ }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Agents")).not.toBeInTheDocument();
    expect(screen.queryByText("Repos")).not.toBeInTheDocument();
    expect(screen.queryByText("Workspace files")).not.toBeInTheDocument();
    expect(
      await screen.findByRole("tab", { name: /Changes\s+2/ }),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("tab", { name: /Changes\s+2/ }));
    expect(await screen.findByText("atlas · loomcli · 2")).toBeInTheDocument();
    expect(
      screen.queryByText("loomcli · shared checkout · 3"),
    ).not.toBeInTheDocument();
  });

  it("agent mode Quick Open uses agent checkout indexes with repo qualifiers", async () => {
    mocks.indexScopedFiles.mockResolvedValue({
      paths: ["src/agent-only.ts"],
      truncated: false,
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.keyDown(window, { key: "p", metaKey: true });

    await screen.findByRole("dialog", { name: "Quick open" });
    await waitFor(() => {
      expect(mocks.indexScopedFiles).toHaveBeenCalledWith("ws-1", {
        scope: "agent",
        target: "atlas",
        repo: "loomcli",
      });
    });
    expect(screen.getByText("atlas · loomcli · src")).toBeInTheDocument();
    expect(screen.getByText("agent-only.ts")).toBeInTheDocument();
    expect(mocks.indexScopedFiles).not.toHaveBeenCalledWith("ws-1", {
      scope: "workspace",
    });
  });

  it("keeps open file tabs scoped to the selected agent", async () => {
    mocks.agents = [
      {
        name: "atlas",
        repos: ["loomcli"],
        repo_groups: [],
        cross_repo: false,
      },
      {
        name: "nova",
        repos: ["loomcli"],
        repo_groups: [],
        cross_repo: false,
      },
    ];
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
          kind: "agent",
          agent: "nova",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
      ],
    });

    const { rerender } = render(
      <WorkspaceFileBrowser mode="agent" agentName="atlas" />,
    );
    fireEvent.click(
      await screen.findByRole("button", { name: /atlas.*loomcli/ }),
    );
    fireEvent.click(await screen.findByLabelText("main.ts"));
    expect(
      await screen.findByRole("tab", { name: "main.ts" }),
    ).toBeInTheDocument();
    expect(
      localStorage.getItem("loom:ws-1:file-browser-tabs:v3:agent:atlas"),
    ).toContain("main.ts");
    expect(localStorage.getItem("loom:ws-1:file-browser-tabs:v3")).toBeNull();

    rerender(<WorkspaceFileBrowser mode="agent" agentName="nova" />);

    expect(
      await screen.findByRole("button", { name: /nova.*loomcli/ }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("tab", { name: "main.ts" }),
    ).not.toBeInTheDocument();
  });

  it("does not open global file shortcuts when inactive", async () => {
    render(
      <WorkspaceFileBrowser mode="agent" agentName="atlas" isActive={false} />,
    );

    fireEvent.keyDown(window, { key: "p", metaKey: true });

    await waitFor(() => {
      expect(
        screen.queryByRole("dialog", { name: "Quick open" }),
      ).not.toBeInTheDocument();
    });
    expect(mocks.indexScopedFiles).not.toHaveBeenCalled();
  });

  it("renders in compact layout at narrow embedded widths", async () => {
    const { container } = render(
      <div style={{ width: 500 }}>
        <WorkspaceFileBrowser mode="agent" agentName="atlas" />
      </div>,
    );

    await waitFor(() => {
      expect(container.querySelector("[data-compact='true']")).toBeTruthy();
    });
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

  it("skips unavailable checkouts in Changes counts and shows a notice", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "repo",
          repo: "loomcli",
          exists: true,
          change_count: 2,
        },
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 3,
          status_error: true,
        },
        {
          kind: "repo",
          repo: "missing",
          exists: false,
          change_count: 5,
        },
      ],
    });
    mocks.gitStatusScoped.mockResolvedValue({ "main.ts": " M" });

    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+2/ }));

    expect(
      await screen.findByText("atlas · loomcli unavailable"),
    ).toBeVisible();
    expect(
      screen.getByText("loomcli · shared checkout · 2"),
    ).toBeInTheDocument();
    expect(screen.queryByText("atlas · loomcli · 3")).not.toBeInTheDocument();
  });

  it("shows an unavailable repair chip for status_error checkouts and still expands files", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    const repairButton = await screen.findByRole("button", {
      name: /Repair checkout for atlas loomcli: Git status unavailable/,
    });
    expect(repairButton).toHaveTextContent("Unavailable");

    fireEvent.click(await screen.findByRole("button", { name: /^atlas/ }));

    expect(await screen.findByLabelText("main.ts")).toBeInTheDocument();
    expect(
      screen.queryByText(/agent worktree .* not found/i),
    ).not.toBeInTheDocument();
  });

  it("expands exists:false checkouts to a friendly unavailable state", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: false,
          change_count: 0,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    expect(
      await screen.findByRole("button", {
        name: /Repair checkout for atlas loomcli: This checkout is not checked out/,
      }),
    ).toHaveTextContent("Not checked out");
    expect(
      screen.queryByText("This checkout is not checked out"),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/agent worktree .* not found/i),
    ).not.toBeInTheDocument();
  });

  it("renders the unavailable chip as a keyboard-focusable repair button", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    const repairButton = await screen.findByRole("button", {
      name: /Repair checkout for atlas loomcli: Git status unavailable/,
    });
    repairButton.focus();
    expect(repairButton).toHaveFocus();
  });

  it("repairs a status_error checkout from the unavailable chip", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.click(
      await screen.findByRole("button", {
        name: /Repair checkout for atlas loomcli: Git status unavailable/,
      }),
    );

    await waitFor(() =>
      expect(mocks.repairFileCheckout).toHaveBeenCalledWith("ws-1", {
        scope: "agent",
        target: "atlas",
        repo: "loomcli",
        force: false,
      }),
    );
    await waitFor(() =>
      expect(mocks.showToast).toHaveBeenCalledWith("Repaired checkout", {
        type: "success",
      }),
    );
    expect(mocks.listFileCheckouts.mock.calls.length).toBeGreaterThan(1);
    expect(mocks.gitStatusScoped).toHaveBeenCalled();
  });

  it("prompts for force and retries checkout repair with force", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });
    mocks.repairFileCheckout
      .mockResolvedValueOnce({
        repaired: false,
        method: "none",
        requires_force: true,
        message: "recreate required",
      })
      .mockResolvedValueOnce({
        repaired: true,
        method: "recreate",
        backup_path: "/tmp/atlas.broken-123",
        message: "recreated",
      });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.click(
      await screen.findByRole("button", {
        name: /Repair checkout for atlas loomcli: Git status unavailable/,
      }),
    );

    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "timestamped backup folder",
    );
    fireEvent.click(screen.getByRole("button", { name: "Recreate" }));

    await waitFor(() =>
      expect(mocks.repairFileCheckout).toHaveBeenCalledTimes(2),
    );
    expect(mocks.repairFileCheckout).toHaveBeenNthCalledWith(1, "ws-1", {
      scope: "agent",
      target: "atlas",
      repo: "loomcli",
      force: false,
    });
    expect(mocks.repairFileCheckout).toHaveBeenNthCalledWith(2, "ws-1", {
      scope: "agent",
      target: "atlas",
      repo: "loomcli",
      force: true,
    });
    await waitFor(() =>
      expect(mocks.showToast).toHaveBeenCalledWith("recreated", {
        type: "success",
      }),
    );
  });

  it("offers repair from the checkout row context menu", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.contextMenu(
      await screen.findByRole("button", { name: /^atlas/ }),
    );
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Repair checkout" }),
    );

    await waitFor(() => expect(mocks.repairFileCheckout).toHaveBeenCalled());
  });

  it("shows an inline repair error without dumping backend text into the tree", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });
    mocks.repairFileCheckout.mockRejectedValueOnce(
      new Error("fatal: raw backend stack"),
    );

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.click(
      await screen.findByRole("button", {
        name: /Repair checkout for atlas loomcli: Git status unavailable/,
      }),
    );

    expect(
      await screen.findByText("Repair failed for atlas loomcli."),
    ).toBeVisible();
    expect(screen.queryByText(/raw backend stack/)).not.toBeInTheDocument();
  });

  it("expands status_error checkouts even when repair is available", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
          status_error: true,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.click(await screen.findByRole("button", { name: /^atlas/ }));

    expect(await screen.findByLabelText("main.ts")).toBeInTheDocument();
    expect(
      screen.queryByText(/agent worktree .* not found/i),
    ).not.toBeInTheDocument();
  });

  it("keeps missing checkouts visually distinct from git status errors", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: false,
          change_count: 0,
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    expect(
      await screen.findByRole("button", {
        name: /This checkout is not checked out/,
      }),
    ).toBeVisible();
    expect(screen.queryByLabelText("main.ts")).not.toBeInTheDocument();
    expect(screen.queryByText("Unavailable")).not.toBeInTheDocument();
  });

  it("maps tree list failures to the friendly unavailable state", async () => {
    mocks.scopedTreeError = 'agent worktree "atlas" not found';

    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.click(await screen.findByRole("button", { name: /^atlas/ }));

    expect(
      await screen.findByText("This checkout is not checked out"),
    ).toBeVisible();
    expect(screen.queryByText('agent worktree "atlas" not found')).toBeNull();
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

  it("toggles the History panel and follows active tab changes", async () => {
    mocks.historyScopedFile.mockImplementation(
      (_workspaceId: string, _ref: unknown, path: string) =>
        Promise.resolve({
          path,
          entries: [
            {
              kind: "commit",
              sha: path === "symbols.ts" ? "feed123" : "def5678",
              author: "Test User",
              time: "2026-01-02T00:00:00Z",
              summary: `${path} commit`,
            },
          ],
        }),
    );

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    await screen.findByDisplayValue(/console\.log/);
    fireEvent.click(screen.getByRole("button", { name: "History" }));

    expect(await screen.findByLabelText("File history")).toBeInTheDocument();
    expect(screen.getByText("main.ts commit")).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText("symbols.ts"));
    expect(await screen.findByText("symbols.ts commit")).toBeInTheDocument();
    expect(
      within(screen.getByLabelText("File history")).getByText("symbols.ts"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "History" }));
    await waitFor(() => {
      expect(screen.queryByLabelText("File history")).not.toBeInTheDocument();
    });
  });

  it("opens commit diff and read-only file-at-rev from History", async () => {
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
      ],
    });

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(await screen.findByRole("button", { name: "View diff" }));

    await waitFor(() => {
      expect(mocks.diffScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "def5678^",
        "def5678",
      );
    });
    expect(
      screen.getByText("Workspace files · commit summary"),
    ).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Close diff" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Open at commit" }),
    );

    await waitFor(() => {
      expect(mocks.readScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "def5678",
      );
    });
    expect(
      await screen.findByText("Workspace files · def5678"),
    ).toBeInTheDocument();
  });

  it("collapses consecutive History saves and restores an expanded save", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    mocks.historyScopedFile.mockResolvedValue({
      path: "main.ts",
      entries: [
        {
          kind: "save",
          id: "save-1",
          author: "browser",
          time: "2026-01-03T00:00:00Z",
          summary: "Browser save",
          content: "previous 1\n",
        },
        {
          kind: "save",
          id: "save-2",
          author: "browser",
          time: "2026-01-03T00:01:00Z",
          summary: "Browser save",
          content: "previous 2\n",
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(await screen.findByRole("button", { name: /2 saves/ }));
    const restoreButtons = await screen.findAllByRole("button", {
      name: /Restore save from/,
    });
    fireEvent.click(restoreButtons[0]!);

    await waitFor(() => {
      expect(confirmSpy).toHaveBeenCalledWith(
        "Restore Workspace files: main.ts?",
      );
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "previous 1\n",
      );
    });
    confirmSpy.mockRestore();
  });

  it("refreshes the open editor buffer after restoring a History save", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(true);
    mocks.historyScopedFile.mockResolvedValue({
      path: "main.ts",
      entries: [
        {
          kind: "save",
          id: "save-restore",
          author: "browser",
          time: "2026-01-03T00:00:00Z",
          summary: "Browser save",
          content: "restored from history\n",
        },
      ],
    });
    mocks.writeScopedFile.mockImplementationOnce(
      (_workspaceId: string, _ref: unknown, path: string, content: string) => {
        mocks.fileMap[path] = {
          path,
          content,
          size: content.length,
          binary: false,
        };
        return Promise.resolve();
      },
    );

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    expect(
      await screen.findByDisplayValue(/console\.log\('hi'\)/),
    ).toBeInTheDocument();
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Restore save from/ }),
    );

    await waitFor(() => {
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "restored from history\n",
      );
    });
    await waitFor(() => {
      expect(screen.getByTestId("mock-codemirror")).toHaveValue(
        "restored from history\n",
      );
    });
    expect(mocks.showToast).toHaveBeenCalledWith("Restored main.ts", {
      type: "success",
    });
    confirmSpy.mockRestore();
  });

  it("warns that dirty open editor buffers will be replaced before restoring", async () => {
    const confirmSpy = vi.spyOn(window, "confirm").mockReturnValue(false);
    mocks.historyScopedFile.mockResolvedValue({
      path: "main.ts",
      entries: [
        {
          kind: "save",
          id: "save-dirty",
          author: "browser",
          time: "2026-01-03T00:00:00Z",
          summary: "Browser save",
          content: "previous content\n",
        },
      ],
    });

    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    const editor = await screen.findByTestId("mock-codemirror");
    fireEvent.change(editor, { target: { value: "unsaved edits\n" } });
    await waitFor(() => {
      expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
    });
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(
      await screen.findByRole("button", { name: /Restore save from/ }),
    );

    await waitFor(() => {
      expect(confirmSpy).toHaveBeenCalledWith(
        expect.stringContaining(
          "Unsaved edits in the open tab will be replaced.",
        ),
      );
    });
    expect(mocks.writeScopedFile).not.toHaveBeenCalled();
    confirmSpy.mockRestore();
  });

  it("does not render the old Timeline sidebar section", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    expect(screen.queryByText("Timeline")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^Workspace files$/ }),
    ).toBeInTheDocument();
  });

  it("shows an empty History state when the open panel has no file subject", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    expect(await screen.findByLabelText("File history")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close main.ts" }));

    expect(await screen.findByText("No file selected.")).toBeInTheDocument();
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
