import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type FormEvent,
  type MouseEvent,
} from "react";
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

interface FileTreeProps {
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
  annotations?:
    | Record<string, { label: string; tone?: "shadowed" | "info" }>
    | undefined;
  /** When set, scroll this path into view + focus it once it appears (jump-to). */
  scrollToPath?: string | null | undefined;
  /** Visual depth offset when this tree is nested under a semantic root. */
  depthOffset?: number | undefined;
  /** Stable id prefix to avoid aria-activedescendant collisions across roots. */
  idPrefix?: string | undefined;
}

/** A single node in the flattened, visible-order tree. */
interface VisNode {
  path: string;
  name: string;
  isDir: boolean;
  depth: number;
  isExpanded: boolean;
}

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

/** Build full path from parent dir path and entry name */
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

/**
 * flattenVisible walks treeData from the root into expanded directories,
 * applying the filter, and returns nodes in render (visible) order. The render
 * and keyboard navigation both consume this single list so they never diverge.
 */
function flattenVisible(
  treeData: Map<string, FileEntry[]>,
  expanded: Set<string>,
  filter: string,
): VisNode[] {
  const out: VisNode[] = [];
  const walk = (parentPath: string, depth: number) => {
    const entries = (treeData.get(parentPath) ?? []).filter((e) =>
      shouldShow(e, parentPath, filter, treeData),
    );
    for (const e of entries) {
      const path = buildPath(parentPath, e.name);
      const isExpanded = e.is_dir && expanded.has(path);
      out.push({ path, name: e.name, isDir: e.is_dir, depth, isExpanded });
      if (isExpanded) walk(path, depth + 1);
    }
  };
  walk("", 0);
  return out;
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

const NodeIcon = ({ node }: { node: VisNode }) => (
  <span
    className={styles.icon}
    data-ext={node.isDir ? undefined : fileExtension(node.name)}
  >
    {node.isDir ? (
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

function InlineEditRow({
  depth,
  value,
  isDir,
  onChange,
  onCommit,
  onCancel,
}: {
  depth: number;
  value: string;
  isDir: boolean;
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

  const submit = (event: FormEvent) => {
    event.preventDefault();
    onCommit?.();
  };

  return (
    <form
      className={styles.inlineEditRow}
      style={{ paddingLeft: 8 + depth * 16 }}
      onSubmit={submit}
      onClick={(event) => event.stopPropagation()}
      data-testid="file-tree-inline-edit"
    >
      <span className={styles.fileIconSpacer} />
      <NodeIcon
        node={{
          path: "__inline__",
          name: value,
          isDir,
          depth,
          isExpanded: false,
        }}
      />
      <input
        ref={inputRef}
        className={styles.inlineEditInput}
        value={value}
        onChange={(event) => onChange?.(event.target.value)}
        onBlur={() => {
          if (!canceledRef.current) onCommit?.();
        }}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            canceledRef.current = true;
            event.preventDefault();
            onCancel?.();
          }
        }}
        aria-label="File name"
      />
    </form>
  );
}

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
          event.preventDefault();
          onCommit?.();
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

function TreeNodeRow({
  node,
  depthOffset,
  idPrefix,
  activePath,
  selectedPath,
  filterText,
  inlineEdit,
  gitStatus,
  folderDecorations,
  annotation,
  onActivate,
  onContextMenuNode,
  onInlineEditChange,
  onInlineEditCommit,
  onInlineEditCancel,
}: {
  node: VisNode;
  depthOffset: number;
  idPrefix: string;
  activePath: string | null;
  selectedPath: string | null;
  filterText: string;
  inlineEdit?: FileTreeInlineEdit | null | undefined;
  gitStatus: Record<string, string>;
  folderDecorations: Map<string, FolderGitDecoration>;
  annotation?: { label: string; tone?: "shadowed" | "info" } | undefined;
  onActivate: (node: VisNode) => void;
  onContextMenuNode?: (
    node: FileTreeNodeInfo,
    event: MouseEvent<HTMLDivElement>,
  ) => void;
  onInlineEditChange?: ((value: string) => void) | undefined;
  onInlineEditCommit?: (() => void) | undefined;
  onInlineEditCancel?: (() => void) | undefined;
}) {
  const fileDecoration = node.isDir
    ? null
    : gitDecorationForStatus(gitStatus[node.path]);
  const folderDecoration = node.isDir
    ? folderDecorations.get(node.path)
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
  const visualDepth = node.depth + depthOffset;

  return (
    <div
      id={nodeDomId(idPrefix, node.path)}
      data-path={node.path}
      role="treeitem"
      aria-label={node.name}
      title={node.path}
      aria-level={visualDepth + 1}
      aria-expanded={node.isDir ? node.isExpanded : undefined}
      aria-selected={node.path === selectedPath || undefined}
      data-selected={node.path === selectedPath || undefined}
      data-dir={node.isDir || undefined}
      data-focused={node.path === activePath || undefined}
      data-git-status-kind={decorationKind}
      data-conflict={hasConflict || undefined}
      data-shadowed={annotation?.tone === "shadowed" || undefined}
      className={styles.treeNode}
      style={{
        paddingLeft: 8 + visualDepth * 16,
      }}
      onClick={() => onActivate(node)}
      onContextMenu={(event) => {
        event.preventDefault();
        onContextMenuNode?.(node, event);
      }}
    >
      {node.isDir ? (
        <DirChevron expanded={node.isExpanded} />
      ) : (
        <span className={styles.fileIconSpacer} />
      )}
      <NodeIcon node={node} />
      {inlineEdit?.kind === "rename" && inlineEdit.path === node.path ? (
        <InlineEditInput
          value={inlineEdit.value}
          onChange={onInlineEditChange}
          onCommit={onInlineEditCommit}
          onCancel={onInlineEditCancel}
        />
      ) : (
        <span className={styles.fileName}>
          {highlightMatch(node.name, filterText)}
        </span>
      )}
      {node.isDir && folderDecoration?.changed && (
        <span
          className={styles.gitStatusDot}
          data-conflict={folderDecoration.conflict || undefined}
          aria-hidden="true"
        />
      )}
      {!node.isDir && fileDecoration && (
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
      {annotation && (
        <span className={styles.nodeAnnotation}>{annotation.label}</span>
      )}
    </div>
  );
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
  annotations = {},
  scrollToPath,
  depthOffset = 0,
  idPrefix = "ft",
}: FileTreeProps) {
  const treeRef = useRef<HTMLDivElement>(null);
  const [focusedPath, setFocusedPath] = useState<string | null>(null);
  const lastRevealRef = useRef<string | null>(null);

  const nodes = useMemo(
    () => flattenVisible(treeData, expanded, filterText),
    [treeData, expanded, filterText],
  );
  const folderDecorations = useMemo(
    () => buildFolderGitDecorations(gitStatus),
    [gitStatus],
  );

  // The active (keyboard) node: the explicitly focused one if still visible,
  // else the selected file, else the first node.
  const activePath = useMemo(() => {
    const has = (p: string | null) =>
      p != null && nodes.some((n) => n.path === p);
    if (has(focusedPath)) return focusedPath;
    if (has(selectedPath)) return selectedPath;
    return nodes[0]?.path ?? null;
  }, [focusedPath, selectedPath, nodes]);

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
    (e: KeyboardEvent<HTMLDivElement>) => {
      if (nodes.length === 0) return;
      const idx = activePath
        ? nodes.findIndex((n) => n.path === activePath)
        : -1;
      const cur = idx >= 0 ? nodes[idx] : undefined;

      switch (e.key) {
        case "ArrowDown":
          e.preventDefault();
          focusNode(nodes[Math.min(nodes.length - 1, idx + 1)]?.path ?? null);
          break;
        case "ArrowUp":
          e.preventDefault();
          focusNode(nodes[Math.max(0, idx - 1)]?.path ?? null);
          break;
        case "Home":
          e.preventDefault();
          focusNode(nodes[0]?.path ?? null);
          break;
        case "End":
          e.preventDefault();
          focusNode(nodes[nodes.length - 1]?.path ?? null);
          break;
        case "ArrowRight":
          e.preventDefault();
          if (cur?.isDir && !cur.isExpanded) void onToggle(cur.path);
          else if (cur?.isDir && cur.isExpanded)
            focusNode(nodes[idx + 1]?.path ?? null);
          break;
        case "ArrowLeft":
          e.preventDefault();
          if (cur?.isDir && cur.isExpanded) void onToggle(cur.path);
          else if (cur) {
            const p = parentOf(cur.path);
            if (nodes.some((n) => n.path === p)) focusNode(p);
          }
          break;
        case "Enter":
        case " ":
          e.preventDefault();
          if (cur) activate(cur);
          break;
        case "F2":
          if (cur) {
            e.preventDefault();
            onRequestRename?.(cur);
          }
          break;
        case "Delete":
        case "Backspace":
          if (cur) {
            e.preventDefault();
            onRequestDelete?.(cur);
          }
          break;
        default:
          // Type-ahead: jump to the next node whose name starts with the key.
          if (e.key.length === 1 && !e.ctrlKey && !e.metaKey && !e.altKey) {
            const lower = e.key.toLowerCase();
            for (let i = 1; i <= nodes.length; i++) {
              const n = nodes[(idx + i) % nodes.length];
              if (n && n.name.toLowerCase().startsWith(lower)) {
                focusNode(n.path);
                break;
              }
            }
          }
      }
    },
    [
      nodes,
      activePath,
      focusNode,
      onToggle,
      activate,
      onRequestRename,
      onRequestDelete,
    ],
  );

  // Jump-to-folder: when a reveal target appears in the tree, focus + scroll it
  // into view once (guarded so expanding other folders doesn't yank the view).
  useEffect(() => {
    if (!scrollToPath || scrollToPath === lastRevealRef.current) return;
    if (!nodes.some((n) => n.path === scrollToPath)) return;
    lastRevealRef.current = scrollToPath;
    setFocusedPath(scrollToPath);
    treeRef.current?.focus();
    const el = treeRef.current?.querySelector(
      `[data-path="${scrollToPath.replace(/"/g, '\\"')}"]`,
    );
    if (el && typeof el.scrollIntoView === "function") {
      el.scrollIntoView({ block: "center" });
    }
  }, [scrollToPath, nodes]);

  const filtering = filterText.length > 0;

  return (
    <div
      ref={treeRef}
      className={styles.tree}
      role="tree"
      aria-label="File tree"
      tabIndex={0}
      aria-activedescendant={
        activePath ? nodeDomId(idPrefix, activePath) : undefined
      }
      onKeyDown={handleKeyDown}
    >
      {inlineEdit &&
        inlineEdit.kind !== "rename" &&
        inlineEdit.parentPath === "" && (
          <InlineEditRow
            depth={depthOffset}
            value={inlineEdit.value}
            isDir={inlineEdit.isDir}
            onChange={onInlineEditChange}
            onCommit={onInlineEditCommit}
            onCancel={onInlineEditCancel}
          />
        )}
      {nodes.map((node) => (
        <div key={node.path}>
          <TreeNodeRow
            node={node}
            depthOffset={depthOffset}
            idPrefix={idPrefix}
            activePath={activePath}
            selectedPath={selectedPath}
            filterText={filterText}
            inlineEdit={inlineEdit}
            gitStatus={gitStatus}
            folderDecorations={folderDecorations}
            annotation={annotations[node.path]}
            onActivate={activate}
            onContextMenuNode={(rowNode, event) => {
              setFocusedPath(rowNode.path);
              onContextMenuNode?.(rowNode, event);
            }}
            onInlineEditChange={onInlineEditChange}
            onInlineEditCommit={onInlineEditCommit}
            onInlineEditCancel={onInlineEditCancel}
          />
          {inlineEdit &&
            inlineEdit.kind !== "rename" &&
            inlineEdit.parentPath === node.path && (
              <InlineEditRow
                depth={node.depth + depthOffset + 1}
                value={inlineEdit.value}
                isDir={inlineEdit.isDir}
                onChange={onInlineEditChange}
                onCommit={onInlineEditCommit}
                onCancel={onInlineEditCancel}
              />
            )}
        </div>
      ))}
      {nodes.length === 0 && !filtering && (
        <div className={styles.empty}>No files found</div>
      )}
      {nodes.length === 0 && filtering && (
        <div className={styles.empty}>
          No matches in loaded folders for &ldquo;{filterText}&rdquo;
        </div>
      )}
    </div>
  );
}
