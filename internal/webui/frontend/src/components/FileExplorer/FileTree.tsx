import {
  createContext,
  forwardRef,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type ComponentType,
  type CSSProperties,
  type FormEvent,
  type HTMLAttributes,
  type KeyboardEvent,
  type MouseEvent,
  type RefObject,
} from "react";
import {
  Tree,
  type NodeRendererProps,
  type RowRendererProps,
  type TreeApi,
} from "react-arborist";
import { RowContainer } from "react-arborist/dist/module/components/row-container";
import { useDataUpdates, useTreeApi } from "react-arborist/dist/module/context";
import { FixedSizeList, type ListChildComponentProps } from "react-window";

import type { FileEntry } from "@/api/workspace";

import {
  buildFolderGitDecorations,
  gitDecorationForStatus,
  type FolderGitDecoration,
} from "./gitDecorations";
import styles from "./FileExplorer.module.css";

export interface FileTreeNodeInfo {
  path: string;
  name: string;
  isDir: boolean;
  depth: number;
}

export interface FileTreeInlineEdit {
  kind: "create-file" | "create-folder" | "rename";
  parentPath: string;
  path?: string | undefined;
  value: string;
  isDir: boolean;
}

export interface FileTreeProps {
  treeData: Map<string, FileEntry[]>;
  expanded: Set<string>;
  selectedPath: string | null;
  filterText: string;
  onToggle: (dirPath: string) => Promise<void>;
  onSelectFile: (filePath: string | null) => void;
  onContextMenuNode?: (
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onRequestRename?: (node: FileTreeNodeInfo) => void;
  onRequestDelete?: (node: FileTreeNodeInfo) => void;
  inlineEdit?: FileTreeInlineEdit | null | undefined;
  onInlineEditChange?: (value: string) => void;
  onInlineEditCommit?: () => void;
  onInlineEditCancel?: () => void;
  gitStatus?: Record<string, string> | undefined;
  /** When set, scroll this path into view + focus it once it appears (jump-to). */
  scrollToPath?: string | null | undefined;
  /** Visual depth offset when this tree is nested under a semantic root. */
  depthOffset?: number | undefined;
  /** Stable id prefix to avoid aria-activedescendant collisions across roots. */
  idPrefix?: string | undefined;
}

interface EntryTreeItem {
  kind: "entry";
  id: string;
  path: string;
  name: string;
  isDir: boolean;
  depth: number;
  isExpanded: boolean;
  children?: TreeItem[];
}

interface InlineTreeItem {
  kind: "inline";
  id: string;
  path: string;
  name: string;
  isDir: boolean;
  depth: number;
  parentPath: string;
}

type TreeItem = EntryTreeItem | InlineTreeItem;

/** A single entry node in the flattened, visible-order tree. */
interface VisNode {
  path: string;
  name: string;
  isDir: boolean;
  depth: number;
  isExpanded: boolean;
}

interface BuiltTree {
  data: TreeItem[];
  visibleEntries: VisNode[];
  visibleCount: number;
  dirPaths: string[];
}

const ROW_HEIGHT = 28;
const INDENT_WIDTH = 16;
const BASE_PADDING = 8;

function highlightMatch(name: string, filter: string): JSX.Element {
  if (!filter) return <>{name}</>;
  const lower = name.toLowerCase();
  const idx = lower.indexOf(filter.toLowerCase());
  if (idx === -1) return <>{name}</>;
  return (
    <>
      {name.slice(0, idx)}
      <mark className={styles.highlight}>
        {name.slice(idx, idx + filter.length)}
      </mark>
      {name.slice(idx + filter.length)}
    </>
  );
}

/** Build full path from parent dir path and entry name. */
function buildPath(parentPath: string, name: string): string {
  return parentPath ? `${parentPath}/${name}` : name;
}

/** Lowercased file extension (no leading dot), or undefined when none. */
function fileExtension(name: string): string | undefined {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1).toLowerCase() : undefined;
}

/** Stable DOM id for a node (for aria-activedescendant). */
function nodeDomId(prefix: string, path: string): string {
  return `${prefix}-${encodeURIComponent(path)}`;
}

function parentOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

