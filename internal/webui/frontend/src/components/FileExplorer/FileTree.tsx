import { useCallback } from "react";
import type { FileEntry } from "@/api/workspace";
import styles from "./FileExplorer.module.css";

interface FileTreeProps {
  treeData: Map<string, FileEntry[]>;
  expanded: Set<string>;
  selectedPath: string | null;
  filterText: string;
  onToggle: (dirPath: string) => Promise<void>;
  onSelectFile: (filePath: string | null) => void;
}

interface TreeNodeProps {
  entry: FileEntry;
  /** Full path of this entry (parentPath/entry.name or just entry.name for root) */
  fullPath: string;
  depth: number;
  treeData: Map<string, FileEntry[]>;
  expanded: Set<string>;
  selectedPath: string | null;
  filterText: string;
  onToggle: (dirPath: string) => Promise<void>;
  onSelectFile: (filePath: string | null) => void;
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

function TreeNode({
  entry,
  fullPath,
  depth,
  treeData,
  expanded,
  selectedPath,
  filterText,
  onToggle,
  onSelectFile,
}: TreeNodeProps) {
  const isExpanded = expanded.has(fullPath);
  const isSelected = selectedPath === fullPath;
  const children = entry.is_dir ? treeData.get(fullPath) : undefined;
  const indent = 8 + depth * 16;

  const handleClick = useCallback(() => {
    if (entry.is_dir) {
      onToggle(fullPath);
    } else {
      onSelectFile(fullPath);
    }
  }, [entry.is_dir, fullPath, onToggle, onSelectFile]);

  return (
    <>
      <button
        type="button"
        className={styles.treeNode}
        style={{ paddingLeft: indent }}
        data-selected={isSelected || undefined}
        data-dir={entry.is_dir || undefined}
        onClick={handleClick}
        aria-expanded={entry.is_dir ? isExpanded : undefined}
        aria-label={entry.name}
      >
        {entry.is_dir ? (
          <span
            className={`${styles.chevron} ${isExpanded ? styles.chevronExpanded : ""}`}
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
        ) : (
          <span className={styles.fileIconSpacer} />
        )}
        <span className={styles.icon}>
          {entry.is_dir ? (
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
        <span className={styles.fileName}>
          {highlightMatch(entry.name, filterText)}
        </span>
      </button>
      {entry.is_dir &&
        isExpanded &&
        children &&
        children
          .filter((child) => shouldShow(child, fullPath, filterText, treeData))
          .map((child) => (
            <TreeNode
              key={buildPath(fullPath, child.name)}
              entry={child}
              fullPath={buildPath(fullPath, child.name)}
              depth={depth + 1}
              treeData={treeData}
              expanded={expanded}
              selectedPath={selectedPath}
              filterText={filterText}
              onToggle={onToggle}
              onSelectFile={onSelectFile}
            />
          ))}
    </>
  );
}

export function FileTree({
  treeData,
  expanded,
  selectedPath,
  filterText,
  onToggle,
  onSelectFile,
}: FileTreeProps) {
  const rootEntries = treeData.get("") ?? [];

  return (
    <div className={styles.tree} role="tree" aria-label="File tree">
      {rootEntries
        .filter((entry) => shouldShow(entry, "", filterText, treeData))
        .map((entry) => (
          <TreeNode
            key={entry.name}
            entry={entry}
            fullPath={entry.name}
            depth={0}
            treeData={treeData}
            expanded={expanded}
            selectedPath={selectedPath}
            filterText={filterText}
            onToggle={onToggle}
            onSelectFile={onSelectFile}
          />
        ))}
      {rootEntries.length === 0 && !filterText && (
        <div className={styles.empty}>No files found</div>
      )}
      {rootEntries.length > 0 &&
        filterText &&
        rootEntries.filter((e) => shouldShow(e, "", filterText, treeData))
          .length === 0 && (
          <div className={styles.empty}>
            No matches for &ldquo;{filterText}&rdquo;
          </div>
        )}
    </div>
  );
}
