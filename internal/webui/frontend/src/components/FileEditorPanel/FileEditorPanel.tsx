/**
 * FileEditorPanel - Two-column editor view with file tree sidebar and CodeMirror editor.
 * Provides file browsing, editing, dirty tracking, save (Cmd+S), and discard confirmation.
 */

import type { FileEntry } from "@/api/workspace";
import type { UseFileTreeReturn } from "@/hooks/common/useFileTree";
import { CodeMirrorEditor } from "@/components/CodeMirrorEditor/CodeMirrorEditor";
import { useFileEditor } from "./useFileEditor";
import type { UseFileEditorReturn } from "./useFileEditor";
import styles from "./FileEditorPanel.module.css";

export interface FileEditorPanelProps {
  /** Agent name to browse/edit files for */
  agentName: string;
  /** Whether this panel is currently active (gates keyboard shortcuts) */
  isActive: boolean;
}

// --- FileTreeNode (recursive) ---

interface FileTreeNodeProps {
  entry: FileEntry;
  parentPath: string;
  depth: number;
  tree: UseFileTreeReturn;
  selectedPath: string | null;
  debouncedFilterText: string;
  onFileSelect: (path: string) => void;
}

function matchesFilter(name: string, filter: string): boolean {
  return name.toLowerCase().includes(filter.toLowerCase());
}

function FileTreeNode({
  entry,
  parentPath,
  depth,
  tree,
  selectedPath,
  debouncedFilterText,
  onFileSelect,
}: FileTreeNodeProps): JSX.Element | null {
  const fullPath = parentPath ? `${parentPath}/${entry.name}` : entry.name;
  const isExpanded = tree.expanded.has(fullPath);
  const isSelected = fullPath === selectedPath;
  const children = tree.treeData.get(fullPath);

  if (entry.is_dir) {
    // For directories, check if any children match (or if no filter)
    const hasVisibleChildren =
      !debouncedFilterText ||
      matchesFilter(entry.name, debouncedFilterText) ||
      (children &&
        children.some((child) =>
          matchesFilter(child.name, debouncedFilterText),
        ));

    if (debouncedFilterText && !hasVisibleChildren) return null;

    return (
      <>
        <button
          className={styles.treeRow}
          style={{ paddingLeft: `${depth * 16 + 8}px` }}
          onClick={() => tree.toggle(fullPath)}
          type="button"
        >
          <span
            className={styles.chevron}
            data-expanded={isExpanded || undefined}
            aria-hidden="true"
          >
            &#9654;
          </span>
          <span className={styles.treeRowName}>{entry.name}</span>
        </button>
        {isExpanded &&
          children &&
          children.map((child) => (
            <FileTreeNode
              key={child.name}
              entry={child}
              parentPath={fullPath}
              depth={depth + 1}
              tree={tree}
              selectedPath={selectedPath}
              debouncedFilterText={debouncedFilterText}
              onFileSelect={onFileSelect}
            />
          ))}
      </>
    );
  }

  // File node
  if (debouncedFilterText && !matchesFilter(entry.name, debouncedFilterText)) {
    return null;
  }

  return (
    <button
      className={`${styles.treeRow} ${isSelected ? styles.selected : ""}`}
      style={{ paddingLeft: `${depth * 16 + 8}px` }}
      onClick={() => onFileSelect(fullPath)}
      type="button"
    >
      <span className={styles.fileIcon} aria-hidden="true">
        &#128196;
      </span>
      <span className={styles.treeRowName}>{entry.name}</span>
    </button>
  );
}

// --- Main component ---

export function FileEditorPanel({
  agentName,
  isActive,
}: FileEditorPanelProps): JSX.Element {
  const editor: UseFileEditorReturn = useFileEditor(agentName, isActive);
  const {
    tree,
    fileContent,
    content,
    language,
    isDirty,
    isSaving,
    pendingAction,
    handleFileSelect,
    handleContentChange,
    save,
    confirmDiscard,
    cancelDiscard,
  } = editor;

  const rootEntries = tree.treeData.get("") ?? [];

  return (
    <div className={styles.container} data-testid="file-editor-panel">
      {/* File Tree Sidebar */}
      <div className={styles.sidebar}>
        <div className={styles.sidebarHeader}>
          <input
            type="text"
            className={styles.filterInput}
            placeholder="Filter files..."
            value={tree.filterText}
            onChange={(e) => tree.setFilterText(e.target.value)}
            aria-label="Filter files"
          />
        </div>
        <div className={styles.treeContainer}>
          {tree.isLoading && (
            <div className={styles.loadingMessage}>Loading...</div>
          )}
          {tree.error && (
            <div className={styles.errorMessage} role="alert">
              {tree.error}
            </div>
          )}
          {!tree.isLoading &&
            !tree.error &&
            rootEntries.map((entry) => (
              <FileTreeNode
                key={entry.name}
                entry={entry}
                parentPath=""
                depth={0}
                tree={tree}
                selectedPath={tree.selectedPath}
                debouncedFilterText={tree.debouncedFilterText}
                onFileSelect={handleFileSelect}
              />
            ))}
        </div>
      </div>

      {/* Editor Area */}
      <div className={styles.editorArea}>
        {tree.selectedPath ? (
          <>
            <div className={styles.editorHeader}>
              <span className={styles.filePath}>{tree.selectedPath}</span>
              {isDirty && (
                <span className={styles.dirtyIndicator}>Modified</span>
              )}
              <button
                className={styles.saveButton}
                onClick={save}
                disabled={!isDirty || isSaving}
                type="button"
              >
                {isSaving ? "Saving..." : "Save"}
              </button>
            </div>
            <div className={styles.editorContent}>
              {fileContent.isLoading ? (
                <div className={styles.emptyState}>Loading file...</div>
              ) : fileContent.error ? (
                <div className={styles.errorMessage} role="alert">
                  {fileContent.error}
                </div>
              ) : fileContent.fileData?.binary ? (
                <div className={styles.emptyState}>
                  Binary file &mdash; cannot display
                </div>
              ) : (
                <CodeMirrorEditor
                  value={content}
                  onChange={handleContentChange}
                  language={language}
                  readOnly={isSaving}
                />
              )}
            </div>
          </>
        ) : (
          <div className={styles.emptyState}>Select a file to edit</div>
        )}
      </div>

      {/* Discard Confirmation Dialog */}
      {pendingAction && (
        <div
          className={styles.dialogOverlay}
          data-testid="discard-dialog-overlay"
        >
          <div
            className={styles.dialog}
            role="dialog"
            aria-modal="true"
            data-testid="discard-dialog"
          >
            <p className={styles.dialogMessage}>
              You have unsaved changes. Discard them?
            </p>
            <div className={styles.dialogActions}>
              <button
                className={styles.secondaryButton}
                onClick={cancelDiscard}
                type="button"
              >
                Keep Editing
              </button>
              <button
                className={styles.dangerButton}
                onClick={confirmDiscard}
                type="button"
              >
                Discard Changes
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