function shouldShow(
  entry: FileEntry,
  parentPath: string,
  filter: string,
  treeData: Map<string, FileEntry[]>,
): boolean {
  if (!filter) return true;
  if (entry.name.toLowerCase().includes(filter.toLowerCase())) return true;
  if (entry.is_dir) {
    const dirPath = buildPath(parentPath, entry.name);
    const children = treeData.get(dirPath);
    if (children) {
      return children.some((child) =>
        shouldShow(child, dirPath, filter, treeData),
      );
    }
  }
  return false;
}

function inlineId(parentPath: string): string {
  return `__inline__:${parentPath}`;
}

function makeInlineItem(inlineEdit: FileTreeInlineEdit, depth: number) {
  return {
    kind: "inline",
    id: inlineId(inlineEdit.parentPath),
    path: inlineId(inlineEdit.parentPath),
    name: inlineEdit.value,
    isDir: inlineEdit.isDir,
    depth,
    parentPath: inlineEdit.parentPath,
  } satisfies InlineTreeItem;
}

function buildTree(
  treeData: Map<string, FileEntry[]>,
  expanded: Set<string>,
  filter: string,
  inlineEdit?: FileTreeInlineEdit | null | undefined,
): BuiltTree {
  const visibleEntries: VisNode[] = [];
  const dirPaths: string[] = [];
  let visibleCount = 0;
  const createInline =
    inlineEdit && inlineEdit.kind !== "rename" ? inlineEdit : null;

  const walk = (
    parentPath: string,
    depth: number,
    includeEntries = true,
  ): TreeItem[] => {
    const items: TreeItem[] = [];
    if (createInline?.parentPath === parentPath) {
      items.push(makeInlineItem(createInline, depth));
      visibleCount += 1;
    }
    if (!includeEntries) return items;

    const entries = (treeData.get(parentPath) ?? []).filter((entry) =>
      shouldShow(entry, parentPath, filter, treeData),
    );
    for (const entry of entries) {
      const path = buildPath(parentPath, entry.name);
      const isExpanded = entry.is_dir && expanded.has(path);
      if (entry.is_dir) dirPaths.push(path);

      const item: EntryTreeItem = {
        kind: "entry",
        id: path,
        path,
        name: entry.name,
        isDir: entry.is_dir,
        depth,
        isExpanded,
      };
      items.push(item);
      visibleEntries.push({
        path,
        name: entry.name,
        isDir: entry.is_dir,
        depth,
        isExpanded,
      });
      visibleCount += 1;
      if (entry.is_dir) {
        const hasInlineChild = createInline?.parentPath === path;
        item.children =
          isExpanded || hasInlineChild ? walk(path, depth + 1, isExpanded) : [];
      }
    }
    return items;
  };

  return {
    data: walk("", 0),
    visibleEntries,
    visibleCount,
    dirPaths,
  };
}

function openStateFor(
  expanded: Set<string>,
  forcedOpenPath?: string | null,
): Record<string, boolean> {
  const state: Record<string, boolean> = {};
  expanded.forEach((path) => {
    state[path] = true;
  });
  if (forcedOpenPath) state[forcedOpenPath] = true;
  return state;
}

const DirChevron = ({ expanded }: { expanded: boolean }) => (
  <span
    className={`${styles.chevron} ${expanded ? styles.chevronExpanded : ""}`}
  >
    <svg viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M6 4l4 4-4 4"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
      />
    </svg>
  </span>
);

const NodeIcon = ({ name, isDir }: { name: string; isDir: boolean }) => (
  <span
    className={styles.icon}
    data-ext={isDir ? undefined : fileExtension(name)}
  >
    {isDir ? (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path
          d="M2 3h4l2 2h6v8H2V3z"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.2"
          strokeLinejoin="round"
        />
      </svg>
    ) : (
      <svg viewBox="0 0 16 16" aria-hidden="true">
        <path
          d="M4 2h5l3 3v9H4V2z"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.2"
          strokeLinejoin="round"
        />
        <path
          d="M9 2v3h3"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.2"
        />
      </svg>
    )}
  </span>
);

