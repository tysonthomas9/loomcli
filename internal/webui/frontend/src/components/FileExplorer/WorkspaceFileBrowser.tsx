import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type FormEvent,
  type MouseEvent,
} from "react";
import { useStore } from "zustand";

import { ErrorDisplay, LoadingSkeleton } from "@/components";
import { useFileEditorBuffer } from "@/components/FileEditorPanel";
import { ResizeHandle } from "@/components/ResizeHandle";
import type { FileEntry, FileScopeRef } from "@/api/workspace";
import {
  deleteScopedPath,
  gitStatusScoped,
  indexScopedFiles,
  mkdirScoped,
  moveScopedPath,
  readScopedFile,
  writeScopedFile,
} from "@/hooks/api";
import {
  useScopedFileContent,
  useScopedFileTree,
  useToast,
  useWorkspaceContext,
  useEventContext,
  FileBrowserStoreProvider,
  fileBrowserTabsStorageKey,
  useFileBrowserStoreInstance,
  type FileBrowserGroup,
} from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import {
  FileTree,
  type FileTreeInlineEdit,
  type FileTreeNodeInfo,
} from "./FileTree";
import { FileSearchPanel } from "./FileSearchPanel";
import { FileTabBar } from "./FileTabBar";
import { QuickOpenPalette } from "./QuickOpenPalette";
import { WorkspaceFilePane } from "./WorkspaceFilePane";
import { gitDecorationForStatus, resolveTreeDropMove } from "./gitDecorations";
import styles from "./FileExplorer.module.css";

const TREE_WIDTH_KEY = "loom:file-browser:tree-width";
const DELETE_FILE_SKIP_KEY = "file-browser-delete-files-without-confirm";
const DEFAULT_TREE_WIDTH = 320;
const MIN_TREE_WIDTH = 240;
const MAX_TREE_WIDTH = 400;

const DEFAULT_GROUP_WIDTH = 560;
const MIN_GROUP_WIDTH = 320;
const MAX_GROUP_WIDTH = 1100;
const QUICK_OPEN_STALE_MS = 10_000;

function clampTreeWidth(w: number): number {
  return Math.min(MAX_TREE_WIDTH, Math.max(MIN_TREE_WIDTH, w));
}

function getStoredTreeWidth(): number {
  try {
    const raw = localStorage.getItem(TREE_WIDTH_KEY);
    if (raw !== null) {
      const n = Number(raw);
      if (Number.isFinite(n) && n > 0) return clampTreeWidth(n);
    }
  } catch {
    // localStorage unavailable
  }
  return DEFAULT_TREE_WIDTH;
}

function storeTreeWidth(w: number): void {
  try {
    localStorage.setItem(TREE_WIDTH_KEY, String(w));
  } catch {
    // localStorage unavailable
  }
}

function basename(path: string): string {
  return path.split("/").pop() || path;
}

function dirname(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

function joinPath(parent: string, child: string): string {
  const cleanChild = child.replace(/^\/+|\/+$/g, "");
  return parent ? `${parent}/${cleanChild}` : cleanChild;
}

function pathMatchesPrefix(path: string, prefix: string): boolean {
  return path === prefix || path.startsWith(`${prefix}/`);
}

function isConflictError(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "status" in err &&
    (err as { status?: unknown }).status === 409
  );
}

function sortedEntries(entries: FileEntry[]): FileEntry[] {
  return [...entries].sort((a, b) => a.name.localeCompare(b.name));
}

function duplicateName(name: string, siblings: FileEntry[]): string {
  const taken = new Set(siblings.map((entry) => entry.name));
  const dot = name.lastIndexOf(".");
  const hasExt = dot > 0;
  const stem = hasExt ? name.slice(0, dot) : name;
  const ext = hasExt ? name.slice(dot) : "";
  let candidate = `${stem} copy${ext}`;
  let n = 2;
  while (taken.has(candidate)) {
    candidate = `${stem} copy ${n}${ext}`;
    n += 1;
  }
  return candidate;
}

interface FileBrowserProps {
  scopeRef: FileScopeRef;
}

interface ContextMenuState {
  node: FileTreeNodeInfo;
  x: number;
  y: number;
}

interface DeleteConfirmState {
  node: FileTreeNodeInfo;
}

interface LineTarget {
  line: number;
  token: number;
}

