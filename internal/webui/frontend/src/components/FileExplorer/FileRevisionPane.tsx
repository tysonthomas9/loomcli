import { lazy, Suspense, useEffect, useMemo, useState } from "react";

import type { FileReadData } from "@/api/workspace";
import { readScopedFile } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks";
import { detectLanguage } from "@/utils/detectLanguage";
import type { CheckoutRef } from "@/utils/fileExplorerRefs";

import styles from "./FileExplorer.module.css";

const CodeMirrorEditor = lazy(() =>
  import("@/components/CodeMirrorEditor").then((m) => ({
    default: m.CodeMirrorEditor,
  })),
);

export interface RevisionViewState {
  ref: CheckoutRef;
  path: string;
  rev: string;
  title: string;
}

export function FileRevisionPane({
  revisionView,
  historyOpen,
  onToggleHistory,
  onClose,
}: {
  revisionView: RevisionViewState;
  historyOpen: boolean;
  onToggleHistory: () => void;
  onClose: () => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const [fileData, setFileData] = useState<FileReadData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const language = useMemo(
    () => detectLanguage(revisionView.path),
    [revisionView.path],
  );

  useEffect(() => {
    let canceled = false;
    setIsLoading(true);
    setError(null);
    setFileData(null);
    readScopedFile(
      workspaceId,
      revisionView.ref,
      revisionView.path,
      revisionView.rev,
    )
      .then((data) => {
        if (!canceled) setFileData(data);
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
  }, [revisionView, workspaceId]);

  return (
    <div className={styles.viewerColumn}>
      <div className={styles.viewerHeader}>
        <div className={styles.diffTitle}>
          <span className={styles.diffTitlePath}>{revisionView.path}</span>
          <span className={styles.diffTitleMeta}>{revisionView.title}</span>
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
          <button
            type="button"
            className={styles.iconButton}
            aria-label="Close revision"
            title="Close revision"
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
      <div className={styles.viewerContent}>
        {isLoading && <div className={styles.loading}>Loading file...</div>}
        {error && <div className={styles.error}>{error}</div>}
        {fileData?.binary && (
          <div className={styles.binaryNotice}>
            Binary file — cannot display
          </div>
        )}
        {fileData && !fileData.binary && fileData.truncated && (
          <div className={styles.truncatedBanner} role="status">
            File is larger than the editable limit. Showing a read-only preview.
          </div>
        )}
        {fileData && !fileData.binary && (
          <Suspense
            fallback={<div className={styles.loading}>Loading editor...</div>}
          >
            <CodeMirrorEditor
              value={fileData.content ?? ""}
              language={language}
              readOnly
            />
          </Suspense>
        )}
      </div>
    </div>
  );
}