function InlineEditInput({
  value,
  onChange,
  onCommit,
  onCancel,
}: {
  value: string;
  onChange?: ((value: string) => void) | undefined;
  onCommit?: (() => void) | undefined;
  onCancel?: (() => void) | undefined;
}) {
  const inputRef = useRef<HTMLInputElement>(null);
  const canceledRef = useRef(false);

  useEffect(() => {
    inputRef.current?.focus();
    inputRef.current?.select();
  }, []);

  const commit = (event?: FormEvent | KeyboardEvent<HTMLInputElement>) => {
    event?.preventDefault();
    onCommit?.();
  };

  return (
    <input
      ref={inputRef}
      className={styles.inlineEditInput}
      value={value}
      onClick={(event) => event.stopPropagation()}
      onChange={(event) => onChange?.(event.target.value)}
      onBlur={() => {
        if (!canceledRef.current) onCommit?.();
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter") {
          commit(event);
        }
        if (event.key === "Escape") {
          canceledRef.current = true;
          event.preventDefault();
          onCancel?.();
        }
      }}
      aria-label="File name"
    />
  );
}

interface FileTreeRenderContextValue {
  idPrefix: string;
  depthOffset: number;
  activePath: string | null;
  selectedPath: string | null;
  filterText: string;
  inlineEdit: FileTreeInlineEdit | null | undefined;
  gitStatus: Record<string, string>;
  folderDecorations: Map<string, FolderGitDecoration>;
  treeRef: RefObject<HTMLDivElement>;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
  onActivate: (node: VisNode) => void;
  onContextMenuNode:
    | ((node: FileTreeNodeInfo, event: MouseEvent<HTMLDivElement>) => void)
    | undefined;
  onFocusPath: (path: string) => void;
  onInlineEditChange: ((value: string) => void) | undefined;
  onInlineEditCommit: (() => void) | undefined;
  onInlineEditCancel: (() => void) | undefined;
}

const FileTreeRenderContext = createContext<FileTreeRenderContextValue | null>(
  null,
);

function useFileTreeRenderContext(): FileTreeRenderContextValue {
  const value = useContext(FileTreeRenderContext);
  if (!value) throw new Error("FileTree render context missing");
  return value;
}

const TreeListOuter = forwardRef<
  HTMLDivElement,
  HTMLAttributes<HTMLDivElement>
>(function TreeListOuter({ style, ...props }, ref) {
  return <div {...props} ref={ref} style={{ ...style, overflow: "visible" }} />;
});

function ArboristContainer(): JSX.Element {
  useDataUpdates();
  const tree = useTreeApi<TreeItem>();
  const { treeRef, activePath, idPrefix, onKeyDown } =
    useFileTreeRenderContext();
  const itemCount = tree.visibleNodes.length;
  const Row = RowContainer as ComponentType<ListChildComponentProps>;

  return (
    <div
      ref={treeRef}
      className={styles.tree}
      role="tree"
      aria-label={tree.props["aria-label"]}
      tabIndex={0}
      aria-activedescendant={
        activePath ? nodeDomId(idPrefix, activePath) : undefined
      }
      onKeyDown={onKeyDown}
      style={{
        height: tree.height,
        width: tree.width,
        minHeight: 0,
        minWidth: 0,
      }}
    >
      {itemCount > 0 && (
        <FixedSizeList
          outerRef={tree.listEl}
          itemCount={itemCount}
          height={tree.height}
          width={tree.width}
          itemSize={tree.rowHeight}
          overscanCount={tree.overscanCount}
          itemKey={(index) => tree.visibleNodes[index]?.id ?? index}
          ref={tree.list as RefObject<FixedSizeList>}
          outerElementType={TreeListOuter}
        >
          {Row}
        </FixedSizeList>
      )}
    </div>
  );
}