function OpenEditors({
  groups,
  dirty,
  gitStatus,
  collapsed,
  onToggle,
  onActivate,
}: {
  groups: FileBrowserGroup[];
  dirty: Record<string, boolean>;
  gitStatus: Record<string, string>;
  collapsed: boolean;
  onToggle: () => void;
  onActivate: (groupIndex: number, path: string) => void;
}) {
  const openCount = groups.reduce((sum, group) => sum + group.tabs.length, 0);
  return (
    <section className={styles.openEditors}>
      <button
        type="button"
        className={styles.openEditorsHeader}
        aria-expanded={!collapsed}
        onClick={onToggle}
      >
        <span
          className={`${styles.chevron} ${!collapsed ? styles.chevronExpanded : ""}`}
          aria-hidden="true"
        >
          <svg viewBox="0 0 16 16">
            <path
              d="M6 4l4 4-4 4"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.5"
            />
          </svg>
        </span>
        <span>Open Editors</span>
        <span className={styles.openEditorsCount}>{openCount}</span>
      </button>
      {!collapsed && (
        <div className={styles.openEditorsList}>
          {openCount === 0 ? (
            <div className={styles.openEditorsEmpty}>No open files</div>
          ) : (
            groups.map((group, groupIndex) => (
              <div key={groupIndex} className={styles.openEditorsGroup}>
                {groups.length > 1 && (
                  <div className={styles.openEditorsGroupLabel}>
                    Group {groupIndex + 1}
                  </div>
                )}
                {group.tabs.map((tab) => (
                  <OpenEditorItem
                    key={`${groupIndex}:${tab.path}`}
                    path={tab.path}
                    active={group.active === tab.path}
                    dirty={!!dirty[tab.path]}
                    status={gitStatus[tab.path]}
                    onClick={() => onActivate(groupIndex, tab.path)}
                  />
                ))}
              </div>
            ))
          )}
        </div>
      )}
    </section>
  );
}

function OpenEditorItem({
  path,
  active,
  dirty,
  status,
  onClick,
}: {
  path: string;
  active: boolean;
  dirty: boolean;
  status?: string | undefined;
  onClick: () => void;
}) {
  const decoration = gitDecorationForStatus(status);
  return (
    <button
      type="button"
      className={styles.openEditorItem}
      data-active={active || undefined}
      data-git-status-kind={decoration?.kind}
      title={path}
      onClick={onClick}
    >
      {dirty && <span className={styles.tabDirty} aria-hidden="true" />}
      <span className={styles.openEditorName}>{basename(path)}</span>
      <span className={styles.openEditorPath}>{dirname(path)}</span>
    </button>
  );
}

function DeleteConfirmDialog({
  node,
  onCancel,
  onConfirm,
}: {
  node: FileTreeNodeInfo;
  onCancel: () => void;
  onConfirm: (skipFutureFileConfirms: boolean) => void;
}) {
  const [skipFiles, setSkipFiles] = useState(false);
  return (
    <div className={styles.dialogOverlay}>
      <div className={styles.dialog} role="dialog" aria-modal="true">
        <p className={styles.dialogMessage}>
          Delete {node.isDir ? "folder" : "file"} {node.path}?
        </p>
        {!node.isDir && (
          <label className={styles.checkboxRow}>
            <input
              type="checkbox"
              checked={skipFiles}
              onChange={(event) => setSkipFiles(event.target.checked)}
            />
            <span>Do not ask again for files</span>
          </label>
        )}
        <div className={styles.dialogActions}>
          <button
            type="button"
            className={styles.secondaryButton}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.dangerButton}
            onClick={() => onConfirm(skipFiles)}
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

function ContextMenu({
  state,
  onNewFile,
  onNewFolder,
  onRename,
  onDelete,
  onDuplicate,
  onCopyPath,
}: {
  state: ContextMenuState;
  onNewFile: (node: FileTreeNodeInfo) => void;
  onNewFolder: (node: FileTreeNodeInfo) => void;
  onRename: (node: FileTreeNodeInfo) => void;
  onDelete: (node: FileTreeNodeInfo) => void;
  onDuplicate: (node: FileTreeNodeInfo) => void;
  onCopyPath: (node: FileTreeNodeInfo) => void;
}) {
  return (
    <div
      className={styles.contextMenu}
      style={{ left: state.x, top: state.y }}
      role="menu"
    >
      <button
        type="button"
        role="menuitem"
        onClick={() => onNewFile(state.node)}
      >
        New File
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => onNewFolder(state.node)}
      >
        New Folder
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => onRename(state.node)}
      >
        Rename
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => onDelete(state.node)}
      >
        Delete
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => onDuplicate(state.node)}
        disabled={state.node.isDir}
        title={
          state.node.isDir ? "Duplicate is available for files" : undefined
        }
      >
        Duplicate
      </button>
      <button
        type="button"
        role="menuitem"
        onClick={() => onCopyPath(state.node)}
      >
        Copy Path
      </button>
    </div>
  );
}

