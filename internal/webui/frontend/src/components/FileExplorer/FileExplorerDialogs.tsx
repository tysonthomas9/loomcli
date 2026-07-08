import { useMemo, useState } from "react";

import type { FileEntry } from "@/api/workspace";
import { useScopedFileTree } from "@/hooks";
import { checkoutLabel } from "@/utils/fileExplorerRefs";

import type { FileTreeNodeInfo } from "./FileTree";
import {
  dirname,
  joinPath,
  resolveMoveToTarget,
  sortedEntries,
} from "./fileExplorerLocalUtils";
import styles from "./FileExplorer.module.css";
import type {
  ContextMenuState,
  MoveDialogState,
} from "./workspaceFileBrowserTypes";

export function DeleteConfirmDialog({
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

export function ContextMenu({
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

export function MoveToDialog({
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
