import { useMemo, lazy, Suspense } from "react";
import type { FileReadData } from "@/api/workspace";
import { detectLanguage } from "@/utils/detectLanguage";
import styles from "./FileExplorer.module.css";

const CodeMirrorEditor = lazy(() =>
  import("@/components/CodeMirrorEditor").then((m) => ({
    default: m.CodeMirrorEditor,
  })),
);

interface WorkspaceFilePaneProps {
  /** Selected file path, or null when nothing is selected. */
  path: string | null;
  fileData: FileReadData | null;
  isLoading: boolean;
  error: string | null;
  content: string;
  isDirty: boolean;
  isSaving: boolean;
  searchOpen: boolean;
  onContentChange: (value: string) => void;
  onSave: () => void;
  onToggleSearch: () => void;
  onSplitRight: () => void;
  /** Reveal a folder in the tree when its breadcrumb segment is clicked. */
  onNavigate?: ((dirPath: string) => void) | undefined;
}

/** Clickable path breadcrumb; folder segments reveal that folder in the tree. */
function Breadcrumb({
  path,
  onNavigate,
}: {
  path: string;
  onNavigate?: ((dirPath: string) => void) | undefined;
}) {
  const segments = path.split("/").filter(Boolean);
  return (
    <nav className={styles.breadcrumb} aria-label="File path">
      {segments.map((seg, i) => {
        const isLast = i === segments.length - 1;
        const dirPath = segments.slice(0, i + 1).join("/");
        return (
          <span key={dirPath} className={styles.crumb}>
            {i > 0 && (
              <span className={styles.crumbSep} aria-hidden="true">
                /
              </span>
            )}
            {isLast ? (
              <span className={styles.crumbCurrent}>{seg}</span>
            ) : (
              <button
                type="button"
                className={styles.crumbLink}
                onClick={() => onNavigate?.(dirPath)}
                title={`Reveal ${dirPath}`}
              >
                {seg}
              </button>
            )}
          </span>
        );
      })}
    </nav>
  );
}

/**
 * WorkspaceFilePane is the docked, always-visible viewer column of the
 * workspace file browser. Unlike FileViewer (a modal drawer used by the agent
 * files tab), this is a persistent right-hand pane in a split layout.
 */
export function WorkspaceFilePane({
  path,
  fileData,
  isLoading,
  error,
  content,
  isDirty,
  isSaving,
  searchOpen,
  onContentChange,
  onSave,
  onToggleSearch,
  onSplitRight,
  onNavigate,
}: WorkspaceFilePaneProps) {
  const language = useMemo(
    () => (path ? detectLanguage(path) : undefined),
    [path],
  );
  const isReadOnly = isSaving || !!fileData?.truncated || !!fileData?.binary;

  return (
    <div className={styles.viewerColumn}>
      {path && (
        <div className={styles.viewerHeader}>
          <Breadcrumb path={path} onNavigate={onNavigate} />
          <div className={styles.viewerActions}>
            {isDirty && <span className={styles.dirtyLabel}>Modified</span>}
            <button
              type="button"
              className={styles.iconButton}
              aria-label="Find in file"
              title="Find in file"
              onClick={onToggleSearch}
              disabled={!fileData || fileData.binary}
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
                  d="M10.2 10.2L14 14"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                />
              </svg>
            </button>
            <button
              type="button"
              className={styles.iconButton}
              aria-label="Split right"
              title="Split right"
              onClick={onSplitRight}
              disabled={!path}
            >
              <svg viewBox="0 0 16 16" aria-hidden="true">
                <rect
                  x="2"
                  y="3"
                  width="12"
                  height="10"
                  rx="1"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.2"
                />
                <path
                  d="M8 3v10"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.2"
                />
              </svg>
            </button>
            <button
              type="button"
              className={styles.saveButton}
              onClick={onSave}
              disabled={!isDirty || isReadOnly}
            >
              {isSaving ? "Saving..." : "Save"}
            </button>
          </div>
        </div>
      )}
      <div className={styles.viewerContent}>
        {!path && (
          <div className={styles.empty}>
            Select a file to view its contents.
          </div>
        )}
        {path && isLoading && (
          <div className={styles.loading}>Loading file...</div>
        )}
        {path && error && <div className={styles.error}>{error}</div>}
        {path && fileData && fileData.binary && (
          <div className={styles.binaryNotice}>
            Binary file — cannot display
          </div>
        )}
        {path && fileData && !fileData.binary && fileData.truncated && (
          <div className={styles.truncatedBanner} role="status">
            File is larger than the editable limit. Showing a read-only preview.
          </div>
        )}
        {path && fileData && !fileData.binary && (
          <Suspense
            fallback={<div className={styles.loading}>Loading editor...</div>}
          >
            <CodeMirrorEditor
              value={content}
              onChange={onContentChange}
              language={language}
              readOnly={isReadOnly}
              searchOpen={searchOpen}
            />
          </Suspense>
        )}
      </div>
    </div>
  );
}