function EditorGroup({
  groupIndex,
  group,
  scopeRef,
  isActiveGroup,
  dirty,
  onSelectTab,
  onCloseTab,
  onSplitRight,
  onNavigate,
  onSaved,
  lineTarget,
  onLineTargetApplied,
}: {
  groupIndex: number;
  group: FileBrowserGroup;
  scopeRef: FileScopeRef;
  isActiveGroup: boolean;
  dirty: Record<string, boolean>;
  onSelectTab: (groupIndex: number, path: string) => void;
  onCloseTab: (groupIndex: number, path: string) => void;
  onSplitRight: (groupIndex: number) => void;
  onNavigate: (dirPath: string) => void;
  onSaved: (path: string) => void;
  lineTarget?: LineTarget | undefined;
  onLineTargetApplied: (path: string, token: number) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const store = useFileBrowserStoreInstance();
  const { fileData, isLoading, error, fetchFile, clearFile } =
    useScopedFileContent(scopeRef);
  const activePath = group.active;
  const pathDirty = activePath ? !!dirty[activePath] : false;
  const [searchOpen, setSearchOpen] = useState(false);

  useEffect(() => {
    setSearchOpen(false);
    if (activePath) {
      if (!pathDirty) {
        void fetchFile(activePath);
      }
    } else {
      clearFile();
    }
  }, [activePath, pathDirty, fetchFile, clearFile]);

  const writeFile = useCallback(
    (path: string, content: string) =>
      writeScopedFile(workspaceId, scopeRef, path, content),
    [workspaceId, scopeRef],
  );
  const setDirty = useCallback(
    (path: string, isDirty: boolean) =>
      store.getState().setDirty(path, isDirty),
    [store],
  );

  const canSave =
    !!activePath && !!fileData && !fileData.binary && !fileData.truncated;

  const editor = useFileEditorBuffer({
    path: activePath,
    fileData,
    isActive: isActiveGroup,
    canSave,
    writeFile,
    onDirtyChange: setDirty,
    onSaved: () => {
      if (activePath) onSaved(activePath);
    },
  });

  return (
    <section
      className={styles.editorGroup}
      data-active={isActiveGroup || undefined}
    >
      <FileTabBar
        tabs={group.tabs.map((tab) => tab.path)}
        activePath={activePath}
        dirtyPaths={dirty}
        groupLabel={`group ${groupIndex + 1}`}
        onSelect={(path) => onSelectTab(groupIndex, path)}
        onClose={(path) => onCloseTab(groupIndex, path)}
      />
      <WorkspaceFilePane
        path={activePath}
        fileData={fileData}
        isActive={isActiveGroup}
        isLoading={isLoading}
        error={error}
        content={editor.content}
        isDirty={editor.isDirty}
        isSaving={editor.isSaving}
        searchOpen={searchOpen}
        onContentChange={editor.handleContentChange}
        onSave={() => void editor.save()}
        onToggleSearch={() => setSearchOpen((open) => !open)}
        onSplitRight={() => onSplitRight(groupIndex)}
        onNavigate={onNavigate}
        lineTarget={lineTarget}
        onLineTargetApplied={onLineTargetApplied}
      />
    </section>
  );
}

function FileBrowserInner({ scopeRef }: FileBrowserProps) {
  const {
    expanded,
    treeData,
    isLoading,
    error,
    filterText,
    debouncedFilterText,
    toggle,
    loadDir,
    revealPath,
    setFilterText,
  } = useScopedFileTree(scopeRef);

  const { workspaceId } = useWorkspaceContext();
  const eventContext = useEventContext();
  const { showToast } = useToast();
  const store = useFileBrowserStoreInstance();
  const groups = useStore(store, (s) => s.groups);
  const activeGroup = useStore(store, (s) => s.activeGroup);
  const dirty = useStore(store, (s) => s.dirty);
  const mru = useStore(store, (s) => s.mru);

  const [treeWidth, setTreeWidth] = useState<number>(getStoredTreeWidth);
  const [splitLeftWidth, setSplitLeftWidth] = useState(DEFAULT_GROUP_WIDTH);
  const [jumpText, setJumpText] = useState<string>("");
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);
  const [lineTargets, setLineTargets] = useState<Record<string, LineTarget>>(
    {},
  );
  const [openEditorsCollapsed, setOpenEditorsCollapsed] = useState(false);
  const [quickOpenOpen, setQuickOpenOpen] = useState(false);
  const [quickOpenPaths, setQuickOpenPaths] = useState<string[]>([]);
  const [quickOpenTruncated, setQuickOpenTruncated] = useState(false);
  const [quickOpenFetchedAt, setQuickOpenFetchedAt] = useState(0);
  const [quickOpenLoading, setQuickOpenLoading] = useState(false);
  const [quickOpenError, setQuickOpenError] = useState<string | null>(null);
  const [gitStatus, setGitStatus] = useState<Record<string, string>>({});
  const [searchPanelOpen, setSearchPanelOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [inlineEdit, setInlineEdit] = useState<FileTreeInlineEdit | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<DeleteConfirmState | null>(
    null,
  );
  const inlineCommitKeyRef = useRef<string | null>(null);
  const reconnectAttemptsRef = useRef(eventContext.reconnectAttempts);

  const activePath = groups[activeGroup]?.active ?? groups[0]?.active ?? null;

  const markIndexStale = useCallback(() => {
    setQuickOpenFetchedAt(0);
  }, []);

  const refreshGitStatus = useCallback(async () => {
    try {
      const status = await gitStatusScoped(workspaceId, scopeRef);
      setGitStatus(status);
    } catch {
      setGitStatus({});
    }
  }, [scopeRef, workspaceId]);

  const fetchQuickOpenIndex = useCallback(
    async (force = false) => {
      const now = Date.now();
      if (
        !force &&
        quickOpenFetchedAt > 0 &&
        now - quickOpenFetchedAt < QUICK_OPEN_STALE_MS
      ) {
        return;
      }
      setQuickOpenLoading(true);
      setQuickOpenError(null);
      try {
        const index = await indexScopedFiles(workspaceId, scopeRef);
        setQuickOpenPaths(index.paths);
        setQuickOpenTruncated(index.truncated);
        setQuickOpenFetchedAt(Date.now());
      } catch (err) {
        setQuickOpenError(err instanceof Error ? err.message : String(err));
      } finally {
        setQuickOpenLoading(false);
      }
    },
    [quickOpenFetchedAt, scopeRef, workspaceId],
  );

  useEffect(() => {
    if (!contextMenu) return;
    const close = () => setContextMenu(null);
    window.addEventListener("click", close);
    window.addEventListener("keydown", close);
    return () => {
      window.removeEventListener("click", close);
      window.removeEventListener("keydown", close);
    };
  }, [contextMenu]);

  useEffect(() => {
    if (activePath) {
      void revealPath(activePath).then(() => setScrollTarget(activePath));
    }
  }, [activePath, revealPath]);

  useEffect(() => {
    if (quickOpenOpen) {
      void fetchQuickOpenIndex();
    }
  }, [fetchQuickOpenIndex, quickOpenOpen]);

  useEffect(() => {
    if (!isLoading && !error) {
      void refreshGitStatus();
    }
  }, [error, isLoading, refreshGitStatus]);

  useEffect(() => {
    const handleFocus = () => {
      void refreshGitStatus();
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [refreshGitStatus]);

  useEffect(() => {
    const previous = reconnectAttemptsRef.current;
    reconnectAttemptsRef.current = eventContext.reconnectAttempts;
    if (
      eventContext.reconnectAttempts > 0 ||
      (previous > 0 && eventContext.state === "connected")
    ) {
      void refreshGitStatus();
    }
  }, [eventContext.reconnectAttempts, eventContext.state, refreshGitStatus]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      const mod = event.metaKey || event.ctrlKey;
      if (mod && !event.shiftKey && key === "p") {
        event.preventDefault();
        setQuickOpenOpen(true);
      } else if (mod && event.shiftKey && key === "f") {
        event.preventDefault();
        setSearchPanelOpen(true);
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  const refreshParents = useCallback(
    async (...paths: string[]) => {
      const parents = new Set(paths.map(dirname));
      await Promise.all([...parents].map((parent) => loadDir(parent)));
    },
    [loadDir],
  );

  const discardActiveIfNeeded = useCallback(
    (groupIndex: number, nextPath?: string): boolean => {
      const state = store.getState();
      const current = state.groups[groupIndex]?.active;
      if (!current || current === nextPath || !state.dirty[current])
        return true;
      const ok = window.confirm(`Discard unsaved changes in ${current}?`);
      if (!ok) return false;
      state.setDirty(current, false);
      return true;
    },
    [store],
  );

  const openFile = useCallback(
    (
      path: string,
      groupIndex = store.getState().activeGroup,
      lineNumber?: number,
    ) => {
      if (!discardActiveIfNeeded(groupIndex, path)) return;
      store.getState().openTab(path, groupIndex);
      if (lineNumber && lineNumber > 0) {
        setLineTargets((prev) => ({
          ...prev,
          [path]: { line: lineNumber, token: (prev[path]?.token ?? 0) + 1 },
        }));
      }
    },
    [discardActiveIfNeeded, store],
  );

  const handleLineTargetApplied = useCallback((path: string, token: number) => {
    setLineTargets((prev) => {
      if (prev[path]?.token !== token) return prev;
      const next = { ...prev };
      delete next[path];
      return next;
    });
  }, []);

  const selectTab = useCallback(
    (groupIndex: number, path: string) => {
      if (!discardActiveIfNeeded(groupIndex, path)) return;
      store.getState().activateTab(groupIndex, path);
    },
    [discardActiveIfNeeded, store],
  );

  const closeTab = useCallback(
    (groupIndex: number, path: string) => {
      if (store.getState().dirty[path]) {
        const ok = window.confirm(`Discard unsaved changes in ${path}?`);
        if (!ok) return;
        store.getState().setDirty(path, false);
      }
      store.getState().closeTab(groupIndex, path);
    },
    [store],
  );

  const splitRight = useCallback(
    (groupIndex: number) => {
      const path = store.getState().groups[groupIndex]?.active;
      if (!path) return;
      if (store.getState().dirty[path]) {
        const ok = window.confirm(
          `Discard unsaved changes in ${path} before splitting?`,
        );
        if (!ok) return;
        store.getState().setDirty(path, false);
      }
      store.getState().splitRight(path);
    },
    [store],
  );

  const handleSaved = useCallback(
    (path: string) => {
      markIndexStale();
      void refreshGitStatus();
      showToast("File saved", { type: "success" });
      void refreshParents(path);
    },
    [markIndexStale, refreshGitStatus, refreshParents, showToast],
  );

  const handleResizeDelta = useCallback((deltaPx: number) => {
    setTreeWidth((w) => {
      const next = clampTreeWidth(w + deltaPx);
      storeTreeWidth(next);
      return next;
    });
  }, []);

  const handleResizeReset = useCallback(() => {
    setTreeWidth(DEFAULT_TREE_WIDTH);
    storeTreeWidth(DEFAULT_TREE_WIDTH);
  }, []);

  const handleSplitResizeDelta = useCallback((deltaPx: number) => {
    setSplitLeftWidth((w) =>
      Math.min(MAX_GROUP_WIDTH, Math.max(MIN_GROUP_WIDTH, w + deltaPx)),
    );
  }, []);

  const handleJump = useCallback(
    (e: FormEvent) => {
      e.preventDefault();
      const target = jumpText.trim();
      if (!target) return;
      void revealPath(target).then(() => setScrollTarget(target));
      setJumpText("");
    },
    [jumpText, revealPath],
  );

  const navigateToDir = useCallback(
    (dirPath: string) => {
      void revealPath(dirPath).then(() => setScrollTarget(dirPath));
    },
    [revealPath],
  );

  const beginCreate = useCallback(
    (kind: "create-file" | "create-folder", node: FileTreeNodeInfo) => {
      setContextMenu(null);
      const parentPath = node.isDir ? node.path : dirname(node.path);
      setInlineEdit({
        kind,
        parentPath,
        value: kind === "create-file" ? "untitled.txt" : "new-folder",
        isDir: kind === "create-folder",
      });
    },
    [],
  );

  const beginRename = useCallback((node: FileTreeNodeInfo) => {
    setContextMenu(null);
    setInlineEdit({
      kind: "rename",
      parentPath: dirname(node.path),
      path: node.path,
      value: node.name,
      isDir: node.isDir,
    });
  }, []);

  const commitInlineEdit = useCallback(async () => {
    if (!inlineEdit) return;
    const value = inlineEdit.value.trim();
    if (!value) {
      setInlineEdit(null);
      return;
    }
    const commitKey = `${inlineEdit.kind}:${inlineEdit.path ?? inlineEdit.parentPath}:${value}`;
    if (inlineCommitKeyRef.current === commitKey) return;
    inlineCommitKeyRef.current = commitKey;
    setInlineEdit(null);
    try {
      if (inlineEdit.kind === "create-file") {
        const path = joinPath(inlineEdit.parentPath, value);
        await writeScopedFile(workspaceId, scopeRef, path, "");
        markIndexStale();
        void refreshGitStatus();
        await refreshParents(path);
        openFile(path);
        void revealPath(path).then(() => setScrollTarget(path));
      } else if (inlineEdit.kind === "create-folder") {
        const path = joinPath(inlineEdit.parentPath, value);
        await mkdirScoped(workspaceId, scopeRef, path);
        markIndexStale();
        void refreshGitStatus();
        await refreshParents(path);
        void revealPath(path).then(() => setScrollTarget(path));
      } else if (inlineEdit.path) {
        const nextPath = joinPath(inlineEdit.parentPath, value);
        if (nextPath !== inlineEdit.path) {
          await moveScopedPath(
            workspaceId,
            scopeRef,
            inlineEdit.path,
            nextPath,
          );
          store.getState().retargetPathPrefix(inlineEdit.path, nextPath);
          markIndexStale();
          void refreshGitStatus();
          await refreshParents(inlineEdit.path, nextPath);
          void revealPath(nextPath).then(() => setScrollTarget(nextPath));
        }
      }
    } catch (err) {
      showToast(err instanceof Error ? err.message : String(err), {
        type: "error",
      });
    } finally {
      inlineCommitKeyRef.current = null;
    }
  }, [
    inlineEdit,
    workspaceId,
    scopeRef,
    refreshParents,
    openFile,
    revealPath,
    store,
    showToast,
    markIndexStale,
    refreshGitStatus,
  ]);

  const dirtyTabsForPath = useCallback(
    (path: string): string[] =>
      Object.keys(store.getState().dirty).filter((tabPath) =>
        pathMatchesPrefix(tabPath, path),
      ),
    [store],
  );

  const performDelete = useCallback(
    async (node: FileTreeNodeInfo, skipFutureFileConfirms = false) => {
      const dirtyTabs = dirtyTabsForPath(node.path);
      if (dirtyTabs.length > 0) {
        const ok = window.confirm(
          `Discard unsaved changes in ${dirtyTabs.length} open file${dirtyTabs.length === 1 ? "" : "s"}?`,
        );
        if (!ok) return;
        for (const path of dirtyTabs) {
          store.getState().setDirty(path, false);
        }
      }
      try {
        await deleteScopedPath(workspaceId, scopeRef, node.path, node.isDir);
        markIndexStale();
        void refreshGitStatus();
        if (!node.isDir && skipFutureFileConfirms) {
          wsSet(workspaceId, DELETE_FILE_SKIP_KEY, "1");
        }
        store.getState().closePathPrefix(node.path);
        await refreshParents(node.path);
        showToast("Deleted", { type: "success" });
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      } finally {
        setDeleteConfirm(null);
      }
    },
    [
      dirtyTabsForPath,
      workspaceId,
      scopeRef,
      store,
      refreshParents,
      showToast,
      markIndexStale,
      refreshGitStatus,
    ],
  );

  const requestDelete = useCallback(
    (node: FileTreeNodeInfo) => {
      setContextMenu(null);
      const skipFileConfirm =
        !node.isDir && wsGet(workspaceId, DELETE_FILE_SKIP_KEY) === "1";
      if (skipFileConfirm) {
        void performDelete(node);
      } else {
        setDeleteConfirm({ node });
      }
    },
    [performDelete, workspaceId],
  );

  const duplicateFile = useCallback(
    async (node: FileTreeNodeInfo) => {
      setContextMenu(null);
      if (node.isDir) return;
      try {
        const data = await readScopedFile(workspaceId, scopeRef, node.path);
        if (data.binary || data.truncated) {
          showToast("Only complete text files can be duplicated", {
            type: "error",
          });
          return;
        }
        const parent = dirname(node.path);
        const entries = sortedEntries(treeData.get(parent) ?? []);
        const nextName = duplicateName(basename(node.path), entries);
        const nextPath = joinPath(parent, nextName);
        await writeScopedFile(
          workspaceId,
          scopeRef,
          nextPath,
          data.content ?? "",
        );
        markIndexStale();
        void refreshGitStatus();
        await refreshParents(nextPath);
        openFile(nextPath);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      }
    },
    [
      workspaceId,
      scopeRef,
      treeData,
      refreshParents,
      openFile,
      showToast,
      markIndexStale,
      refreshGitStatus,
    ],
  );

  const copyPath = useCallback(
    (node: FileTreeNodeInfo) => {
      setContextMenu(null);
      void navigator.clipboard?.writeText(node.path).catch(() => {
        showToast("Failed to copy path", { type: "error" });
      });
    },
    [showToast],
  );

  const handleContextMenu = useCallback(
    (node: FileTreeNodeInfo, event: MouseEvent<HTMLDivElement>) => {
      setContextMenu({ node, x: event.clientX, y: event.clientY });
    },
    [],
  );

  const handleMoveNode = useCallback(
    async (fromPath: string, targetFolderPath: string) => {
      const move = resolveTreeDropMove(fromPath, targetFolderPath);
      if (!move) return;
      const applyMove = async (overwrite: boolean) => {
        await moveScopedPath(
          workspaceId,
          scopeRef,
          move.from,
          move.to,
          overwrite,
        );
      };
      try {
        await applyMove(false);
      } catch (err) {
        if (!isConflictError(err)) {
          showToast(err instanceof Error ? err.message : String(err), {
            type: "error",
          });
          return;
        }
        const ok = window.confirm(`Overwrite ${move.to}?`);
        if (!ok) return;
        try {
          await applyMove(true);
        } catch (overwriteErr) {
          showToast(
            overwriteErr instanceof Error
              ? overwriteErr.message
              : String(overwriteErr),
            { type: "error" },
          );
          return;
        }
      }

      store.getState().retargetPathPrefix(move.from, move.to);
      markIndexStale();
      void refreshGitStatus();
      await refreshParents(move.from, move.to);
      void revealPath(move.to).then(() => setScrollTarget(move.to));
      showToast("Moved", { type: "success" });
    },
    [
      workspaceId,
      scopeRef,
      store,
      markIndexStale,
      refreshGitStatus,
      refreshParents,
      revealPath,
      showToast,
    ],
  );

  const selectedPath = activePath;

  return (
    <div className={styles.container}>
      <div
        className={styles.treePanel}
        style={{ ["--tree-width"]: `${treeWidth}px` } as CSSProperties}
      >
        {searchPanelOpen ? (
          <FileSearchPanel
            workspaceId={workspaceId}
            scopeRef={scopeRef}
            onOpenResult={(path, line) => openFile(path, undefined, line)}
            onFilesChanged={(paths) => {
              markIndexStale();
              void refreshGitStatus();
              void refreshParents(...paths);
              showToast("Replace applied", { type: "success" });
            }}
            onClose={() => setSearchPanelOpen(false)}
          />
        ) : (
          <>
            <OpenEditors
              groups={groups}
              dirty={dirty}
              gitStatus={gitStatus}
              collapsed={openEditorsCollapsed}
              onToggle={() =>
                setOpenEditorsCollapsed((collapsed) => !collapsed)
              }
              onActivate={selectTab}
            />
            <div className={styles.toolbar}>
              <form onSubmit={handleJump} style={{ display: "contents" }}>
                <input
                  className={styles.filterInput}
                  type="text"
                  value={jumpText}
                  onChange={(e) => setJumpText(e.target.value)}
                  placeholder="Jump to folder... (e.g. src/api)"
                  aria-label="Jump to folder"
                />
              </form>
              <input
                className={styles.filterInput}
                type="text"
                value={filterText}
                onChange={(e) => setFilterText(e.target.value)}
                placeholder="Filter files..."
                aria-label="Filter files"
              />
            </div>
            {isLoading ? (
              <div className={styles.treeScroll}>
                {Array.from({ length: 6 }, (_, i) => (
                  <div
                    key={i}
                    style={{
                      padding: "4px 8px",
                      paddingLeft: `${(i % 3) * 16 + 8}px`,
                    }}
                  >
                    <LoadingSkeleton
                      shape="text"
                      width={100 + (i % 4) * 20}
                      height={12}
                    />
                  </div>
                ))}
              </div>
            ) : error ? (
              <div className={styles.treeScroll}>
                <ErrorDisplay
                  variant="fetch-error"
                  title="Failed to load files"
                  error={new Error(error)}
                  showDetails
                />
              </div>
            ) : (
              <div className={styles.treeScroll}>
                <FileTree
                  treeData={treeData}
                  expanded={expanded}
                  selectedPath={selectedPath}
                  filterText={debouncedFilterText}
                  onToggle={toggle}
                  onSelectFile={(p) => {
                    if (p) openFile(p);
                  }}
                  onContextMenuNode={handleContextMenu}
                  onRequestRename={beginRename}
                  onRequestDelete={requestDelete}
                  inlineEdit={inlineEdit}
                  onInlineEditChange={(value) =>
                    setInlineEdit((prev) => (prev ? { ...prev, value } : prev))
                  }
                  onInlineEditCommit={() => void commitInlineEdit()}
                  onInlineEditCancel={() => setInlineEdit(null)}
                  gitStatus={gitStatus}
                  onMoveNode={handleMoveNode}
                  scrollToPath={scrollTarget}
                />
              </div>
            )}
          </>
        )}
      </div>
      <ResizeHandle
        width={treeWidth}
        minWidth={MIN_TREE_WIDTH}
        maxWidth={MAX_TREE_WIDTH}
        edge="right"
        onDelta={handleResizeDelta}
        onReset={handleResizeReset}
        ariaLabel="Resize file tree"
        testId="file-tree-resize-handle"
        className={styles.resizeHandle}
      />
      <div className={styles.mainColumn}>
        <div
          className={styles.editorGroups}
          data-split={groups.length > 1 || undefined}
        >
          <div
            className={styles.editorGroupSlot}
            style={
              groups.length > 1
                ? ({ "--group-width": `${splitLeftWidth}px` } as CSSProperties)
                : undefined
            }
          >
            <EditorGroup
              groupIndex={0}
              group={groups[0] ?? { tabs: [], active: null }}
              scopeRef={scopeRef}
              isActiveGroup={activeGroup === 0}
              dirty={dirty}
              onSelectTab={selectTab}
              onCloseTab={closeTab}
              onSplitRight={splitRight}
              onNavigate={navigateToDir}
              onSaved={handleSaved}
              onLineTargetApplied={handleLineTargetApplied}
              lineTarget={
                groups[0]?.active ? lineTargets[groups[0].active] : undefined
              }
            />
          </div>
          {groups[1] && (
            <>
              <ResizeHandle
                width={splitLeftWidth}
                minWidth={MIN_GROUP_WIDTH}
                maxWidth={MAX_GROUP_WIDTH}
                edge="right"
                onDelta={handleSplitResizeDelta}
                onReset={() => setSplitLeftWidth(DEFAULT_GROUP_WIDTH)}
                ariaLabel="Resize editor groups"
                testId="file-editor-group-resize-handle"
                className={styles.resizeHandle}
              />
              <div className={styles.editorGroupSlot}>
                <EditorGroup
                  groupIndex={1}
                  group={groups[1]}
                  scopeRef={scopeRef}
                  isActiveGroup={activeGroup === 1}
                  dirty={dirty}
                  onSelectTab={selectTab}
                  onCloseTab={closeTab}
                  onSplitRight={splitRight}
                  onNavigate={navigateToDir}
                  onSaved={handleSaved}
                  onLineTargetApplied={handleLineTargetApplied}
                  lineTarget={
                    groups[1]?.active
                      ? lineTargets[groups[1].active]
                      : undefined
                  }
                />
              </div>
            </>
          )}
        </div>
      </div>
      {contextMenu && (
        <ContextMenu
          state={contextMenu}
          onNewFile={(node) => beginCreate("create-file", node)}
          onNewFolder={(node) => beginCreate("create-folder", node)}
          onRename={beginRename}
          onDelete={requestDelete}
          onDuplicate={duplicateFile}
          onCopyPath={copyPath}
        />
      )}
      {deleteConfirm && (
        <DeleteConfirmDialog
          node={deleteConfirm.node}
          onCancel={() => setDeleteConfirm(null)}
          onConfirm={(skip) => void performDelete(deleteConfirm.node, skip)}
        />
      )}
      <QuickOpenPalette
        isOpen={quickOpenOpen}
        paths={quickOpenPaths}
        mru={mru}
        isLoading={quickOpenLoading}
        error={quickOpenError}
        truncated={quickOpenTruncated}
        onClose={() => setQuickOpenOpen(false)}
        onOpen={(path) => openFile(path)}
      />
    </div>
  );
}

export function FileBrowser({ scopeRef }: FileBrowserProps) {
  const { workspaceId } = useWorkspaceContext();
  const key = `${workspaceId}:${fileBrowserTabsStorageKey(scopeRef)}`;
  return (
    <FileBrowserStoreProvider
      key={key}
      workspaceId={workspaceId}
      scopeRef={scopeRef}
    >
      <FileBrowserInner scopeRef={scopeRef} />
    </FileBrowserStoreProvider>
  );
}

/**
 * WorkspaceFileBrowser remains the exported compatibility wrapper for the
 * Files page lazy import. Passing scopeRef enables the v2 scoped browser.
 */
export function WorkspaceFileBrowser({
  scopeRef = { scope: "workspace" },
}: Partial<FileBrowserProps>) {
  return <FileBrowser scopeRef={scopeRef} />;
}