function ArboristRow({
  node,
  attrs,
  innerRef,
  children,
}: RowRendererProps<TreeItem>): JSX.Element {
  const item = node.data;
  const {
    idPrefix,
    depthOffset,
    activePath,
    selectedPath,
    gitStatus,
    folderDecorations,
    onActivate,
    onContextMenuNode,
    onFocusPath,
  } = useFileTreeRenderContext();
  const visualDepth = item.depth + depthOffset;
  const baseStyle = attrs.style as CSSProperties;

  if (item.kind === "inline") {
    return (
      <div
        {...attrs}
        ref={innerRef}
        role="treeitem"
        aria-level={visualDepth + 1}
        aria-label={item.name || "New file"}
        data-path={item.path}
        data-dir={item.isDir || undefined}
        className={styles.inlineEditRow}
        style={{
          ...baseStyle,
          paddingLeft: BASE_PADDING + visualDepth * INDENT_WIDTH,
        }}
        data-testid="file-tree-inline-edit"
      >
        {children}
      </div>
    );
  }

  const fileDecoration = item.isDir
    ? null
    : gitDecorationForStatus(gitStatus[item.path]);
  const folderDecoration = item.isDir
    ? folderDecorations.get(item.path)
    : undefined;
  const decorationKind =
    fileDecoration?.kind ??
    (folderDecoration?.conflict
      ? "conflict"
      : folderDecoration?.changed
        ? "modified"
        : undefined);
  const hasConflict =
    !!fileDecoration?.conflict || !!folderDecoration?.conflict;

  return (
    <div
      {...attrs}
      ref={innerRef}
      id={nodeDomId(idPrefix, item.path)}
      data-path={item.path}
      role="treeitem"
      aria-label={item.name}
      title={item.path}
      aria-level={visualDepth + 1}
      aria-expanded={item.isDir ? item.isExpanded : undefined}
      aria-selected={item.path === selectedPath || undefined}
      data-selected={item.path === selectedPath || undefined}
      data-dir={item.isDir || undefined}
      data-focused={item.path === activePath || undefined}
      data-git-status-kind={decorationKind}
      data-conflict={hasConflict || undefined}
      tabIndex={-1}
      className={styles.treeNode}
      style={{
        ...baseStyle,
        paddingLeft: BASE_PADDING + visualDepth * INDENT_WIDTH,
      }}
      onClick={() => onActivate(item)}
      onContextMenu={(event) => {
        event.preventDefault();
        onFocusPath(item.path);
        onContextMenuNode?.(item, event);
      }}
    >
      {children}
    </div>
  );
}

function EntryNodeContent({ item }: { item: EntryTreeItem }): JSX.Element {
  const {
    filterText,
    inlineEdit,
    gitStatus,
    folderDecorations,
    onInlineEditChange,
    onInlineEditCommit,
    onInlineEditCancel,
  } = useFileTreeRenderContext();
  const fileDecoration = item.isDir
    ? null
    : gitDecorationForStatus(gitStatus[item.path]);
  const folderDecoration = item.isDir
    ? folderDecorations.get(item.path)
    : undefined;
  const hasConflict =
    !!fileDecoration?.conflict || !!folderDecoration?.conflict;

  return (
    <>
      {item.isDir ? (
        <DirChevron expanded={item.isExpanded} />
      ) : (
        <span className={styles.fileIconSpacer} />
      )}
      <NodeIcon name={item.name} isDir={item.isDir} />
      {inlineEdit?.kind === "rename" && inlineEdit.path === item.path ? (
        <InlineEditInput
          value={inlineEdit.value}
          onChange={onInlineEditChange}
          onCommit={onInlineEditCommit}
          onCancel={onInlineEditCancel}
        />
      ) : (
        <span className={styles.fileName}>
          {highlightMatch(item.name, filterText)}
        </span>
      )}
      {item.isDir && folderDecoration?.changed && (
        <span
          className={styles.gitStatusDot}
          data-conflict={folderDecoration.conflict || undefined}
          aria-hidden="true"
        />
      )}
      {!item.isDir && fileDecoration && (
        <span
          className={styles.gitStatusDot}
          data-kind={fileDecoration.kind}
          data-conflict={fileDecoration.conflict || undefined}
          aria-hidden="true"
        />
      )}
      {hasConflict && (
        <span className={styles.conflictBadge} title="Merge conflict">
          !
        </span>
      )}
    </>
  );
}

function InlineNodeContent({ item }: { item: InlineTreeItem }): JSX.Element {
  const {
    inlineEdit,
    onInlineEditChange,
    onInlineEditCommit,
    onInlineEditCancel,
  } = useFileTreeRenderContext();

  return (
    <>
      <span className={styles.fileIconSpacer} />
      <NodeIcon name={inlineEdit?.value ?? item.name} isDir={item.isDir} />
      <InlineEditInput
        value={inlineEdit?.value ?? item.name}
        onChange={onInlineEditChange}
        onCommit={onInlineEditCommit}
        onCancel={onInlineEditCancel}
      />
    </>
  );
}

