import { useMemo, useEffect, useRef, lazy, Suspense } from "react";
import type { FileReadData } from "@/api/files";
import styles from "./FileExplorer.module.css";

const CodeMirrorEditor = lazy(() =>
  import("@/components/CodeMirrorEditor").then((m) => ({
    default: m.CodeMirrorEditor,
  })),
);

interface FileViewerProps {
  isOpen: boolean;
  path: string | null;
  fileData: FileReadData | null;
  isLoading: boolean;
  error: string | null;
  onClose: () => void;
}

function detectLanguage(path: string): string | undefined {
  const ext = path.split(".").pop()?.toLowerCase();
  switch (ext) {
    case "go":
      return "go";
    case "json":
      return "json";
    case "yaml":
    case "yml":
      return "yaml";
    case "md":
    case "markdown":
      return "markdown";
    default:
      return undefined;
  }
}

export function FileViewer({
  isOpen,
  path,
  fileData,
  isLoading,
  error,
  onClose,
}: FileViewerProps) {
  const language = useMemo(
    () => (path ? detectLanguage(path) : undefined),
    [path],
  );
  const fileName = useMemo(
    () => (path ? (path.split("/").pop() ?? path) : ""),
    [path],
  );

  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (isOpen) panelRef.current?.focus();
  }, [isOpen]);

  const overlayClassName = [styles.overlay, isOpen ? styles.overlayOpen : ""]
    .filter(Boolean)
    .join(" ");

  return (
    <div className={overlayClassName} onClick={onClose} aria-hidden={!isOpen}>
      <div
        ref={panelRef}
        className={styles.viewerPanel}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={`File: ${fileName}`}
        tabIndex={-1}
      >
        <div className={styles.viewerHeader}>
          <span className={styles.viewerPath} title={path ?? ""}>
            {path}
          </span>
          <button
            type="button"
            className={styles.closeButton}
            onClick={onClose}
            aria-label="Close file viewer"
          >
            <svg viewBox="0 0 16 16" aria-hidden="true">
              <path
                d="M4 4l8 8M12 4l-8 8"
                stroke="currentColor"
                strokeWidth="1.5"
              />
            </svg>
          </button>
        </div>
        <div className={styles.viewerContent}>
          {isLoading && <div className={styles.loading}>Loading file...</div>}
          {error && <div className={styles.error}>{error}</div>}
          {fileData && fileData.binary && (
            <div className={styles.binaryNotice}>
              Binary file — cannot display
            </div>
          )}
          {fileData && !fileData.binary && (
            <Suspense
              fallback={<div className={styles.loading}>Loading editor...</div>}
            >
              <CodeMirrorEditor
                value={fileData.content ?? ""}
                language={language}
                readOnly={true}
              />
            </Suspense>
          )}
        </div>
      </div>
    </div>
  );
}
