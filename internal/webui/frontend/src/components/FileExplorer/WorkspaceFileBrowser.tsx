import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type KeyboardEvent,
  type MouseEvent,
} from "react";
import { useStore } from "zustand";

import { DiffFileViewer } from "@/components/AgentDetailPanel";
import { useFileEditorBuffer } from "@/components/FileEditorPanel";
import { ResizeHandle } from "@/components/ResizeHandle";
import type { FileBlameData, FileCheckout, FileEntry } from "@/api/workspace";
import {
  blameScopedFile,
  deleteScopedPath,
  diffScopedFile,
  gitStatusScoped,
  indexScopedFiles,
  listScopedDir,
  listFileCheckouts,
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
  useFileBrowserStoreInstance,
  type FileBrowserGroup,
  type FileBrowserTab,
} from "@/hooks";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import {
  checkoutLabel,
  checkoutRefKey,
  checkoutTitle,
  mapWorkspaceIndexPathToCheckout,
  sameCheckoutRef,
  tabIdentityKey,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import {
  FileTree,
  type FileTreeInlineEdit,
  type FileTreeNodeInfo,
} from "./FileTree";
import {
  FileHistoryPanel,
  type HistoryOpenDiffRequest,
  type HistoryOpenRevisionRequest,
  type HistorySubject,
} from "./FileHistoryPanel";
import { FileRevisionPane, type RevisionViewState } from "./FileRevisionPane";
import { FileSearchPanel } from "./FileSearchPanel";
import { FileTabBar } from "./FileTabBar";
import { QuickOpenPalette } from "./QuickOpenPalette";
import { WorkspaceFilePane } from "./WorkspaceFilePane";
import { resolveTreeDropMove } from "./gitDecorations";
import {
  buildFileTreeSections,
  existingCheckoutRefs,
  type FileBrowserMode,
  type FileTreeRoot,
} from "./treeRoots";
import {
  buildChangeGroups,
  checkoutRefFromCheckout,
  type ChangeCheckoutGroup,
} from "./changesLens";
import type { QuickOpenItem } from "./quickOpen";
import { computeGitGutterLineMarks, type GitGutterLineMark } from "./gitGutter";
import styles from "./FileExplorer.module.css";

const TREE_WIDTH_KEY = "loom:file-browser:tree-width";
const DELETE_FILE_SKIP_KEY = "file-browser-delete-files-without-confirm";
const FILE_EXPLORER_LENS_KEY = "file-explorer-lens";
const DEFAULT_TREE_WIDTH = 320;
const MIN_TREE_WIDTH = 240;
const MAX_TREE_WIDTH = 400;

const DEFAULT_GROUP_WIDTH = 560;
const MIN_GROUP_WIDTH = 320;
const MAX_GROUP_WIDTH = 1100;
const QUICK_OPEN_STALE_MS = 10_000;

type ExplorerLens = "files" | "changes";

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

function getStoredLens(workspaceId: string): ExplorerLens {
  return wsGet(workspaceId, FILE_EXPLORER_LENS_KEY) === "changes"
    ? "changes"
    : "files";
}

function storeLens(workspaceId: string, lens: ExplorerLens): void {
  wsSet(workspaceId, FILE_EXPLORER_LENS_KEY, lens);
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

function shallowRecordEqual(
  a: Record<string, string> | undefined,
  b: Record<string, string>,
): boolean {
  if (!a) return false;
  const aEntries = Object.entries(a);
  const bEntries = Object.entries(b);
  if (aEntries.length !== bEntries.length) return false;
  return bEntries.every(([key, value]) => a[key] === value);
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
  mode?: FileBrowserMode | undefined;
  agentName?: string | undefined;
}

interface ContextMenuState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
  x: number;
  y: number;
}

interface DeleteConfirmState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
}

interface MoveDialogState {
  ref: CheckoutRef;
  node: FileTreeNodeInfo;
}

interface ScopedInlineEdit {
  ref: CheckoutRef;
  edit: FileTreeInlineEdit;
}

interface TreeRevealRequest {
  path: string;
  token: number;
}

interface TreeRefreshRequest {
  paths: string[];
  token: number;
}

interface LineTarget {
  line: number;
  token: number;
}

interface FileReloadRequest {
  key: string | null;
  token: number | undefined;
}

interface DiffViewState {
  ref: CheckoutRef;
  path: string;
  from?: string | undefined;
  to?: string | undefined;
  title: string;
  patch?: string | undefined;
  restoreContent?: string | undefined;
  canOpenFile?: boolean | undefined;
}

type OpenDiffRequest = HistoryOpenDiffRequest;

function LensToggle({
  lens,
  changeCount,
  onChange,
}: {
  lens: ExplorerLens;
  changeCount: number;
  onChange: (lens: ExplorerLens) => void;
}) {
  const tabs: Array<{ id: ExplorerLens; label: string }> = [
    { id: "files", label: "Files" },
    { id: "changes", label: "Changes" },
  ];

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    const currentIndex = tabs.findIndex((tab) => tab.id === lens);
    let nextIndex = -1;
    if (event.key === "ArrowRight" || event.key === "ArrowDown") {
      nextIndex = (currentIndex + 1) % tabs.length;
    } else if (event.key === "ArrowLeft" || event.key === "ArrowUp") {
      nextIndex = (currentIndex - 1 + tabs.length) % tabs.length;
    }
    if (nextIndex >= 0) {
      event.preventDefault();
      onChange(tabs[nextIndex]!.id);
    }
  };

  return (
    <div
      className={styles.lensToggle}
      role="tablist"
      aria-label="File explorer lens"
      onKeyDown={handleKeyDown}
    >
      {tabs.map((tab) => {
        const active = lens === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            role="tab"
            className={styles.lensTab}
            data-active={active || undefined}
            aria-selected={active}
            aria-label={
              tab.id === "changes" ? `Changes ${changeCount}` : tab.label
            }
            tabIndex={active ? 0 : -1}
            onClick={() => onChange(tab.id)}
          >
            <span>{tab.label}</span>
            {tab.id === "changes" && (
              <span className={styles.lensBadge}>{changeCount}</span>
            )}
          </button>
        );
      })}
    </div>
  );
}