function ArboristNode({
  node,
}: NodeRendererProps<TreeItem>): JSX.Element | null {
  const item = node.data;
  if (item.kind === "inline") return <InlineNodeContent item={item} />;
  return <EntryNodeContent item={item} />;
}

export function FileTree({
  treeData,
  expanded,
  selectedPath,
  filterText,
  onToggle,
  onSelectFile,
  onContextMenuNode,
  onRequestRename,
  onRequestDelete,
  inlineEdit,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
  gitStatus = {},
  scrollToPath,
  depthOffset = 0,
  idPrefix = "ft",
}: FileTreeProps) {
  const treeRef = useRef<HTMLDivElement>(null);
  const arboristRef = useRef<TreeApi<TreeItem>>();
  const [focusedPath, setFocusedPath] = useState<string | null>(null);
  const lastRevealRef = useRef<string | null>(null);

  const builtTree = useMemo(
    () => buildTree(treeData, expanded, filterText, inlineEdit),
    [treeData, expanded, filterText, inlineEdit],
  );
  const createInlineParent =
    inlineEdit && inlineEdit.kind !== "rename" ? inlineEdit.parentPath : null;
  const folderDecorations = useMemo(
    () => buildFolderGitDecorations(gitStatus),
    [gitStatus],
  );
  const initialOpenState = useMemo(
    () => openStateFor(expanded, createInlineParent),
    [createInlineParent, expanded],
  );

  useLayoutEffect(() => {
    const tree = arboristRef.current;
    if (!tree) return;
    for (const path of builtTree.dirPaths) {
      if (expanded.has(path) || createInlineParent === path) {
        tree.open(path, false);
      } else tree.close(path, false);
    }
  }, [builtTree.dirPaths, createInlineParent, expanded]);

  // The active (keyboard) node: the explicitly focused one if still visible,
  // else the selected file, else the first entry node.
  const activePath = useMemo(() => {
    const has = (path: string | null) =>
      path != null && builtTree.visibleEntries.some((n) => n.path === path);
    if (has(focusedPath)) return focusedPath;
    if (has(selectedPath)) return selectedPath;
    return builtTree.visibleEntries[0]?.path ?? null;
  }, [focusedPath, selectedPath, builtTree.visibleEntries]);

  const focusNode = useCallback((path: string | null) => {
    setFocusedPath(path);
    if (!path) return;
    const el = treeRef.current?.querySelector(
      `[data-path="${path.replace(/"/g, '\\"')}"]`,
    );
    // scrollIntoView is unimplemented in jsdom; guard so tests don't throw.
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "nearest" });
    }
  }, []);

  const activate = useCallback(
    (node: VisNode) => {
      setFocusedPath(node.path);
      treeRef.current?.focus();
      if (node.isDir) {
        void onToggle(node.path);
      } else {
        onSelectFile(node.path);
      }
    },
    [onToggle, onSelectFile],
  );

  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLDivElement>) => {
      if (builtTree.visibleEntries.length === 0) return;
      const idx = activePath
        ? builtTree.visibleEntries.findIndex((n) => n.path === activePath)
        : -1;
      const cur = idx >= 0 ? builtTree.visibleEntries[idx] : undefined;

      switch (event.key) {
        case "ArrowDown":
          event.preventDefault();
          focusNode(
            builtTree.visibleEntries[
              Math.min(builtTree.visibleEntries.length - 1, idx + 1)
            ]?.path ?? null,
          );
          break;
        case "ArrowUp":
          event.preventDefault();
          focusNode(
            builtTree.visibleEntries[Math.max(0, idx - 1)]?.path ?? null,
          );
          break;
        case "Home":
          event.preventDefault();
          focusNode(builtTree.visibleEntries[0]?.path ?? null);
          break;
        case "End":
          event.preventDefault();
          focusNode(
            builtTree.visibleEntries[builtTree.visibleEntries.length - 1]
              ?.path ?? null,
          );
          break;
        case "ArrowRight":
          event.preventDefault();
          if (cur?.isDir && !cur.isExpanded) void onToggle(cur.path);
          else if (cur?.isDir && cur.isExpanded) {
            focusNode(builtTree.visibleEntries[idx + 1]?.path ?? null);
          }
          break;
        case "ArrowLeft":
          event.preventDefault();
          if (cur?.isDir && cur.isExpanded) void onToggle(cur.path);
          else if (cur) {
            const parent = parentOf(cur.path);
            if (builtTree.visibleEntries.some((n) => n.path === parent)) {
              focusNode(parent);
            }
          }
          break;
        case "Enter":
        case " ":
          event.preventDefault();
          if (cur) activate(cur);
          break;
        case "F2":
          if (cur) {
            event.preventDefault();
            onRequestRename?.(cur);
          }
          break;
        case "Delete":
        case "Backspace":
          if (cur) {
            event.preventDefault();
            onRequestDelete?.(cur);
          }
          break;
        default:
          // Type-ahead: jump to the next node whose name starts with the key.
          if (
            event.key.length === 1 &&
            !event.ctrlKey &&
            !event.metaKey &&
            !event.altKey
          ) {
            const lower = event.key.toLowerCase();
            for (let i = 1; i <= builtTree.visibleEntries.length; i++) {
              const node =
                builtTree.visibleEntries[
                  (idx + i) % builtTree.visibleEntries.length
                ];
              if (node && node.name.toLowerCase().startsWith(lower)) {
                focusNode(node.path);
                break;
              }
            }
          }
      }
    },
    [
      activePath,
      activate,
      builtTree.visibleEntries,
      focusNode,
      onRequestDelete,
      onRequestRename,
      onToggle,
    ],
  );

  // Jump-to-folder: when a reveal target appears in the tree, focus + scroll it
  // into view once (guarded so expanding other folders doesn't yank the view).
  useEffect(() => {
    if (!scrollToPath || scrollToPath === lastRevealRef.current) return;
    if (!builtTree.visibleEntries.some((n) => n.path === scrollToPath)) return;
    lastRevealRef.current = scrollToPath;
    setFocusedPath(scrollToPath);
    treeRef.current?.focus();
    const el = treeRef.current?.querySelector(
      `[data-path="${scrollToPath.replace(/"/g, '\\"')}"]`,
    );
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "center" });
    }
  }, [scrollToPath, builtTree.visibleEntries]);

  const renderContext = useMemo<FileTreeRenderContextValue>(
    () => ({
      idPrefix,
      depthOffset,
      activePath,
      selectedPath,
      filterText,
      inlineEdit,
      gitStatus,
      folderDecorations,
      treeRef,
      onKeyDown: handleKeyDown,
      onActivate: activate,
      onContextMenuNode,
      onFocusPath: setFocusedPath,
      onInlineEditChange,
      onInlineEditCommit,
      onInlineEditCancel,
    }),
    [
      idPrefix,
      depthOffset,
      activePath,
      selectedPath,
      filterText,
      inlineEdit,
      gitStatus,
      folderDecorations,
      handleKeyDown,
      activate,
      onContextMenuNode,
      onInlineEditChange,
      onInlineEditCommit,
      onInlineEditCancel,
    ],
  );

  const filtering = filterText.length > 0;

  return (
    <FileTreeRenderContext.Provider value={renderContext}>
      <Tree<TreeItem>
        ref={arboristRef}
        data={builtTree.data}
        idAccessor="id"
        childrenAccessor="children"
        openByDefault={false}
        initialOpenState={initialOpenState}
        selection={selectedPath ?? ""}
        disableDrag
        disableDrop
        disableMultiSelection
        disableDeselectOnClick
        rowHeight={ROW_HEIGHT}
        height={builtTree.visibleCount * ROW_HEIGHT}
        width="100%"
        overscanCount={builtTree.visibleCount}
        renderContainer={ArboristContainer}
        renderRow={ArboristRow}
        aria-label="File tree"
      >
        {ArboristNode}
      </Tree>
      {builtTree.visibleEntries.length === 0 && !filtering && (
        <div className={styles.empty}>No files found</div>
      )}
      {builtTree.visibleEntries.length === 0 && filtering && (
        <div className={styles.empty}>
          No matches in loaded folders for &ldquo;{filterText}&rdquo;
        </div>
      )}
    </FileTreeRenderContext.Provider>
  );
}
