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
  workspaceDataId: "ws-1",
  repos: [
    {
      name: "loomcli",
      path: "/tmp/loomcli",
      default_branch: "main",
      remote: "origin",
      groups: [] as string[],
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
  reconnectAttempts: 0,
  eventState: "connected" as "connected" | "reconnecting",
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
      // `workspace` is the polled payload the repo and agent lists come out of,
      // so its id is what says which workspace they describe.
      workspace: { id: mocks.workspaceDataId, repos: mocks.repos },
      repos: mocks.repos,
      agents: mocks.agents,
    }),
    useEventContext: () => ({
      state: mocks.eventState,
      reconnectAttempts: mocks.reconnectAttempts,
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

function storeWorkingCompareMode(): void {
  localStorage.setItem("loom:ws-1:file-explorer-compare-mode", "working");
}

describe("WorkspaceFileBrowser SSE bug replay", () => {
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
    mocks.workspaceDataId = "ws-1";
    mocks.repos = [
      {
        name: "loomcli",
        path: "/tmp/loomcli",
        default_branch: "main",
        remote: "origin",
        groups: [],
      },
    ];
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

  it("#650 a failed reconnect Git refresh does not report modified files as clean", async () => {
    storeWorkingCompareMode();
    mocks.reconnectAttempts = 0;
    mocks.eventState = "connected";
    mocks.listFileCheckouts.mockResolvedValue({
      checkouts: [
        { kind: "repo", repo: "loomcli", exists: true, change_count: 1 },
      ],
    });
    mocks.gitStatusScoped.mockResolvedValue({
      status: { "main.ts": " M" },
      partial: false,
      limit_hit: false,
      errors: [],
    });

    const view = render(<WorkspaceFileBrowser mode="workspace" />);
    fireEvent.click(await screen.findByRole("tab", { name: /Changes\s+1/ }));
    await screen.findByRole("button", { name: /Open diff for main\.ts/ });

    mocks.gitStatusScoped.mockRejectedValue(new Error("git status failed"));
    mocks.reconnectAttempts = 1;
    mocks.eventState = "reconnecting";
    view.rerender(<WorkspaceFileBrowser mode="workspace" />);
    mocks.reconnectAttempts = 0;
    mocks.eventState = "connected";
    view.rerender(<WorkspaceFileBrowser mode="workspace" />);
    await waitFor(() =>
      expect(mocks.gitStatusScoped.mock.calls.length).toBeGreaterThan(1),
    );
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    expect(
      screen.queryByRole("button", { name: /Open diff for main\.ts/ }),
      "failed reconnect refresh replaced known Git status with an empty clean status",
    ).toBeInTheDocument();
  });
});
