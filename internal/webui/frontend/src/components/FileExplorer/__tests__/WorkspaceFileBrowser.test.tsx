/**
 * @vitest-environment jsdom
 */

import "@testing-library/jest-dom";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  FileEntry,
  FileReadData,
  SkillCatalogGroup,
} from "@/api/workspace";

const mocks = vi.hoisted(() => ({
  showToast: vi.fn(),
  loadDir: vi.fn(() => Promise.resolve()),
  revealPath: vi.fn(() => Promise.resolve()),
  writeScopedFile: vi.fn(() => Promise.resolve()),
  mkdirScoped: vi.fn(() => Promise.resolve()),
  moveScopedPath: vi.fn(() => Promise.resolve()),
  deleteScopedPath: vi.fn(() => Promise.resolve()),
  readScopedFile: vi.fn(() => Promise.resolve({})),
  statScopedPath: vi.fn((_workspaceId, _scopeRef, path: string) =>
    Promise.resolve({
      path,
      is_dir: false,
      size: 1,
      mod_time: "2026-01-01T00:00:00Z",
      version: `version:${path}`,
    }),
  ),
  diffScopedFile: vi.fn(() => Promise.resolve({ path: "main.ts", patch: "" })),
  fetchDiffFiles: vi.fn(() => Promise.resolve([])),
  fetchDiffFile: vi.fn(() =>
    Promise.resolve({
      patch: "",
      is_binary: false,
      is_too_large: false,
      additions: 0,
      deletions: 0,
    }),
  ),
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
  capabilities: { read: true, write: true, sensitive: true },
  capabilitiesLoading: false,
  capabilitiesError: null as string | null,
  retryCapabilities: vi.fn(),
  invalidateSkills: vi.fn(),
  skillsCatalogStatus: "loaded" as "idle" | "loading" | "loaded" | "error",
  skillsCatalogRevision: 1,
  skillGroups: [] as SkillCatalogGroup[],
  skillIndexPaths: [] as string[],
  documentExternalConflict: null as {
    content: string;
    version: string;
    fileData: FileReadData;
  } | null,
  useExternal: vi.fn(),
  overwriteExternal: vi.fn(() => Promise.resolve(null)),
  registryDirtyPaths: [] as string[],
  registryDirtyKeys: new Set<string>(),
  registryListeners: new Set<() => void>(),
  registryRevision: 0,
  registryDiscard: vi.fn(),
  registryRefresh: vi.fn(() => Promise.resolve()),
  registryRetarget: vi.fn(),
  registryReset: vi.fn(),
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
  fetchDiffFile: mocks.fetchDiffFile,
  fetchDiffFiles: mocks.fetchDiffFiles,
  gitStatusScoped: mocks.gitStatusScoped,
  historyScopedFile: mocks.historyScopedFile,
  indexScopedFiles: mocks.indexScopedFiles,
  listFileCheckouts: mocks.listFileCheckouts,
  listScopedDir: mocks.listScopedDir,
  mkdirScoped: mocks.mkdirScoped,
  moveScopedPath: mocks.moveScopedPath,
  repairFileCheckout: mocks.repairFileCheckout,
  readScopedFile: mocks.readScopedFile,
  statScopedPath: mocks.statScopedPath,
  searchScopedFiles: mocks.searchScopedFiles,
  writeScopedFile: mocks.writeScopedFile,
}));