function ChangesList({
  groups,
  onOpenDiff,
}: {
  groups: ChangeCheckoutGroup[];
  onOpenDiff: (request: OpenDiffRequest) => void;
}) {
  if (groups.length === 0) {
    return (
      <div className={styles.changesEmpty}>
        No uncommitted changes across this workspace.
      </div>
    );
  }

  return (
    <div className={styles.changesList} aria-label="Workspace changes">
      {groups.map((group) => (
        <section key={group.id} className={styles.changesGroup}>
          <h2 className={styles.changesGroupHeader}>{group.label}</h2>
          {!group.loaded ? (
            <div className={styles.changesLoading}>Loading changes...</div>
          ) : group.items.length === 0 ? (
            <div className={styles.changesLoading}>No changed files found</div>
          ) : (
            group.items.map((item) => (
              <button
                type="button"
                key={item.path}
                className={styles.changeRow}
                aria-label={`Open diff for ${item.path} (${item.status.label})`}
                onClick={() =>
                  onOpenDiff({
                    ref: group.ref,
                    path: item.path,
                    from: "HEAD",
                    title: checkoutLabel(group.ref),
                    canOpenFile: item.status.kind !== "deleted",
                  })
                }
              >
                <span className={styles.changePath}>
                  <span className={styles.changeName}>{item.name}</span>
                  {item.parentPath && (
                    <span className={styles.changeParent}>
                      {item.parentPath}
                    </span>
                  )}
                </span>
                <span
                  className={styles.changeStatusChip}
                  data-status={item.status.kind}
                >
                  {item.status.label}
                </span>
              </button>
            ))
          )}
        </section>
      ))}
    </div>
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
  onMove,
  onDuplicate,
  onCopyPath,
}: {
  state: ContextMenuState;
  onNewFile: (node: FileTreeNodeInfo) => void;
  onNewFolder: (node: FileTreeNodeInfo) => void;
  onRename: (node: FileTreeNodeInfo) => void;
  onDelete: (node: FileTreeNodeInfo) => void;
  onMove: (node: FileTreeNodeInfo) => void;
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
      <button type="button" role="menuitem" onClick={() => onMove(state.node)}>
        Move to...
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

function ChangeBadge({ count }: { count: number }): JSX.Element | null {
  if (count <= 0) return null;
  return <span className={styles.checkoutBadge}>{count}</span>;
}

function AgentAvatar({ name }: { name: string }): JSX.Element {
  const bg = getAvatarColor(name);
  return (
    <span
      className={styles.agentAvatar}
      style={{
        background: bg,
        color: shouldUseWhiteText(bg) ? "#fff" : "#1a1a1a",
      }}
      aria-hidden="true"
    >
      {getCompactAvatarInitials(name)}
    </span>
  );
}

function RootIcon({ icon }: { icon: "agent" | "repo" | "workspace" }) {
  if (icon === "agent") return null;
  return (
    <span className={styles.rootIcon} aria-hidden="true">
      {icon === "workspace" ? (
        <svg viewBox="0 0 16 16">
          <path
            d="M2.5 4.5h11v8h-11zM4 3h8"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      ) : (
        <svg viewBox="0 0 16 16">
          <path
            d="M2 3h4l2 2h6v8H2V3z"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
        </svg>
      )}
    </span>
  );
}

function RootRow({
  root,
  expanded,
  depth = 0,
  onToggle,
}: {
  root: FileTreeRoot;
  expanded: boolean;
  depth?: number | undefined;
  onToggle: () => void;
}) {
  const isAgent = root.kind === "agent";
  const label = root.label;
  const secondary = root.secondary;
  const exists = root.exists;
  const icon = isAgent ? "agent" : root.icon;
  const disabledTitle = exists ? undefined : "not checked out on this machine";
  return (
    <button
      type="button"
      className={styles.rootRow}
      data-dimmed={root.kind === "checkout" && root.dimmed ? true : undefined}
      data-disabled={!exists || undefined}
      style={{ paddingLeft: 8 + depth * 16 }}
      disabled={!exists}
      title={disabledTitle}
      onClick={onToggle}
    >
      <span
        className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
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
      {isAgent ? (
        <AgentAvatar name={root.agentName} />
      ) : (
        <RootIcon icon={icon} />
      )}
      <span className={styles.rootLabel}>{label}</span>
      {secondary && <span className={styles.rootSecondary}>· {secondary}</span>}
      <ChangeBadge count={root.changeCount} />
    </button>
  );
}

function sameRefInlineEdit(
  inlineEdit: ScopedInlineEdit | null,
  ref: CheckoutRef,
): FileTreeInlineEdit | null {
  return inlineEdit && sameCheckoutRef(inlineEdit.ref, ref)
    ? inlineEdit.edit
    : null;
}

function CheckoutTreeBlock({
  refInfo,
  depthOffset,
  selectedTab,
  inlineEdit,
  gitStatus,
  revealRequest,
  refreshRequest,
  onOpenFile,
  onContextMenu,
  onRequestRename,
  onRequestDelete,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  refInfo: CheckoutRef;
  depthOffset: number;
  selectedTab: FileBrowserTab | null;
  inlineEdit: ScopedInlineEdit | null;
  gitStatus: Record<string, string>;
  revealRequest?: TreeRevealRequest | undefined;
  refreshRequest?: TreeRefreshRequest | undefined;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onContextMenu: (
    ref: CheckoutRef,
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onRequestDelete: (ref: CheckoutRef, node: FileTreeNodeInfo) => void;
  onInlineEditChange: (value: string) => void;
  onInlineEditCommit: () => void;
  onInlineEditCancel: () => void;
}) {
  const {
    expanded,
    treeData,
    isLoading,
    error,
    debouncedFilterText,
    toggle,
    loadDir,
    revealPath,
  } = useScopedFileTree(refInfo);
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);
  const selectedPath =
    selectedTab && sameCheckoutRef(selectedTab.ref, refInfo)
      ? selectedTab.path
      : null;

  useEffect(() => {
    if (!revealRequest) return;
    void revealPath(revealRequest.path).then(() =>
      setScrollTarget(revealRequest.path),
    );
  }, [revealPath, revealRequest]);

  useEffect(() => {
    if (!refreshRequest) return;
    const parents = new Set(refreshRequest.paths.map(dirname));
    void Promise.all([...parents].map((parent) => loadDir(parent)));
  }, [loadDir, refreshRequest]);

  if (isLoading) {
    return (
      <div
        className={styles.checkoutTreeState}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        Loading...
      </div>
    );
  }
  if (error) {
    return (
      <div
        className={styles.checkoutTreeError}
        style={{ paddingLeft: 8 + depthOffset * 16 }}
      >
        {error}
      </div>
    );
  }

  return (
    <FileTree
      treeData={treeData}
      expanded={expanded}
      selectedPath={selectedPath}
      filterText={debouncedFilterText}
      onToggle={toggle}
      onSelectFile={(path) => {
        if (path) onOpenFile(refInfo, path);
      }}
      onContextMenuNode={(node, event) => onContextMenu(refInfo, node, event)}
      onRequestRename={(node) => onRequestRename(refInfo, node)}
      onRequestDelete={(node) => onRequestDelete(refInfo, node)}
      inlineEdit={sameRefInlineEdit(inlineEdit, refInfo)}
      onInlineEditChange={onInlineEditChange}
      onInlineEditCommit={onInlineEditCommit}
      onInlineEditCancel={onInlineEditCancel}
      gitStatus={gitStatus}
      scrollToPath={scrollTarget}
      depthOffset={depthOffset}
      idPrefix={`ft-${checkoutRefKey(refInfo).replace(/[^a-zA-Z0-9_-]/g, "-")}`}
    />
  );
}

function folderEntries(
  treeData: Map<string, FileEntry[]>,
  expanded: Set<string>,
): FileTreeNodeInfo[] {
  const out: FileTreeNodeInfo[] = [
    { path: "", name: "Checkout root", isDir: true, depth: 0 },
  ];
  const walk = (parent: string, depth: number) => {
    const entries = sortedEntries(treeData.get(parent) ?? []).filter(
      (entry) => entry.is_dir,
    );
    for (const entry of entries) {
      const path = joinPath(parent, entry.name);
      out.push({ path, name: entry.name, isDir: true, depth });
      if (expanded.has(path)) walk(path, depth + 1);
    }
  };
  walk("", 1);
  return out;
}

function resolveMoveToTarget(
  fromPath: string,
  targetFolderPath: string,
): { from: string; to: string } | null {
  if (targetFolderPath === "") {
    const to = basename(fromPath);
    return to && to !== fromPath ? { from: fromPath, to } : null;
  }
  return resolveTreeDropMove(fromPath, targetFolderPath);
}

function invalidMoveTarget(
  node: FileTreeNodeInfo,
  targetFolderPath: string,
): boolean {
  if (targetFolderPath === dirname(node.path)) return true;
  if (!node.isDir) return false;
  return (
    targetFolderPath === node.path ||
    targetFolderPath.startsWith(`${node.path}/`)
  );
}

function MoveToDialog({
  state,
  onCancel,
  onConfirm,
}: {
  state: MoveDialogState;
  onCancel: () => void;
  onConfirm: (targetFolderPath: string) => void;
}) {
  const { expanded, treeData, isLoading, error, toggle } = useScopedFileTree(
    state.ref,
  );
  const [selected, setSelected] = useState("");
  const folders = useMemo(
    () => folderEntries(treeData, expanded),
    [treeData, expanded],
  );
  const selectedInvalid = invalidMoveTarget(state.node, selected);
  const selectedMove = selectedInvalid
    ? null
    : resolveMoveToTarget(state.node.path, selected);

  return (
    <div className={styles.dialogOverlay}>
      <div
        className={styles.moveDialog}
        role="dialog"
        aria-modal="true"
        aria-label="Move to"
      >
        <div className={styles.moveDialogHeader}>
          <div>
            <div className={styles.moveDialogTitle}>Move to...</div>
            <div className={styles.moveDialogMeta}>
              {checkoutLabel(state.ref)} · {state.node.path}
            </div>
          </div>
          <button
            type="button"
            className={styles.iconButton}
            aria-label="Close move dialog"
            onClick={onCancel}
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
        <div className={styles.moveFolderList} role="tree">
          {isLoading ? (
            <div className={styles.emptyState}>Loading folders...</div>
          ) : error ? (
            <div className={styles.error}>{error}</div>
          ) : (
            folders.map((folder) => {
              const disabled = invalidMoveTarget(state.node, folder.path);
              const expandedFolder = expanded.has(folder.path);
              const hasChildren = (treeData.get(folder.path) ?? []).some(
                (entry) => entry.is_dir,
              );
              return (
                <div
                  key={folder.path || "__root__"}
                  className={styles.moveFolderRow}
                  style={{ paddingLeft: 8 + folder.depth * 16 }}
                  role="treeitem"
                  aria-selected={selected === folder.path}
                  aria-disabled={disabled || undefined}
                >
                  <button
                    type="button"
                    className={styles.moveFolderToggle}
                    aria-label={
                      expandedFolder ? "Collapse folder" : "Expand folder"
                    }
                    disabled={!hasChildren}
                    onClick={() => void toggle(folder.path)}
                  >
                    {hasChildren ? (expandedFolder ? "v" : ">") : ""}
                  </button>
                  <button
                    type="button"
                    className={styles.moveFolderSelect}
                    data-selected={selected === folder.path || undefined}
                    disabled={disabled}
                    title={
                      disabled
                        ? "Cannot move into itself or a descendant"
                        : undefined
                    }
                    onClick={() => setSelected(folder.path)}
                  >
                    {folder.name}
                  </button>
                </div>
              );
            })
          )}
        </div>
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
            className={styles.saveButton}
            disabled={!selectedMove}
            onClick={() => onConfirm(selected)}
          >
            Move
          </button>
        </div>
      </div>
    </div>
  );
}

function DiffEditorPane({
  diffView,
  historyOpen,
  onToggleHistory,
  onClose,
  onOpenFile,
  onRestore,
}: {
  diffView: DiffViewState;
  historyOpen: boolean;
  onToggleHistory: () => void;
  onClose: () => void;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onRestore:
    | ((ref: CheckoutRef, path: string, content: string) => Promise<void>)
    | undefined;
}) {
  const { workspaceId } = useWorkspaceContext();
  const [patch, setPatch] = useState<string | null>(diffView.patch ?? null);
  const [isLoading, setIsLoading] = useState(!diffView.patch);
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    let canceled = false;
    setPatch(diffView.patch ?? null);
    setError(undefined);
    if (diffView.patch !== undefined) {
      setIsLoading(false);
      return () => {
        canceled = true;
      };
    }
    setIsLoading(true);
    diffScopedFile(
      workspaceId,
      diffView.ref,
      diffView.path,
      diffView.from,
      diffView.to,
    )
      .then((res) => {
        if (!canceled) setPatch(res.patch);
      })
      .catch((err) => {
        if (!canceled)
          setError(err instanceof Error ? err.message : String(err));
      })
      .finally(() => {
        if (!canceled) setIsLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [diffView, workspaceId]);

  return (
    <div className={styles.viewerColumn}>
      <div className={styles.viewerHeader}>
        <div className={styles.diffTitle}>
          <span className={styles.diffTitlePath}>{diffView.path}</span>
          <span className={styles.diffTitleMeta}>{diffView.title}</span>
        </div>
        <div className={styles.viewerActions}>
          <button
            type="button"
            className={`${styles.saveButton} ${styles.historyToggle}`}
            aria-pressed={historyOpen}
            onClick={onToggleHistory}
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <circle
                cx="8"
                cy="8"
                r="5.5"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.4"
              />
              <path
                d="M8 4.8V8l2.2 1.4"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
            <span>History</span>
          </button>
          {diffView.canOpenFile && (
            <button
              type="button"
              className={styles.saveButton}
              onClick={() => onOpenFile(diffView.ref, diffView.path)}
            >
              Open file
            </button>
          )}
          {diffView.restoreContent !== undefined && onRestore && (
            <button
              type="button"
              className={styles.saveButton}
              onClick={() =>
                void onRestore(
                  diffView.ref,
                  diffView.path,
                  diffView.restoreContent ?? "",
                )
              }
            >
              Restore
            </button>
          )}
          <button
            type="button"
            className={styles.iconButton}
            aria-label="Close diff"
            title="Close diff"
            onClick={onClose}
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M4 4l8 8M12 4l-8 8"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
              />
            </svg>
          </button>
        </div>
      </div>
      <div className={styles.diffEditorBody}>
        <DiffFileViewer
          patch={
            patch === null
              ? null
              : {
                  patch,
                  is_binary: false,
                  is_too_large: false,
                  additions: 0,
                  deletions: 0,
                }
          }
          isLoading={isLoading}
          error={error}
        />
      </div>
    </div>
  );
}

function EditorGroup({
  groupIndex,
  group,
  diffView,
  revisionView,
  isActiveGroup,
  dirty,
  onSelectTab,
  onCloseTab,
  onSplitRight,
  onNavigate,
  onSaved,
  onOpenDiff,
  onCloseDiff,
  onOpenRevision,
  onCloseRevision,
  onOpenEditableFile,
  onRestoreSnapshot,
  historyRefreshKey,
  reloadToken,
  lineTarget,
  onLineTargetApplied,
}: {
  groupIndex: number;
  group: FileBrowserGroup;
  diffView: DiffViewState | null;
  revisionView: RevisionViewState | null;
  isActiveGroup: boolean;
  dirty: Record<string, boolean>;
  onSelectTab: (groupIndex: number, tabKey: string) => void;
  onCloseTab: (groupIndex: number, tabKey: string) => void;
  onSplitRight: (groupIndex: number) => void;
  onNavigate: (ref: CheckoutRef, dirPath: string) => void;
  onSaved: (tab: FileBrowserTab) => void;
  onOpenDiff: (groupIndex: number, request: OpenDiffRequest) => void;
  onCloseDiff: (groupIndex: number) => void;
  onOpenRevision: (
    groupIndex: number,
    request: HistoryOpenRevisionRequest,
  ) => void;
  onCloseRevision: (groupIndex: number) => void;
  onOpenEditableFile: (
    groupIndex: number,
    ref: CheckoutRef,
    path: string,
  ) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
  historyRefreshKey: number;
  reloadToken?: number | undefined;
  lineTarget?: LineTarget | undefined;
  onLineTargetApplied: (tabKey: string, token: number) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const store = useFileBrowserStoreInstance();
  const activeTab =
    group.tabs.find((tab) => tabIdentityKey(tab) === group.active) ?? null;
  const scopeRef = useMemo<CheckoutRef>(
    () => activeTab?.ref ?? { scope: "workspace" },
    [activeTab],
  );
  const { fileData, isLoading, error, fetchFile, clearFile } =
    useScopedFileContent(scopeRef);
  const fetchFileRef = useRef(fetchFile);
  const clearFileRef = useRef(clearFile);
  fetchFileRef.current = fetchFile;
  clearFileRef.current = clearFile;
  const activePath = activeTab?.path ?? null;
  const activeKey = activeTab ? tabIdentityKey(activeTab) : null;
  const scopeKey = checkoutRefKey(scopeRef);
  const pathDirty = activeKey ? !!dirty[activeKey] : false;
  const appliedReloadRef = useRef<FileReloadRequest>({
    key: null,
    token: undefined,
  });
  const [searchOpen, setSearchOpen] = useState(false);
  const [basePath, setBasePath] = useState<string | null>(null);
  const [baseContent, setBaseContent] = useState<string | null>(null);
  const [gitGutterMarks, setGitGutterMarks] = useState<GitGutterLineMark[]>([]);
  const [blameEnabled, setBlameEnabled] = useState(false);
  const [blameData, setBlameData] = useState<FileBlameData | null>(null);
  const [blameLoading, setBlameLoading] = useState(false);
  const [blameError, setBlameError] = useState<string | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const historySubject = useMemo<HistorySubject | null>(() => {
    if (diffView || revisionView || !activeTab || !activePath) return null;
    return { ref: scopeRef, path: activePath };
  }, [activePath, activeTab, diffView, revisionView, scopeRef]);
  const toggleHistory = useCallback(() => setHistoryOpen((open) => !open), []);
  const closeHistory = useCallback(() => setHistoryOpen(false), []);
  const renderHistoryPanel = () =>
    historyOpen ? (
      <FileHistoryPanel
        subject={historySubject}
        refreshKey={historyRefreshKey}
        onClose={closeHistory}
        onOpenDiff={(request) => onOpenDiff(groupIndex, request)}
        onOpenRevision={(request) => onOpenRevision(groupIndex, request)}
        onRestoreSnapshot={onRestoreSnapshot}
      />
    ) : null;

  useEffect(() => {
    setSearchOpen(false);
    const hasReloadRequest =
      activeKey !== null &&
      reloadToken !== undefined &&
      (appliedReloadRef.current.key !== activeKey ||
        appliedReloadRef.current.token !== reloadToken);
    if (hasReloadRequest) {
      appliedReloadRef.current = { key: activeKey, token: reloadToken };
    }
    if (activePath) {
      if (!pathDirty || hasReloadRequest) {
        void fetchFileRef.current(activePath);
      }
    } else {
      appliedReloadRef.current = { key: null, token: undefined };
      clearFileRef.current();
    }
  }, [activeKey, activePath, pathDirty, reloadToken, scopeKey]);

  const writeFile = useCallback(
    (path: string, content: string) =>
      writeScopedFile(workspaceId, scopeRef, path, content),
    [workspaceId, scopeRef],
  );
  const setDirty = useCallback(
    (_path: string, isDirty: boolean) => {
      if (activeKey) store.getState().setDirty(activeKey, isDirty);
    },
    [activeKey, store],
  );

  const canSave =
    !!activePath && !!fileData && !fileData.binary && !fileData.truncated;

  const loadBaseContent = useCallback(
    async (path: string) => {
      try {
        const data = await readScopedFile(workspaceId, scopeRef, path, "HEAD");
        if (!data.binary && !data.truncated) {
          setBasePath(path);
          setBaseContent(data.content ?? "");
        } else {
          setBasePath(null);
          setBaseContent(null);
        }
      } catch {
        setBasePath(null);
        setBaseContent(null);
      }
    },
    [scopeRef, workspaceId],
  );

  const editor = useFileEditorBuffer({
    path: activePath,
    fileData,
    isActive: isActiveGroup,
    canSave,
    writeFile,
    onDirtyChange: setDirty,
    onSaved: () => {
      if (activeTab && activePath) {
        onSaved(activeTab);
        void loadBaseContent(activePath);
      }
    },
  });

  useEffect(() => {
    setBasePath(null);
    setBaseContent(null);
    setGitGutterMarks([]);
    setBlameEnabled(false);
    setBlameData(null);
    setBlameError(null);
    if (activePath && canSave) {
      void loadBaseContent(activePath);
    }
  }, [activePath, canSave, loadBaseContent]);

  useEffect(() => {
    const handleFocus = () => {
      if (activePath && canSave) {
        void loadBaseContent(activePath);
      }
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [activePath, canSave, loadBaseContent]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!activePath || basePath !== activePath || baseContent === null) {
        setGitGutterMarks([]);
        return;
      }
      setGitGutterMarks(computeGitGutterLineMarks(baseContent, editor.content));
    }, 150);
    return () => window.clearTimeout(timer);
  }, [activePath, baseContent, basePath, editor.content]);

  useEffect(() => {
    let canceled = false;
    if (!activePath || !blameEnabled || !canSave) {
      setBlameData(null);
      setBlameLoading(false);
      setBlameError(null);
      return () => {
        canceled = true;
      };
    }
    setBlameLoading(true);
    setBlameError(null);
    blameScopedFile(workspaceId, scopeRef, activePath)
      .then((data) => {
        if (!canceled) setBlameData(data);
      })
      .catch((err) => {
        if (!canceled) {
          setBlameData(null);
          setBlameError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!canceled) setBlameLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [activePath, blameEnabled, canSave, scopeRef, workspaceId]);

  if (diffView) {
    return (
      <section
        className={styles.editorGroup}
        data-active={isActiveGroup || undefined}
      >
        <FileTabBar
          tabs={group.tabs}
          activeKey={group.active}
          dirtyPaths={dirty}
          groupLabel={`group ${groupIndex + 1}`}
          onSelect={(key) => onSelectTab(groupIndex, key)}
          onClose={(key) => onCloseTab(groupIndex, key)}
        />
        <div className={styles.editorGroupBody}>
          <div className={styles.editorPrimaryPane}>
            <DiffEditorPane
              diffView={diffView}
              historyOpen={historyOpen}
              onToggleHistory={toggleHistory}
              onClose={() => onCloseDiff(groupIndex)}
              onOpenFile={(ref, path) =>
                onOpenEditableFile(groupIndex, ref, path)
              }
              onRestore={onRestoreSnapshot}
            />
          </div>
          {renderHistoryPanel()}
        </div>
      </section>
    );
  }

  if (revisionView) {
    return (
      <section
        className={styles.editorGroup}
        data-active={isActiveGroup || undefined}
      >
        <FileTabBar
          tabs={group.tabs}
          activeKey={group.active}
          dirtyPaths={dirty}
          groupLabel={`group ${groupIndex + 1}`}
          onSelect={(key) => onSelectTab(groupIndex, key)}
          onClose={(key) => onCloseTab(groupIndex, key)}
        />
        <div className={styles.editorGroupBody}>
          <div className={styles.editorPrimaryPane}>
            <FileRevisionPane
              revisionView={revisionView}
              historyOpen={historyOpen}
              onToggleHistory={toggleHistory}
              onClose={() => onCloseRevision(groupIndex)}
            />
          </div>
          {renderHistoryPanel()}
        </div>
      </section>
    );
  }

  return (
    <section
      className={styles.editorGroup}
      data-active={isActiveGroup || undefined}
    >
      <FileTabBar
        tabs={group.tabs}
        activeKey={group.active}
        dirtyPaths={dirty}
        groupLabel={`group ${groupIndex + 1}`}
        onSelect={(key) => onSelectTab(groupIndex, key)}
        onClose={(key) => onCloseTab(groupIndex, key)}
      />
      <div className={styles.editorGroupBody}>
        <div className={styles.editorPrimaryPane}>
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
            historyOpen={historyOpen}
            onToggleHistory={toggleHistory}
            onToggleSearch={() => setSearchOpen((open) => !open)}
            onSplitRight={() => onSplitRight(groupIndex)}
            onNavigate={(dirPath) => onNavigate(scopeRef, dirPath)}
            lineTarget={lineTarget}
            onLineTargetApplied={(_path, token) => {
              if (activeKey) onLineTargetApplied(activeKey, token);
            }}
            gitGutterMarks={gitGutterMarks}
            blameEnabled={blameEnabled}
            blameLines={blameData?.skipped ? [] : blameData?.lines}
            blameLoading={blameLoading}
            blameSkippedMessage={
              blameError ?? (blameData?.skipped ? blameData.message : undefined)
            }
            onToggleBlame={() => setBlameEnabled((enabled) => !enabled)}
            onOpenBlameCommit={(sha) => {
              if (!activePath) return;
              onOpenDiff(groupIndex, {
                ref: scopeRef,
                path: activePath,
                from: `${sha}^`,
                to: sha,
                title: sha.slice(0, 8),
              });
            }}
          />
        </div>
        {renderHistoryPanel()}
      </div>
    </section>
  );
}

function FileBrowserInner({ mode = "workspace", agentName }: FileBrowserProps) {
  const { workspaceId, agents, repos } = useWorkspaceContext();
  const eventContext = useEventContext();
  const { showToast } = useToast();
  const store = useFileBrowserStoreInstance();
  const groups = useStore(store, (s) => s.groups);
  const activeGroup = useStore(store, (s) => s.activeGroup);
  const dirty = useStore(store, (s) => s.dirty);
  const mru = useStore(store, (s) => s.mru);

  const [treeWidth, setTreeWidth] = useState<number>(getStoredTreeWidth);
  const [lens, setLens] = useState<ExplorerLens>(() =>
    getStoredLens(workspaceId),
  );
  const [splitLeftWidth, setSplitLeftWidth] = useState(DEFAULT_GROUP_WIDTH);
  const [lineTargets, setLineTargets] = useState<Record<string, LineTarget>>(
    {},
  );
  const [fileReloadTokens, setFileReloadTokens] = useState<
    Record<string, number>
  >({});
  const [historyRefreshKey, setHistoryRefreshKey] = useState(0);
  const [diffViews, setDiffViews] = useState<
    Record<number, DiffViewState | null>
  >({});
  const [revisionViews, setRevisionViews] = useState<
    Record<number, RevisionViewState | null>
  >({});
  const [quickOpenOpen, setQuickOpenOpen] = useState(false);
  const [quickOpenItems, setQuickOpenItems] = useState<QuickOpenItem[]>([]);
  const [quickOpenTruncated, setQuickOpenTruncated] = useState(false);
  const [quickOpenFetchedAt, setQuickOpenFetchedAt] = useState(0);
  const [quickOpenLoading, setQuickOpenLoading] = useState(false);
  const [quickOpenError, setQuickOpenError] = useState<string | null>(null);
  const [gitStatusByRef, setGitStatusByRef] = useState<
    Record<string, Record<string, string>>
  >({});
  const [checkouts, setCheckouts] = useState<FileCheckout[]>([]);
  const [checkoutError, setCheckoutError] = useState<string | null>(null);
  const [searchPanelOpen, setSearchPanelOpen] = useState(false);
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);
  const [inlineEdit, setInlineEdit] = useState<ScopedInlineEdit | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<DeleteConfirmState | null>(
    null,
  );
  const [moveDialog, setMoveDialog] = useState<MoveDialogState | null>(null);
  const [expandedRoots, setExpandedRoots] = useState<Set<string>>(new Set());
  const [treeRevealRequests, setTreeRevealRequests] = useState<
    Record<string, TreeRevealRequest>
  >({});
  const [treeRefreshRequests, setTreeRefreshRequests] = useState<
    Record<string, TreeRefreshRequest>
  >({});
  const inlineCommitKeyRef = useRef<string | null>(null);
  const reconnectAttemptsRef = useRef(eventContext.reconnectAttempts);
  const lastLoadedChangeGroupsRef = useRef<Map<string, ChangeCheckoutGroup>>(
    new Map(),
  );

  const sections = useMemo(
    () =>
      buildFileTreeSections({
        mode,
        agentName,
        agents,
        repos,
        checkouts,
      }),
    [mode, agentName, agents, repos, checkouts],
  );
  const computedValidRefs = useMemo(
    () => existingCheckoutRefs(sections),
    [sections],
  );
  const validRefsKey = computedValidRefs.map(checkoutRefKey).join("|");
  const validRefsRef = useRef<{ key: string; refs: CheckoutRef[] }>({
    key: "",
    refs: [],
  });
  if (validRefsRef.current.key !== validRefsKey) {
    validRefsRef.current = { key: validRefsKey, refs: computedValidRefs };
  }
  const validRefs = validRefsRef.current.refs;
  const knownRefs = validRefs;
  const checkoutChangeCount = useMemo(
    () => checkouts.reduce((sum, checkout) => sum + checkout.change_count, 0),
    [checkouts],
  );
  const changesRefs = useMemo(() => {
    const seen = new Set<string>();
    return checkouts
      .filter((checkout) => checkout.exists && checkout.change_count > 0)
      .map(checkoutRefFromCheckout)
      .filter((ref) => {
        const key = checkoutRefKey(ref);
        if (seen.has(key)) return false;
        seen.add(key);
        return true;
      });
  }, [checkouts]);
  const statusRefs = lens === "changes" ? changesRefs : validRefs;
  const statusRefsKey = statusRefs.map(checkoutRefKey).join("|");
  const stableStatusRefsRef = useRef<{
    key: string;
    refs: CheckoutRef[];
  }>({ key: "", refs: [] });
  if (stableStatusRefsRef.current.key !== statusRefsKey) {
    stableStatusRefsRef.current = { key: statusRefsKey, refs: statusRefs };
  }
  const stableStatusRefs = stableStatusRefsRef.current.refs;
  const changeGroups = useMemo(
    () => buildChangeGroups(checkouts, gitStatusByRef),
    [checkouts, gitStatusByRef],
  );
  const visibleChangeGroups = useMemo(
    () =>
      changeGroups.map((group) => {
        if (group.loaded) return group;
        const previous = lastLoadedChangeGroupsRef.current.get(group.id);
        return previous
          ? {
              ...previous,
              label: group.label,
              changeCount: group.changeCount,
            }
          : group;
      }),
    [changeGroups],
  );
  const activeTab =
    groups[activeGroup]?.tabs.find(
      (tab) => tabIdentityKey(tab) === groups[activeGroup]?.active,
    ) ??
    groups[0]?.tabs.find((tab) => tabIdentityKey(tab) === groups[0]?.active) ??
    null;
  const workspaceRef = useMemo<CheckoutRef>(() => ({ scope: "workspace" }), []);

  useEffect(() => {
    for (const group of visibleChangeGroups) {
      if (group.loaded) lastLoadedChangeGroupsRef.current.set(group.id, group);
    }
  }, [visibleChangeGroups]);

  useEffect(() => {
    store.getState().pruneUnavailableRefs(validRefs);
  }, [store, validRefs]);

  useEffect(() => {
    setLens(getStoredLens(workspaceId));
  }, [workspaceId]);

  const changeLens = useCallback(
    (nextLens: ExplorerLens) => {
      setLens(nextLens);
      storeLens(workspaceId, nextLens);
    },
    [workspaceId],
  );

  const markIndexStale = useCallback(() => {
    setQuickOpenFetchedAt(0);
  }, []);

  const refreshCheckouts = useCallback(async () => {
    try {
      const data = await listFileCheckouts(workspaceId);
      setCheckouts(data.checkouts);
      setCheckoutError(null);
    } catch (err) {
      setCheckoutError(err instanceof Error ? err.message : String(err));
    }
  }, [workspaceId]);

  const refreshGitStatus = useCallback(async () => {
    const next: Record<string, Record<string, string>> = {};
    await Promise.all(
      stableStatusRefs.map(async (ref) => {
        const key = checkoutRefKey(ref);
        try {
          next[key] = await gitStatusScoped(workspaceId, ref);
        } catch {
          next[key] = {};
        }
      }),
    );
    setGitStatusByRef((prev) => {
      let changed = false;
      const merged = { ...prev };
      for (const [key, value] of Object.entries(next)) {
        if (!shallowRecordEqual(prev[key], value)) {
          merged[key] = value;
          changed = true;
        }
      }
      return changed ? merged : prev;
    });
  }, [stableStatusRefs, workspaceId]);

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
        const index = await indexScopedFiles(workspaceId, {
          scope: "workspace",
        });
        const items = index.paths.map((rawPath) => {
          const mapped = mapWorkspaceIndexPathToCheckout(rawPath, knownRefs);
          return {
            id: tabIdentityKey({ ref: mapped.ref, path: mapped.path }),
            ref: mapped.ref,
            path: mapped.path,
            checkoutLabel: checkoutLabel(mapped.ref),
          };
        });
        setQuickOpenItems(items);
        setQuickOpenTruncated(index.truncated);
        setQuickOpenFetchedAt(Date.now());
      } catch (err) {
        setQuickOpenError(err instanceof Error ? err.message : String(err));
      } finally {
        setQuickOpenLoading(false);
      }
    },
    [knownRefs, quickOpenFetchedAt, workspaceId],
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
    if (quickOpenOpen) {
      void fetchQuickOpenIndex();
    }
  }, [fetchQuickOpenIndex, quickOpenOpen]);

  useEffect(() => {
    void refreshCheckouts();
  }, [refreshCheckouts]);

  useEffect(() => {
    void refreshGitStatus();
  }, [refreshGitStatus]);

  useEffect(() => {
    const handleFocus = () => {
      void refreshCheckouts();
      void refreshGitStatus();
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [refreshCheckouts, refreshGitStatus]);

  useEffect(() => {
    const previous = reconnectAttemptsRef.current;
    reconnectAttemptsRef.current = eventContext.reconnectAttempts;
    if (
      eventContext.reconnectAttempts > 0 ||
      (previous > 0 && eventContext.state === "connected")
    ) {
      void refreshCheckouts();
      void refreshGitStatus();
    }
  }, [
    eventContext.reconnectAttempts,
    eventContext.state,
    refreshCheckouts,
    refreshGitStatus,
  ]);

  useEffect(() => {
    const handleKeyDown = (event: globalThis.KeyboardEvent) => {
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

  const expandForRef = useCallback((ref: CheckoutRef) => {
    setExpandedRoots((prev) => {
      const next = new Set(prev);
      next.add(checkoutRefKey(ref));
      if (ref.scope === "agent" && ref.target) {
        next.add(`agent:${ref.target}`);
      }
      return next;
    });
  }, []);

  const revealInTree = useCallback(
    (ref: CheckoutRef, path: string) => {
      expandForRef(ref);
      const key = checkoutRefKey(ref);
      setTreeRevealRequests((prev) => ({
        ...prev,
        [key]: { path, token: (prev[key]?.token ?? 0) + 1 },
      }));
    },
    [expandForRef],
  );

  const refreshParents = useCallback((ref: CheckoutRef, ...paths: string[]) => {
    const key = checkoutRefKey(ref);
    setTreeRefreshRequests((prev) => ({
      ...prev,
      [key]: { paths, token: (prev[key]?.token ?? 0) + 1 },
    }));
  }, []);

  const requestFileReload = useCallback((ref: CheckoutRef, path: string) => {
    const key = tabIdentityKey({ ref, path });
    setFileReloadTokens((prev) => ({
      ...prev,
      [key]: (prev[key] ?? 0) + 1,
    }));
  }, []);

  const discardActiveIfNeeded = useCallback(
    (groupIndex: number, nextKey?: string): boolean => {
      const state = store.getState();
      const current = state.groups[groupIndex]?.active;
      if (!current || current === nextKey || !state.dirty[current]) return true;
      const tab = state.groups[groupIndex]?.tabs.find(
        (candidate) => tabIdentityKey(candidate) === current,
      );
      const ok = window.confirm(`Discard unsaved changes in ${current}?`);
      if (!ok) return false;
      state.setDirty(current, false);
      if (tab) state.setDirty(tabIdentityKey(tab), false);
      return true;
    },
    [store],
  );

  const openFile = useCallback(
    (
      ref: CheckoutRef,
      path: string,
      groupIndex = store.getState().activeGroup,
      lineNumber?: number,
    ) => {
      const tab = { ref, path };
      const key = tabIdentityKey(tab);
      if (!discardActiveIfNeeded(groupIndex, key)) return;
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().openTab(tab, groupIndex);
      revealInTree(ref, path);
      if (lineNumber && lineNumber > 0) {
        setLineTargets((prev) => ({
          ...prev,
          [key]: { line: lineNumber, token: (prev[key]?.token ?? 0) + 1 },
        }));
      }
    },
    [discardActiveIfNeeded, revealInTree, store],
  );

  const handleLineTargetApplied = useCallback((key: string, token: number) => {
    setLineTargets((prev) => {
      if (prev[key]?.token !== token) return prev;
      const next = { ...prev };
      delete next[key];
      return next;
    });
  }, []);

  const selectTab = useCallback(
    (groupIndex: number, key: string) => {
      if (!discardActiveIfNeeded(groupIndex, key)) return;
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().activateTab(groupIndex, key);
      const tab = store
        .getState()
        .groups[
          groupIndex
        ]?.tabs.find((candidate) => tabIdentityKey(candidate) === key);
      if (tab) revealInTree(tab.ref, tab.path);
    },
    [discardActiveIfNeeded, revealInTree, store],
  );

  const closeTab = useCallback(
    (groupIndex: number, key: string) => {
      if (store.getState().dirty[key]) {
        const tab = store
          .getState()
          .groups[
            groupIndex
          ]?.tabs.find((candidate) => tabIdentityKey(candidate) === key);
        const label = tab ? checkoutTitle(tab.ref, tab.path) : key;
        const ok = window.confirm(`Discard unsaved changes in ${label}?`);
        if (!ok) return;
        store.getState().setDirty(key, false);
      }
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
      store.getState().closeTab(groupIndex, key);
    },
    [store],
  );

  const splitRight = useCallback(
    (groupIndex: number) => {
      const state = store.getState();
      const key = state.groups[groupIndex]?.active;
      const tab = state.groups[groupIndex]?.tabs.find(
        (candidate) => tabIdentityKey(candidate) === key,
      );
      if (!key || !tab) return;
      if (state.dirty[key]) {
        const ok = window.confirm(
          `Discard unsaved changes in ${checkoutTitle(tab.ref, tab.path)} before splitting?`,
        );
        if (!ok) return;
        state.setDirty(key, false);
      }
      state.splitRight(tab);
    },
    [store],
  );

  const handleSaved = useCallback(
    (tab: FileBrowserTab) => {
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      setHistoryRefreshKey((key) => key + 1);
      showToast("File saved", { type: "success" });
      refreshParents(tab.ref, tab.path);
    },
    [
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
    ],
  );

  const openDiff = useCallback(
    (groupIndex: number, request: OpenDiffRequest) => {
      const title =
        request.title ??
        (request.to
          ? `${request.from ?? "HEAD"}..${request.to}`
          : `${request.from ?? "HEAD"} vs working tree`);
      setDiffViews((prev) => ({
        ...prev,
        [groupIndex]: { ...request, title },
      }));
      setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
    },
    [],
  );

  const closeDiff = useCallback((groupIndex: number) => {
    setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
  }, []);

  const openRevision = useCallback(
    (groupIndex: number, request: HistoryOpenRevisionRequest) => {
      setRevisionViews((prev) => ({
        ...prev,
        [groupIndex]: { ...request },
      }));
      setDiffViews((prev) => ({ ...prev, [groupIndex]: null }));
    },
    [],
  );

  const closeRevision = useCallback((groupIndex: number) => {
    setRevisionViews((prev) => ({ ...prev, [groupIndex]: null }));
  }, []);

  const restoreSnapshot = useCallback(
    async (ref: CheckoutRef, path: string, content: string) => {
      const key = tabIdentityKey({ ref, path });
      const state = store.getState();
      const isOpen = state.groups.some((group) =>
        group.tabs.some((tab) => tabIdentityKey(tab) === key),
      );
      const unsavedWarning =
        isOpen && state.dirty[key]
          ? "\n\nUnsaved edits in the open tab will be replaced."
          : "";
      const ok = window.confirm(
        `Restore ${checkoutTitle(ref, path)}?${unsavedWarning}`,
      );
      if (!ok) return;
      await writeScopedFile(workspaceId, ref, path, content);
      store.getState().setDirty(key, false);
      if (isOpen) requestFileReload(ref, path);
      setDiffViews({});
      setRevisionViews({});
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      setHistoryRefreshKey((key) => key + 1);
      refreshParents(ref, path);
      showToast(`Restored ${basename(path)}`, { type: "success" });
      openFile(ref, path);
    },
    [
      workspaceId,
      store,
      requestFileReload,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      showToast,
      openFile,
    ],
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

  const navigateToDir = useCallback(
    (ref: CheckoutRef, dirPath: string) => {
      revealInTree(ref, dirPath);
    },
    [revealInTree],
  );

  const beginCreate = useCallback(
    (
      ref: CheckoutRef,
      kind: "create-file" | "create-folder",
      node: FileTreeNodeInfo,
    ) => {
      setContextMenu(null);
      const parentPath = node.isDir ? node.path : dirname(node.path);
      setInlineEdit({
        ref,
        edit: {
          kind,
          parentPath,
          value: kind === "create-file" ? "untitled.txt" : "new-folder",
          isDir: kind === "create-folder",
        },
      });
    },
    [],
  );

  const beginRename = useCallback(
    (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      setInlineEdit({
        ref,
        edit: {
          kind: "rename",
          parentPath: dirname(node.path),
          path: node.path,
          value: node.name,
          isDir: node.isDir,
        },
      });
    },
    [],
  );

  const commitInlineEdit = useCallback(async () => {
    if (!inlineEdit) return;
    const edit = inlineEdit.edit;
    const ref = inlineEdit.ref;
    const value = edit.value.trim();
    if (!value) {
      setInlineEdit(null);
      return;
    }
    const commitKey = `${checkoutRefKey(ref)}:${edit.kind}:${edit.path ?? edit.parentPath}:${value}`;
    if (inlineCommitKeyRef.current === commitKey) return;
    inlineCommitKeyRef.current = commitKey;
    setInlineEdit(null);
    try {
      if (edit.kind === "create-file") {
        const path = joinPath(edit.parentPath, value);
        await writeScopedFile(workspaceId, ref, path, "");
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, path);
        openFile(ref, path);
        revealInTree(ref, path);
      } else if (edit.kind === "create-folder") {
        const path = joinPath(edit.parentPath, value);
        await mkdirScoped(workspaceId, ref, path);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, path);
        revealInTree(ref, path);
      } else if (edit.path) {
        const nextPath = joinPath(edit.parentPath, value);
        if (nextPath !== edit.path) {
          await moveScopedPath(workspaceId, ref, edit.path, nextPath);
          store.getState().retargetPathPrefix(ref, edit.path, nextPath);
          markIndexStale();
          void refreshCheckouts();
          void refreshGitStatus();
          refreshParents(ref, edit.path, nextPath);
          revealInTree(ref, nextPath);
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
    refreshParents,
    openFile,
    revealInTree,
    store,
    showToast,
    markIndexStale,
    refreshCheckouts,
    refreshGitStatus,
  ]);

  const dirtyTabsForPath = useCallback(
    (ref: CheckoutRef, path: string): string[] => {
      const state = store.getState();
      const dirtyKeys = new Set(Object.keys(state.dirty));
      return state.groups
        .flatMap((group) => group.tabs)
        .filter(
          (tab) =>
            dirtyKeys.has(tabIdentityKey(tab)) &&
            sameCheckoutRef(tab.ref, ref) &&
            pathMatchesPrefix(tab.path, path),
        )
        .map(tabIdentityKey);
    },
    [store],
  );

  const performDelete = useCallback(
    async (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      skipFutureFileConfirms = false,
    ) => {
      const dirtyTabs = dirtyTabsForPath(ref, node.path);
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
        await deleteScopedPath(workspaceId, ref, node.path, node.isDir);
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        if (!node.isDir && skipFutureFileConfirms) {
          wsSet(workspaceId, DELETE_FILE_SKIP_KEY, "1");
        }
        store.getState().closePathPrefix(ref, node.path);
        refreshParents(ref, node.path);
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
      store,
      refreshParents,
      showToast,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
    ],
  );

  const requestDelete = useCallback(
    (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      const skipFileConfirm =
        !node.isDir && wsGet(workspaceId, DELETE_FILE_SKIP_KEY) === "1";
      if (skipFileConfirm) {
        void performDelete(ref, node);
      } else {
        setDeleteConfirm({ ref, node });
      }
    },
    [performDelete, workspaceId],
  );

  const duplicateFile = useCallback(
    async (ref: CheckoutRef, node: FileTreeNodeInfo) => {
      setContextMenu(null);
      if (node.isDir) return;
      try {
        const data = await readScopedFile(workspaceId, ref, node.path);
        if (data.binary || data.truncated) {
          showToast("Only complete text files can be duplicated", {
            type: "error",
          });
          return;
        }
        const parent = dirname(node.path);
        const entries = sortedEntries(
          (await listScopedDir(workspaceId, ref, parent)).entries,
        );
        const nextName = duplicateName(basename(node.path), entries);
        const nextPath = joinPath(parent, nextName);
        await writeScopedFile(workspaceId, ref, nextPath, data.content ?? "");
        markIndexStale();
        void refreshCheckouts();
        void refreshGitStatus();
        refreshParents(ref, nextPath);
        openFile(ref, nextPath);
      } catch (err) {
        showToast(err instanceof Error ? err.message : String(err), {
          type: "error",
        });
      }
    },
    [
      workspaceId,
      refreshParents,
      openFile,
      showToast,
      markIndexStale,
      refreshCheckouts,
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
    (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      event: MouseEvent<HTMLDivElement>,
    ) => {
      setContextMenu({ ref, node, x: event.clientX, y: event.clientY });
    },
    [],
  );

  const performMove = useCallback(
    async (
      ref: CheckoutRef,
      node: FileTreeNodeInfo,
      targetFolderPath: string,
    ) => {
      const move = resolveMoveToTarget(node.path, targetFolderPath);
      if (!move) return;
      const applyMove = async (overwrite: boolean) => {
        await moveScopedPath(workspaceId, ref, move.from, move.to, overwrite);
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

      store.getState().retargetPathPrefix(ref, move.from, move.to);
      markIndexStale();
      void refreshCheckouts();
      void refreshGitStatus();
      refreshParents(ref, move.from, move.to);
      revealInTree(ref, move.to);
      showToast("Moved", { type: "success" });
      setMoveDialog(null);
    },
    [
      workspaceId,
      store,
      markIndexStale,
      refreshCheckouts,
      refreshGitStatus,
      refreshParents,
      revealInTree,
      showToast,
    ],
  );

  const selectedTab = activeTab;
  const mruKeys = useMemo(() => mru.map(tabIdentityKey), [mru]);

  const toggleRoot = useCallback((key: string) => {
    setExpandedRoots((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }, []);

  const renderCheckoutRoot = useCallback(
    (root: Extract<FileTreeRoot, { kind: "checkout" }>, depth: number) => {
      const key = checkoutRefKey(root.ref);
      const expanded = expandedRoots.has(key);
      return (
        <div key={root.id}>
          <RootRow
            root={root}
            depth={depth}
            expanded={expanded}
            onToggle={() => toggleRoot(key)}
          />
          {expanded && root.exists && (
            <CheckoutTreeBlock
              refInfo={root.ref}
              depthOffset={depth + 1}
              selectedTab={selectedTab}
              inlineEdit={inlineEdit}
              gitStatus={gitStatusByRef[key] ?? {}}
              revealRequest={treeRevealRequests[key]}
              refreshRequest={treeRefreshRequests[key]}
              onOpenFile={openFile}
              onContextMenu={handleContextMenu}
              onRequestRename={beginRename}
              onRequestDelete={requestDelete}
              onInlineEditChange={(value) =>
                setInlineEdit((prev) =>
                  prev ? { ...prev, edit: { ...prev.edit, value } } : prev,
                )
              }
              onInlineEditCommit={() => void commitInlineEdit()}
              onInlineEditCancel={() => setInlineEdit(null)}
            />
          )}
        </div>
      );
    },
    [
      beginRename,
      commitInlineEdit,
      expandedRoots,
      gitStatusByRef,
      handleContextMenu,
      inlineEdit,
      openFile,
      requestDelete,
      selectedTab,
      toggleRoot,
      treeRefreshRequests,
      treeRevealRequests,
    ],
  );

  const renderRoot = useCallback(
    (root: FileTreeRoot) => {
      if (root.kind === "checkout") return renderCheckoutRoot(root, 0);
      if (root.flattenedRef) {
        const key = checkoutRefKey(root.flattenedRef);
        const expanded = expandedRoots.has(key);
        return (
          <div key={root.id}>
            <RootRow
              root={root}
              expanded={expanded}
              onToggle={() => toggleRoot(key)}
            />
            {expanded && root.exists && (
              <CheckoutTreeBlock
                refInfo={root.flattenedRef}
                depthOffset={1}
                selectedTab={selectedTab}
                inlineEdit={inlineEdit}
                gitStatus={gitStatusByRef[key] ?? {}}
                revealRequest={treeRevealRequests[key]}
                refreshRequest={treeRefreshRequests[key]}
                onOpenFile={openFile}
                onContextMenu={handleContextMenu}
                onRequestRename={beginRename}
                onRequestDelete={requestDelete}
                onInlineEditChange={(value) =>
                  setInlineEdit((prev) =>
                    prev ? { ...prev, edit: { ...prev.edit, value } } : prev,
                  )
                }
                onInlineEditCommit={() => void commitInlineEdit()}
                onInlineEditCancel={() => setInlineEdit(null)}
              />
            )}
          </div>
        );
      }
      const expanded = expandedRoots.has(root.id);
      return (
        <div key={root.id}>
          <RootRow
            root={root}
            expanded={expanded}
            onToggle={() => toggleRoot(root.id)}
          />
          {expanded &&
            root.children.map((child) => renderCheckoutRoot(child, 1))}
        </div>
      );
    },
    [
      beginRename,
      commitInlineEdit,
      expandedRoots,
      gitStatusByRef,
      handleContextMenu,
      inlineEdit,
      openFile,
      renderCheckoutRoot,
      requestDelete,
      selectedTab,
      toggleRoot,
      treeRefreshRequests,
      treeRevealRequests,
    ],
  );

  const renderWorkspaceSection = useCallback(
    (
      section: (typeof sections)[number],
      root: Extract<FileTreeRoot, { kind: "checkout" }>,
    ) => {
      const key = checkoutRefKey(root.ref);
      const expanded = expandedRoots.has(key);
      return (
        <section
          key={section.id}
          className={styles.rootSection}
          data-dimmed={section.dimmed || undefined}
        >
          <h2 className={styles.rootSectionHeading}>
            <button
              type="button"
              className={styles.workspaceSectionToggle}
              aria-expanded={expanded}
              onClick={() => toggleRoot(key)}
            >
              <span
                className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
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
              <span>{section.title}</span>
              {root.changeCount > 0 && (
                <span className={styles.checkoutBadge}>{root.changeCount}</span>
              )}
            </button>
          </h2>
          {expanded && root.exists && (
            <CheckoutTreeBlock
              refInfo={root.ref}
              depthOffset={0}
              selectedTab={selectedTab}
              inlineEdit={inlineEdit}
              gitStatus={gitStatusByRef[key] ?? {}}
              revealRequest={treeRevealRequests[key]}
              refreshRequest={treeRefreshRequests[key]}
              onOpenFile={openFile}
              onContextMenu={handleContextMenu}
              onRequestRename={beginRename}
              onRequestDelete={requestDelete}
              onInlineEditChange={(value) =>
                setInlineEdit((prev) =>
                  prev ? { ...prev, edit: { ...prev.edit, value } } : prev,
                )
              }
              onInlineEditCommit={() => void commitInlineEdit()}
              onInlineEditCancel={() => setInlineEdit(null)}
            />
          )}
        </section>
      );
    },
    [
      beginRename,
      commitInlineEdit,
      expandedRoots,
      gitStatusByRef,
      handleContextMenu,
      inlineEdit,
      openFile,
      requestDelete,
      selectedTab,
      toggleRoot,
      treeRefreshRequests,
      treeRevealRequests,
    ],
  );

  return (
    <div className={styles.container}>
      <div
        className={styles.treePanel}
        style={{ ["--tree-width"]: `${treeWidth}px` } as CSSProperties}
      >
        {searchPanelOpen ? (
          <FileSearchPanel
            workspaceId={workspaceId}
            scopeRef={workspaceRef}
            onOpenResult={(path, line) =>
              openFile(workspaceRef, path, undefined, line)
            }
            onFilesChanged={(paths) => {
              markIndexStale();
              void refreshCheckouts();
              void refreshGitStatus();
              refreshParents(workspaceRef, ...paths);
              showToast("Replace applied", { type: "success" });
            }}
            onClose={() => setSearchPanelOpen(false)}
          />
        ) : (
          <>
            <div className={styles.toolbar}>
              <LensToggle
                lens={lens}
                changeCount={checkoutChangeCount}
                onChange={changeLens}
              />
              <button
                type="button"
                className={styles.quickOpenButton}
                onClick={() => setQuickOpenOpen(true)}
              >
                <svg viewBox="0 0 16 16" aria-hidden="true">
                  <circle
                    cx="7"
                    cy="7"
                    r="4"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  />
                  <path
                    d="M10.5 10.5L14 14"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                    strokeLinecap="round"
                  />
                </svg>
                <span>Go to file...</span>
                <kbd>Cmd+P</kbd>
              </button>
            </div>
            <div className={styles.treeScroll}>
              {checkoutError && (
                <div className={styles.checkoutError}>{checkoutError}</div>
              )}
              {lens === "changes" ? (
                <ChangesList
                  groups={visibleChangeGroups}
                  onOpenDiff={(request) =>
                    openDiff(store.getState().activeGroup, request)
                  }
                />
              ) : (
                sections.map((section) => {
                  const workspaceRoot =
                    section.id === "workspace"
                      ? section.roots.find(
                          (
                            root,
                          ): root is Extract<
                            FileTreeRoot,
                            { kind: "checkout" }
                          > =>
                            root.kind === "checkout" &&
                            root.ref.scope === "workspace",
                        )
                      : undefined;
                  if (workspaceRoot) {
                    return renderWorkspaceSection(section, workspaceRoot);
                  }
                  return (
                    <section
                      key={section.id}
                      className={styles.rootSection}
                      data-dimmed={section.dimmed || undefined}
                    >
                      <h2 className={styles.rootSectionHeading}>
                        {section.title}
                      </h2>
                      {section.roots.length === 0 ? (
                        <div className={styles.rootSectionEmpty}>None</div>
                      ) : (
                        section.roots.map(renderRoot)
                      )}
                    </section>
                  );
                })
              )}
            </div>
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
              diffView={diffViews[0] ?? null}
              revisionView={revisionViews[0] ?? null}
              isActiveGroup={activeGroup === 0}
              dirty={dirty}
              onSelectTab={selectTab}
              onCloseTab={closeTab}
              onSplitRight={splitRight}
              onNavigate={navigateToDir}
              onSaved={handleSaved}
              onOpenDiff={openDiff}
              onCloseDiff={closeDiff}
              onOpenRevision={openRevision}
              onCloseRevision={closeRevision}
              onOpenEditableFile={(groupIndex, ref, path) =>
                openFile(ref, path, groupIndex)
              }
              onRestoreSnapshot={restoreSnapshot}
              historyRefreshKey={historyRefreshKey}
              reloadToken={
                groups[0]?.active
                  ? fileReloadTokens[groups[0].active]
                  : undefined
              }
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
                  diffView={diffViews[1] ?? null}
                  revisionView={revisionViews[1] ?? null}
                  isActiveGroup={activeGroup === 1}
                  dirty={dirty}
                  onSelectTab={selectTab}
                  onCloseTab={closeTab}
                  onSplitRight={splitRight}
                  onNavigate={navigateToDir}
                  onSaved={handleSaved}
                  onOpenDiff={openDiff}
                  onCloseDiff={closeDiff}
                  onOpenRevision={openRevision}
                  onCloseRevision={closeRevision}
                  onOpenEditableFile={(groupIndex, ref, path) =>
                    openFile(ref, path, groupIndex)
                  }
                  onRestoreSnapshot={restoreSnapshot}
                  historyRefreshKey={historyRefreshKey}
                  reloadToken={
                    groups[1]?.active
                      ? fileReloadTokens[groups[1].active]
                      : undefined
                  }
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
          onNewFile={(node) =>
            beginCreate(contextMenu.ref, "create-file", node)
          }
          onNewFolder={(node) =>
            beginCreate(contextMenu.ref, "create-folder", node)
          }
          onRename={(node) => beginRename(contextMenu.ref, node)}
          onDelete={(node) => requestDelete(contextMenu.ref, node)}
          onMove={(node) => {
            setContextMenu(null);
            setMoveDialog({ ref: contextMenu.ref, node });
          }}
          onDuplicate={(node) => duplicateFile(contextMenu.ref, node)}
          onCopyPath={copyPath}
        />
      )}
      {deleteConfirm && (
        <DeleteConfirmDialog
          node={deleteConfirm.node}
          onCancel={() => setDeleteConfirm(null)}
          onConfirm={(skip) =>
            void performDelete(deleteConfirm.ref, deleteConfirm.node, skip)
          }
        />
      )}
      {moveDialog && (
        <MoveToDialog
          state={moveDialog}
          onCancel={() => setMoveDialog(null)}
          onConfirm={(target) =>
            void performMove(moveDialog.ref, moveDialog.node, target)
          }
        />
      )}
      <QuickOpenPalette
        isOpen={quickOpenOpen}
        items={quickOpenItems}
        mruKeys={mruKeys}
        isLoading={quickOpenLoading}
        error={quickOpenError}
        truncated={quickOpenTruncated}
        onClose={() => setQuickOpenOpen(false)}
        onOpen={(item) => openFile(item.ref, item.path)}
      />
    </div>
  );
}

export function FileBrowser({
  mode = "workspace",
  agentName,
}: FileBrowserProps) {
  const { workspaceId } = useWorkspaceContext();
  return (
    <FileBrowserStoreProvider key={workspaceId} workspaceId={workspaceId}>
      <FileBrowserInner mode={mode} agentName={agentName} />
    </FileBrowserStoreProvider>
  );
}

/**
 * WorkspaceFileBrowser remains the exported compatibility wrapper for the
 * Files page lazy import.
 */
export function WorkspaceFileBrowser({
  mode = "workspace",
  agentName,
}: FileBrowserProps) {
  return <FileBrowser mode={mode} agentName={agentName} />;
}
