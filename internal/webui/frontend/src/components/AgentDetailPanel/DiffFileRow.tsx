/**
 * DiffFileRow - single row in the diff file list.
 * Shows status badge, file path, +/- stats, viewed control, expand chevron.
 */

import type { DiffFile } from "@/api/issues";

import styles from "./DiffTab.module.css";

interface DiffFileRowProps {
  file: DiffFile;
  isExpanded: boolean;
  isViewed: boolean;
  onToggleExpand: () => void;
  onToggleViewed: () => void;
}

export function DiffFileRow({
  file,
  isExpanded,
  isViewed,
  onToggleExpand,
  onToggleViewed,
}: DiffFileRowProps): JSX.Element {
  const displayPath =
    file.status === "R" && file.old_path
      ? `${file.old_path} → ${file.path}`
      : file.path;

  return (
    <div
      className={styles.fileRow}
      onClick={onToggleExpand}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onToggleExpand();
        }
      }}
    >
      <span className={styles.statusBadge} data-status={file.status}>
        {file.status}
      </span>
      <span className={styles.filePath} title={displayPath}>
        {displayPath}
      </span>
      <span className={styles.fileRowStats}>
        {file.additions > 0 && (
          <span className={styles.rowStatAdd}>+{file.additions}</span>
        )}
        {file.deletions > 0 && (
          <span className={styles.rowStatDel}>-{file.deletions}</span>
        )}
      </span>
      <button
        type="button"
        className={styles.viewedToggle}
        data-viewed={isViewed || undefined}
        aria-pressed={isViewed}
        aria-label={`${isViewed ? "Unmark" : "Mark"} ${file.path} as viewed`}
        title={isViewed ? "Viewed" : "Mark viewed"}
        onClick={(e) => {
          e.stopPropagation();
          onToggleViewed();
        }}
      >
        Viewed
      </button>
      <svg
        className={styles.expandChevron}
        data-expanded={isExpanded}
        viewBox="0 0 16 16"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      >
        <path d="M6 4l4 4-4 4" />
      </svg>
    </div>
  );
}