vi.mock("@/hooks", async () => {
  const React = await import("react");
  const stores = await import("@/stores");
  const documentKey = (
    workspaceId: string,
    explorerRef: unknown,
    path: string,
  ): string => {
    const ref = explorerRef as {
      kind?: string;
      checkout?: { scope?: string; target?: string; repo?: string };
      group?: { kind?: string; role?: string };
      scope?: string;
      target?: string;
      repo?: string;
    };
    const checkout = ref.kind === "checkout" ? ref.checkout : ref;
    const skills = ref.kind === "skills" ? ref.group : null;
    return [
      workspaceId,
      skills ? `skills:${skills.kind}` : (checkout?.scope ?? "workspace"),
      skills?.role ?? checkout?.target ?? "",
      checkout?.repo ?? "",
      path,
    ].join(":");
  };
  const emitRegistryRevision = () => {
    mocks.registryRevision += 1;
    for (const listener of mocks.registryListeners) listener();
  };
  const setRegistryDirty = (
    workspaceId: string,
    scopeRef: unknown,
    path: string,
    dirty: boolean,
  ) => {
    const key = documentKey(workspaceId, scopeRef, path);
    const had = mocks.registryDirtyKeys.has(key);
    if (dirty) {
      mocks.registryDirtyKeys.add(key);
    } else {
      mocks.registryDirtyKeys.delete(key);
    }
    if (had !== dirty) emitRegistryRevision();
  };
  const documentRegistry = {
    get: (ref: { workspaceId: string; ref: unknown; path: string }) => ({
      dirty: mocks.registryDirtyKeys.has(
        documentKey(ref.workspaceId, ref.ref, ref.path),
      ),
    }),
    dirtyPathsForPrefix: vi.fn(() => mocks.registryDirtyPaths),
    discard: mocks.registryDiscard,
    refresh: mocks.registryRefresh,
    resetPathPrefix: mocks.registryReset,
    retargetPathPrefix: mocks.registryRetarget,
  };
  const skillActions = {
    canEdit: (group: { kind: string }) => group.kind === "role",
    createSkill: vi.fn(),
    updateMetadata: vi.fn(),
    deleteSkill: vi.fn(),
    createFile: vi.fn(),
    deleteFile: vi.fn(),
    invalidate: mocks.invalidateSkills,
    listIndexPaths: () => mocks.skillIndexPaths,
  };
  return {
    FileCapabilitiesProvider: ({ children }: { children: React.ReactNode }) =>
      children,
    FileDocumentRegistryProvider: ({
      children,
    }: {
      children: React.ReactNode;
    }) => children,
    FileBrowserStoreProvider: stores.FileBrowserStoreProvider,
    agentFileBrowserTabsStorageKey: stores.agentFileBrowserTabsStorageKey,
    fileBrowserTabsStorageKey: stores.fileBrowserTabsStorageKey,
    skillsFileBrowserTabsStorageKey: stores.skillsFileBrowserTabsStorageKey,
    useFileBrowserStore: stores.useFileBrowserStore,
    useFileBrowserStoreInstance: stores.useFileBrowserStoreInstance,
    useFileDocumentRegistry: () => documentRegistry,
    useFileDocumentRegistryRevision: () =>
      React.useSyncExternalStore(
        (listener) => {
          mocks.registryListeners.add(listener);
          return () => {
            mocks.registryListeners.delete(listener);
          };
        },
        () => mocks.registryRevision,
        () => mocks.registryRevision,
      ),
    useFileCapabilities: () => ({
      capabilities: mocks.capabilities,
      isLoading: mocks.capabilitiesLoading,
      error: mocks.capabilitiesError,
      retry: mocks.retryCapabilities,
    }),
    useSkillCapabilities: () => ({
      status: "loaded",
      data: {
        can_edit_role_scope: true,
        workspace_scope: "read_only",
      },
      error: null,
      retry: vi.fn(),
    }),
    useSkillsCatalog: () => ({
      status: mocks.skillsCatalogStatus,
      revision: mocks.skillsCatalogRevision,
      groups: mocks.skillGroups,
      error: null,
      shadowedByRef: {},
      shadowsByRef: {},
      readOnlyRefs: new Set<string>(),
      retry: vi.fn(),
      invalidate: mocks.invalidateSkills,
    }),
    useSkillsActions: () => skillActions,
    // Real implementation, not a stub: the component depends on this holding a
    // reference steady across renders, and a pass-through would make the tests
    // exercise a component that re-fetches on every render.
    useStableByKey: <T,>(key: string, value: T): T => {
      const held = React.useRef<{ key: string; value: T } | null>(null);
      if (held.current === null || held.current.key !== key) {
        held.current = { key, value };
      }
      return held.current.value;
    },
    useSkillsTree: (
      _workspaceId: string,
      group: { kind: string; role?: string },
    ) => {
      const catalogGroup = mocks.skillGroups.find((candidate) =>
        group.kind === "workspace"
          ? candidate.scope === "workspace"
          : candidate.scope === "role" && candidate.role === group.role,
      );
      return {
        status: "loaded",
        revision: 1,
        groups: mocks.skillGroups,
        error: null,
        shadowedByRef: {},
        shadowsByRef: {},
        readOnlyRefs: new Set<string>(),
        retry: vi.fn(),
        invalidate: mocks.invalidateSkills,
        loader: vi.fn(),
        skills: catalogGroup?.skills ?? [],
        shadowed: new Set<string>(),
        shadows: new Set<string>(),
      };
    },
    useScopedFileTreeCore: () => ({
      expanded: new Set(["audit"]),
      treeData: new Map<string, FileEntry[]>([
        ["", [entry("audit", true)]],
        ["audit", [entry("SKILL.md")]],
      ]),
      selectedPath: null,
      isLoading: false,
      error: null,
      filterText: "",
      debouncedFilterText: "",
      toggle: vi.fn(() => Promise.resolve()),
      loadDir: vi.fn(() => Promise.resolve()),
      revealPath: vi.fn(() => Promise.resolve()),
      setFilterText: vi.fn(),
      selectFile: vi.fn(),
      isWorkspaceTree: false,
    }),
    useSkill: (
      _workspaceId: string,
      ref: { group: { kind: string; role?: string } } | null,
      name: string | null,
    ) => ({
      skill:
        ref && name
          ? (mocks.skillGroups
              .find((candidate) =>
                ref.group.kind === "workspace"
                  ? candidate.scope === "workspace"
                  : candidate.scope === "role" &&
                    candidate.role === ref.group.role,
              )
              ?.skills.find((skill) => skill.name === name) ?? null)
          : null,
      shadowedByRef: {},
      shadowsByRef: {},
    }),
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
    useFileDocument: (
      _workspaceId: string,
      _scopeRef: unknown,
      path: string,
    ) => {
      const [fileData, setFileData] = React.useState<FileReadData | null>(null);
      const [content, setContent] = React.useState("");
      const [baseContent, setBaseContent] = React.useState("");
      const [isLoading, setIsLoading] = React.useState(false);
      const [isSaving, setIsSaving] = React.useState(false);
      const refresh = async () => {
        setIsLoading(true);
        const next = mocks.fileMap[path] ?? null;
        setFileData(next);
        const nextContent = next?.content ?? "";
        setContent(nextContent);
        setBaseContent(nextContent);
        setRegistryDirty(_workspaceId, _scopeRef, path, false);
        setIsLoading(false);
      };
      return {
        fileData,
        content,
        dirty: content !== baseContent,
        isLoading,
        isSaving,
        error: null,
        externalConflict: mocks.documentExternalConflict,
        refresh,
        edit: (next: string) => {
          setContent(next);
          setRegistryDirty(_workspaceId, _scopeRef, path, next !== baseContent);
        },
        save: async () => {
          if (content === baseContent) return null;
          setIsSaving(true);
          const ref = _scopeRef as {
            kind?: string;
            checkout?: unknown;
          };
          await mocks.writeScopedFile(
            "ws-1",
            ref.kind === "checkout" ? ref.checkout : _scopeRef,
            path,
            content,
          );
          setBaseContent(content);
          setRegistryDirty(_workspaceId, _scopeRef, path, false);
          setIsSaving(false);
          return { success: true, version: "test-version" };
        },
        discard: () => {
          setContent(baseContent);
          setRegistryDirty(_workspaceId, _scopeRef, path, false);
        },
        useExternal: mocks.useExternal,
        overwriteExternal: mocks.overwriteExternal,
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

function storeWorkingCompareMode(): void {
  localStorage.setItem("loom:ws-1:file-explorer-compare-mode", "working");
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
    mocks.capabilities = { read: true, write: true, sensitive: true };
    mocks.capabilitiesLoading = false;
    mocks.capabilitiesError = null;
    mocks.skillsCatalogStatus = "loaded";
    mocks.skillsCatalogRevision = 1;
    mocks.skillGroups = [];
    mocks.skillIndexPaths = [];
    mocks.documentExternalConflict = null;
    mocks.registryDirtyPaths = [];
    mocks.registryDirtyKeys.clear();
    mocks.registryListeners.clear();
    mocks.registryRevision = 0;
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
      (_, __, path: string, rev?: string) => {
        const found =
          (rev === "HEAD" ? mocks.headFileMap[path] : mocks.fileMap[path]) ??
          mocks.fileMap[path];
        return Promise.resolve(
          found
            ? { ...found, version: found.version ?? `version:${path}` }
            : found,
        );
      },
    );
    mocks.diffScopedFile.mockResolvedValue({
      path: "main.ts",
      patch:
        "diff --git a/main.ts b/main.ts\n--- a/main.ts\n+++ b/main.ts\n@@ -1 +1 @@\n-old\n+new\n",
    });
    mocks.fetchDiffFiles.mockResolvedValue([]);
    mocks.fetchDiffFile.mockResolvedValue({
      patch:
        "diff --git a/main.ts b/main.ts\n--- a/main.ts\n+++ b/main.ts\n@@ -1 +1 @@\n-old\n+new\n",
      is_binary: false,
      is_too_large: false,
      additions: 1,
      deletions: 1,
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
    mocks.gitStatusScoped.mockResolvedValue({
      status: {},
      partial: false,
      limit_hit: false,
      errors: [],
    });
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
        { createOnly: true },
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
        false,
        "version:main.ts",
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
        "version:main.ts",
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

  it("rebuilds the Skills section Quick Open when the catalog loads", async () => {
    // Was a workspace-mode test back when skills hung off the Files explorer.
    // Skills only exist in the Skills section now, so the same rebuild-on-load
    // behaviour is pinned there.
    mocks.skillsCatalogStatus = "loading";
    const view = render(<WorkspaceFileBrowser mode="skills" />);
    fireEvent.keyDown(window, { key: "p", metaKey: true });
    await screen.findByRole("dialog", { name: "Quick open" });
    expect(screen.queryByText("SKILL.md")).toBeNull();

    mocks.skillsCatalogStatus = "loaded";
    mocks.skillsCatalogRevision = 2;
    mocks.skillIndexPaths = ["audit/SKILL.md"];
    view.rerender(<WorkspaceFileBrowser mode="skills" />);

    expect(await screen.findByText("SKILL.md")).toBeInTheDocument();
    // No checkout sits behind the Skills section, so Quick Open never indexes
    // one — offering workspace files here would open tabs this browser's tab
    // set is not allowed to keep.
    expect(mocks.indexScopedFiles).not.toHaveBeenCalled();
  });

  it("drops a skills tab saved by the old Files explorer", async () => {
    // A Files-section tab set written while skills still lived in this tree can
    // hold a skills ref. Skills are not reachable here any more, so the stale
    // tab is dropped — not silently re-homed into the Skills section's own tab
    // set, which the Files section has no business writing to.
    //
    // The catalog really does carry this role — so the tab is dropped because
    // the Files tree no longer admits skill refs, not because the role is gone.
    mocks.skillGroups = [
      {
        scope: "role",
        role: "reviewer",
        skills: [
          {
            name: "audit",
            scope: "role",
            role: "reviewer",
            description: "Audit the implementation",
            content_revision: "skill-v1",
            files: [],
            created_at: "2026-08-14T00:00:00Z",
            updated_at: "2026-08-14T00:00:00Z",
          },
        ],
      },
    ];
    localStorage.setItem(
      "loom:ws-1:file-browser-tabs:v3",
      JSON.stringify({
        v: 4,
        groups: [
          {
            tabs: [
              {
                ref: { kind: "checkout", checkout: { scope: "workspace" } },
                path: "main.ts",
              },
              {
                ref: {
                  kind: "skills",
                  group: { kind: "role", role: "reviewer" },
                },
                path: "audit/SKILL.md",
              },
            ],
            active: null,
          },
        ],
        mru: [],
      }),
    );

    render(<WorkspaceFileBrowser mode="workspace" />);

    const tabs = await screen.findByRole("tablist", { name: /Open files/ });
    expect(within(tabs).getByText("main.ts")).toBeInTheDocument();
    await waitFor(() =>
      expect(within(tabs).queryByText("SKILL.md")).toBeNull(),
    );
  });

  it("never offers skills in the Files section Quick Open", async () => {
    mocks.skillIndexPaths = ["audit/SKILL.md"];
    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.keyDown(window, { key: "p", metaKey: true });
    await screen.findByRole("dialog", { name: "Quick open" });
    await waitFor(() =>
      expect(mocks.indexScopedFiles).toHaveBeenCalledTimes(1),
    );

    expect(await screen.findByText("other.ts")).toBeInTheDocument();
    expect(screen.queryByText("SKILL.md")).toBeNull();
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

  it("discards registry drafts when a dirty tab switch is confirmed", async () => {
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    const editor = await screen.findByTestId("mock-codemirror");
    fireEvent.change(editor, { target: { value: "changed\n" } });
    fireEvent.click(screen.getByLabelText("symbols.ts"));

    await waitFor(() =>
      expect(mocks.registryDiscard).toHaveBeenCalledWith({
        workspaceId: "ws-1",
        ref: { kind: "checkout", checkout: { scope: "workspace" } },
        path: "main.ts",
      }),
    );
    expect(
      await screen.findByDisplayValue(/function jumpTarget/),
    ).toBeVisible();
    confirm.mockRestore();
  });

  it("agent mode renders only the selected agent roots and scopes Changes", async () => {
    storeWorkingCompareMode();
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
    mocks.gitStatusScoped.mockResolvedValue({
      status: { "main.ts": " M" },
      partial: false,
      limit_hit: false,
      errors: [],
    });

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

  it("agent mode search and replace uses the selected agent checkout", async () => {
    const agentRef = { scope: "agent", target: "atlas", repo: "loomcli" };
    mocks.writeScopedFile.mockResolvedValue({ success: true, version: "v2" });
    render(<WorkspaceFileBrowser mode="agent" agentName="atlas" />);

    fireEvent.keyDown(window, { key: "f", metaKey: true, shiftKey: true });
    fireEvent.change(screen.getByLabelText("Search files"), {
      target: { value: "console" },
    });
    fireEvent.change(screen.getByLabelText("Replace with"), {
      target: { value: "alert" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    await screen.findByText("console.log('hi')");
    await waitFor(() =>
      expect(mocks.searchScopedFiles).toHaveBeenCalledWith(
        "ws-1",
        agentRef,
        expect.objectContaining({ query: "console" }),
      ),
    );

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    await screen.findByText(/- console\.log/);
    expect(mocks.readScopedFile).toHaveBeenCalledWith(
      "ws-1",
      agentRef,
      "main.ts",
    );

    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        agentRef,
        "main.ts",
        "alert.log('hi')\n",
        { ifMatch: "version:main.ts" },
      ),
    );
    await waitFor(() =>
      expect(mocks.registryRefresh).toHaveBeenCalledWith({
        workspaceId: "ws-1",
        ref: { kind: "checkout", checkout: agentRef },
        path: "main.ts",
      }),
    );
    expect(mocks.searchScopedFiles).not.toHaveBeenCalledWith(
      "ws-1",
      { scope: "workspace" },
      expect.anything(),
    );
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
        { ifMatch: "version:main.ts" },
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
        "version:main.ts",
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
    storeWorkingCompareMode();
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
    mocks.gitStatusScoped.mockResolvedValue({
      status: { "main.ts": " M" },
      partial: false,
      limit_hit: false,
      errors: [],
    });

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
    storeWorkingCompareMode();
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
    mocks.gitStatusScoped.mockResolvedValue({
      status: { "main.ts": " M" },
      partial: false,
      limit_hit: false,
      errors: [],
    });

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
    storeWorkingCompareMode();
    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+0/ }));

    expect(
      await screen.findByText("No uncommitted changes across this workspace."),
    ).toBeInTheDocument();
  });

  it("shows the default branch change count while the Files lens is active", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
      ],
    });
    mocks.fetchDiffFiles.mockResolvedValue([
      {
        path: "src/main.ts",
        status: "M",
        additions: 4,
        deletions: 1,
      },
    ]);

    render(<WorkspaceFileBrowser mode="workspace" />);

    expect(
      await screen.findByRole("tab", { name: /Changes\s+1/ }),
    ).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "Files" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    expect(mocks.fetchDiffFiles).toHaveBeenCalledWith("ws-1", "atlas", "HEAD");
  });

  it("uses branch compare mode by default and opens agent diff API patches", async () => {
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        {
          kind: "agent",
          agent: "atlas",
          repo: "loomcli",
          exists: true,
          change_count: 0,
        },
      ],
    });
    mocks.fetchDiffFiles.mockResolvedValue([
      {
        path: "src/main.ts",
        status: "M",
        additions: 4,
        deletions: 1,
      },
    ]);

    render(<WorkspaceFileBrowser mode="workspace" />);

    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+0/ }));

    expect(await screen.findByText("atlas · loomcli · 1")).toBeInTheDocument();
    expect(
      screen.getByRole("tab", { name: "Branch vs main · 1" }),
    ).toBeInTheDocument();
    expect(mocks.fetchDiffFiles).toHaveBeenCalledWith("ws-1", "atlas", "HEAD");
    fireEvent.click(
      await screen.findByRole("button", {
        name: "Open diff for src/main.ts (Modified)",
      }),
    );

    await waitFor(() => {
      expect(mocks.fetchDiffFile).toHaveBeenCalledWith(
        "ws-1",
        "atlas",
        "src/main.ts",
        "HEAD",
      );
    });
    expect(mocks.diffScopedFile).not.toHaveBeenCalled();
  });

  it("opens a checkout-qualified unified diff from the Changes lens", async () => {
    storeWorkingCompareMode();
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
        Promise.resolve({
          status: ref.scope === "agent" ? { "src/main.ts": " M" } : {},
          partial: false,
          limit_hit: false,
          errors: [],
        }),
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
    storeWorkingCompareMode();
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
    mocks.gitStatusScoped.mockResolvedValue({
      status: { "gone.ts": " D" },
      partial: false,
      limit_hit: false,
      errors: [],
    });

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

  it("does not render the old Timeline sidebar section", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    expect(screen.queryByText("Timeline")).not.toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /^Workspace files$/ }),
    ).toBeInTheDocument();
  });

  it("unmounts History when the open panel has no file subject", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    expect(await screen.findByLabelText("File history")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close main.ts" }));

    await waitFor(() => {
      expect(screen.queryByLabelText("File history")).not.toBeInTheDocument();
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

  it("keeps viewer sessions read-only and hides every mutation entry point", async () => {
    mocks.capabilities = { read: true, write: false, sensitive: false };
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.click(screen.getByLabelText("main.ts"));
    expect(await screen.findByTestId("mock-codemirror")).toHaveAttribute(
      "data-readonly",
      "true",
    );
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();

    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    expect(screen.getByRole("menuitem", { name: "Copy Path" })).toBeVisible();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: "Rename" })).toBeNull();
    expect(
      screen.queryByRole("menuitem", { name: "Duplicate text file" }),
    ).toBeNull();

    fireEvent.keyDown(window, { key: "f", metaKey: true, shiftKey: true });
    expect(screen.getByLabelText("Search files")).toBeVisible();
    expect(screen.queryByLabelText("Replace with")).toBeNull();
    expect(screen.queryByRole("button", { name: "Preview" })).toBeNull();
  });

  it("shows capability loading and fail-closed retry states", async () => {
    mocks.capabilities = { read: true, write: false, sensitive: false };
    mocks.capabilitiesLoading = true;
    const { rerender } = render(<WorkspaceFileBrowser mode="workspace" />);
    expect(screen.getByText("Checking file permissions...")).toBeVisible();
    await screen.findByRole("button", { name: /^Workspace files$/ });

    act(() => {
      mocks.capabilitiesLoading = false;
      mocks.capabilitiesError = "network error";
      rerender(<WorkspaceFileBrowser mode="workspace" />);
    });
    expect(
      screen.getByText("File permissions unavailable. Editing is disabled."),
    ).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(mocks.retryCapabilities).toHaveBeenCalledTimes(1);
  });

  it("retains the delete dialog and drafts when the version is stale", async () => {
    mocks.deleteScopedPath.mockRejectedValueOnce(
      Object.assign(new Error("file changed"), { status: 412 }),
    );
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByRole("dialog")).toBeVisible();
    expect(mocks.registryReset).not.toHaveBeenCalled();
    expect(mocks.deleteScopedPath).toHaveBeenCalledWith(
      "ws-1",
      { scope: "workspace" },
      "main.ts",
      false,
      "version:main.ts",
    );
  });

  it("includes source and destination versions when overwriting a move", async () => {
    mocks.moveScopedPath
      .mockRejectedValueOnce(
        Object.assign(new Error("destination exists"), { status: 409 }),
      )
      .mockResolvedValueOnce(undefined);
    vi.spyOn(window, "confirm").mockReturnValue(true);
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Move to..." }));
    const dialog = await screen.findByRole("dialog", { name: /move to/i });
    fireEvent.click(within(dialog).getByRole("button", { name: "src" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(mocks.moveScopedPath).toHaveBeenCalledTimes(2));
    expect(mocks.moveScopedPath).toHaveBeenLastCalledWith(
      "ws-1",
      { scope: "workspace" },
      "main.ts",
      "src/main.ts",
      true,
      "version:main.ts",
      "version:src/main.ts",
    );
  });

  it("keeps both drafts when destination overwrite discard is canceled", async () => {
    mocks.registryDirtyPaths = ["src/main.ts"];
    mocks.moveScopedPath.mockRejectedValueOnce(
      Object.assign(new Error("destination exists"), { status: 409 }),
    );
    const confirm = vi
      .spyOn(window, "confirm")
      .mockReturnValueOnce(true)
      .mockReturnValueOnce(false);
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Move to..." }));
    const dialog = await screen.findByRole("dialog", { name: /move to/i });
    fireEvent.click(within(dialog).getByRole("button", { name: "src" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(2));
    expect(confirm).toHaveBeenNthCalledWith(1, "Overwrite src/main.ts?");
    expect(confirm).toHaveBeenNthCalledWith(
      2,
      "Discard unsaved changes in 1 destination file?",
    );
    expect(mocks.moveScopedPath).toHaveBeenCalledTimes(1);
    expect(mocks.registryRetarget).not.toHaveBeenCalled();
    expect(dialog).toBeVisible();
  });

  it("resets discarded destination registry drafts before retargeting an overwrite move", async () => {
    mocks.registryDirtyPaths = ["src/main.ts"];
    mocks.moveScopedPath
      .mockRejectedValueOnce(
        Object.assign(new Error("destination exists"), { status: 409 }),
      )
      .mockResolvedValueOnce(undefined);
    const confirm = vi
      .spyOn(window, "confirm")
      .mockReturnValueOnce(true)
      .mockReturnValueOnce(true);
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Move to..." }));
    const dialog = await screen.findByRole("dialog", { name: /move to/i });
    fireEvent.click(within(dialog).getByRole("button", { name: "src" }));
    fireEvent.click(within(dialog).getByRole("button", { name: "Move" }));

    await waitFor(() => expect(mocks.moveScopedPath).toHaveBeenCalledTimes(2));
    expect(mocks.registryReset).toHaveBeenCalledWith(
      "ws-1",
      { kind: "checkout", checkout: { scope: "workspace" } },
      "src/main.ts",
    );
    expect(mocks.registryRetarget).toHaveBeenCalledWith(
      "ws-1",
      { kind: "checkout", checkout: { scope: "workspace" } },
      "main.ts",
      "src/main.ts",
    );
    expect(mocks.registryReset.mock.invocationCallOrder[0]).toBeLessThan(
      mocks.registryRetarget.mock.invocationCallOrder[0],
    );
    confirm.mockRestore();
  });

  it("duplicates only complete text with deterministic create-only names", async () => {
    mocks.rootEntries.push(entry("main copy.ts"));
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Duplicate text file" }),
    );

    await waitFor(() =>
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main copy 2.ts",
        "console.log('hi')\n",
        { createOnly: true },
      ),
    );
  });

  it("does not offer duplicate for binary or truncated files", async () => {
    mocks.rootEntries.push(entry("image.bin"));
    mocks.fileMap["image.bin"] = {
      path: "image.bin",
      size: 4,
      binary: true,
      version: "binary-v1",
    };
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();

    fireEvent.contextMenu(screen.getByLabelText("image.bin"));
    await waitFor(() => expect(mocks.readScopedFile).toHaveBeenCalled());
    expect(
      screen.queryByRole("menuitem", { name: "Duplicate text file" }),
    ).toBeNull();
    fireEvent.contextMenu(screen.getByLabelText("large.txt"));
    await waitFor(() =>
      expect(mocks.readScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "large.txt",
      ),
    );
    expect(
      screen.queryByRole("menuitem", { name: "Duplicate text file" }),
    ).toBeNull();
  });

  it("reselects a duplicate name once after a create-only race", async () => {
    mocks.writeScopedFile
      .mockRejectedValueOnce(
        Object.assign(new Error("already exists"), { status: 412 }),
      )
      .mockResolvedValueOnce({ success: true, version: "created" });
    mocks.listScopedDir
      .mockResolvedValueOnce({ path: "", entries: mocks.rootEntries })
      .mockResolvedValueOnce({
        path: "",
        entries: [...mocks.rootEntries, entry("main copy.ts")],
      });
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Duplicate text file" }),
    );

    await waitFor(() => expect(mocks.writeScopedFile).toHaveBeenCalledTimes(2));
    expect(mocks.writeScopedFile).toHaveBeenLastCalledWith(
      "ws-1",
      { scope: "workspace" },
      "main copy 2.ts",
      "console.log('hi')\n",
      { createOnly: true },
    );
  });

  it("retargets shared dirty documents after an inline rename", async () => {
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Rename" }));
    const input = screen.getByLabelText("File name");
    fireEvent.change(input, { target: { value: "renamed.ts" } });
    fireEvent.keyDown(input, { key: "Enter" });

    await waitFor(() =>
      expect(mocks.registryRetarget).toHaveBeenCalledWith(
        "ws-1",
        { kind: "checkout", checkout: { scope: "workspace" } },
        "main.ts",
        "renamed.ts",
      ),
    );
  });

  it("checks registry-wide dirty documents before deleting", async () => {
    mocks.registryDirtyPaths = ["main.ts"];
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.contextMenu(screen.getByLabelText("main.ts"));
    fireEvent.click(screen.getByRole("menuitem", { name: "Delete" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(confirm).toHaveBeenCalledWith(
      "Discard unsaved changes in 1 open file?",
    );
    expect(mocks.deleteScopedPath).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByRole("dialog")).toBeVisible());
  });

  it("preserves replace conflicts and refreshes successful registry documents", async () => {
    mocks.writeScopedFile.mockRejectedValueOnce(
      Object.assign(new Error("file changed"), { status: 412 }),
    );
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
    await screen.findByText(/- console\.log/);
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    expect(
      await screen.findByText(/Conflicting previews were preserved/),
    ).toBeVisible();
    expect(screen.getByRole("button", { name: "Apply" })).toBeVisible();

    mocks.writeScopedFile.mockResolvedValueOnce({
      success: true,
      version: "replaced",
    });
    fireEvent.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() =>
      expect(mocks.registryRefresh).toHaveBeenCalledWith({
        workspaceId: "ws-1",
        ref: { kind: "checkout", checkout: { scope: "workspace" } },
        path: "main.ts",
      }),
    );
  });

  it("renders external conflict commands without clearing the draft", async () => {
    mocks.documentExternalConflict = {
      content: "external\n",
      version: "external-v2",
      fileData: {
        path: "main.ts",
        content: "external\n",
        size: 9,
        binary: false,
        version: "external-v2",
      },
    };
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.click(screen.getByLabelText("main.ts"));
    await screen.findByText("This file changed outside the editor.");

    fireEvent.click(screen.getByRole("button", { name: "Reload" }));
    expect(mocks.useExternal).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Overwrite" }));
    expect(mocks.overwriteExternal).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Compare" }));
    expect(await screen.findByText("External vs local draft")).toBeVisible();
    expect(screen.getByText("-external")).toBeVisible();
  });

  it("restores a commit conditionally against the current version", async () => {
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
    fireEvent.click(
      await screen.findByRole("button", { name: "Open at commit" }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));

    await waitFor(() =>
      expect(mocks.writeScopedFile).toHaveBeenCalledWith(
        "ws-1",
        { scope: "workspace" },
        "main.ts",
        "console.log('hi')\n",
        { ifMatch: "version:main.ts" },
      ),
    );
  });

  it("keeps the commit revision open when restore conflicts", async () => {
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
    mocks.writeScopedFile.mockRejectedValueOnce(
      Object.assign(new Error("file changed"), { status: 412 }),
    );
    render(<WorkspaceFileBrowser mode="workspace" />);
    await expandWorkspaceFiles();
    fireEvent.click(screen.getByLabelText("main.ts"));
    fireEvent.click(await screen.findByRole("button", { name: "History" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Open at commit" }),
    );
    fireEvent.click(await screen.findByRole("button", { name: "Restore" }));

    expect(await screen.findByText("file changed")).toBeVisible();
    expect(screen.getByRole("button", { name: "Restore" })).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Close revision" }),
    ).toBeVisible();
  });

  it("opens workspace skill documents read-only without using checkout file APIs", async () => {
    mocks.skillGroups = [
      {
        scope: "workspace",
        skills: [
          {
            name: "audit",
            scope: "workspace",
            description: "Audit the implementation",
            content_revision: "skill-v1",
            files: [],
            created_by: "operator",
            source: "loom skill update",
            created_at: "2026-08-14T00:00:00Z",
            updated_at: "2026-08-14T00:00:00Z",
          },
        ],
      },
    ];
    mocks.fileMap["audit/SKILL.md"] = {
      path: "audit/SKILL.md",
      content: "Review every changed seam.",
      size: 26,
      binary: false,
      version: "skill-v1",
    };
    render(<WorkspaceFileBrowser mode="skills" />);
    const skillsRoot = (await screen.findByText("Workspace")).closest("button");
    expect(skillsRoot).not.toBeNull();

    fireEvent.click(skillsRoot!);
    fireEvent.click(await screen.findByLabelText("SKILL.md"));

    expect(
      await screen.findByDisplayValue("Review every changed seam."),
    ).toHaveAttribute("data-readonly", "true");
    expect(screen.getByText("Audit the implementation")).toBeVisible();
    expect(screen.getByText(/Read-only.*loom skill update/)).toBeVisible();
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    // The Skills section touches no checkout API at all — not even git status.
    expect(mocks.readScopedFile).not.toHaveBeenCalled();
    expect(mocks.listScopedDir).not.toHaveBeenCalled();
    expect(mocks.gitStatusScoped).not.toHaveBeenCalled();
  });

  it("offers no checkout History for a skills tab", async () => {
    mocks.skillGroups = [
      {
        scope: "role",
        role: "reviewer",
        skills: [
          {
            name: "audit",
            scope: "role",
            role: "reviewer",
            description: "Audit the implementation",
            content_revision: "skill-v1",
            files: [],
            created_at: "2026-08-14T00:00:00Z",
            updated_at: "2026-08-14T00:00:00Z",
          },
        ],
      },
    ];
    mocks.fileMap["audit/SKILL.md"] = {
      path: "audit/SKILL.md",
      content: "Review every changed seam.",
      size: 26,
      binary: false,
      version: "skill-v1",
    };
    // Previously this switched from a checkout tab to a skills tab inside one
    // browser. That mix is unreachable now that each section keeps its own
    // roots and tab set, so what survives is the invariant it was really
    // guarding: a skills tab never grows checkout history affordances.
    render(<WorkspaceFileBrowser mode="skills" />);

    fireEvent.click((await screen.findByText("reviewer")).closest("button")!);
    fireEvent.click(await screen.findByLabelText("SKILL.md"));

    expect(
      await screen.findByDisplayValue("Review every changed seam."),
    ).toBeVisible();
    expect(screen.queryByRole("button", { name: "History" })).toBeNull();
    expect(screen.queryByLabelText("File history")).not.toBeInTheDocument();
    expect(screen.queryByText("No file selected.")).toBeNull();
  });
});
