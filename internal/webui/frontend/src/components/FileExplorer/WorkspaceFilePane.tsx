import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  lazy,
  Suspense,
} from "react";
import type { FileReadData } from "@/api/workspace";
import type { FileBlameLine } from "@/api/workspace";
import { detectLanguage } from "@/utils/detectLanguage";
import type { FileSymbol, FileSymbolState } from "@/utils/lezerSymbols";
import type { GitGutterLineMark } from "./gitGutter";
import { SymbolPalette } from "./SymbolPalette";
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
  isActive: boolean;
  isLoading: boolean;
  error: string | null;
  content: string;
  isDirty: boolean;
  isSaving: boolean;
  searchOpen: boolean;
  onContentChange: (value: string) => void;
  onSave: () => void;
  historyOpen: boolean;
  onToggleHistory: () => void;
  onToggleSearch: () => void;
  onSplitRight: () => void;
  /** Reveal a folder in the tree when its breadcrumb segment is clicked. */
  onNavigate?: ((dirPath: string) => void) | undefined;
  lineTarget?: { line: number; token: number } | undefined;
  onLineTargetApplied?: ((path: string, token: number) => void) | undefined;
  gitGutterMarks?: GitGutterLineMark[] | undefined;
  blameEnabled?: boolean | undefined;
  blameLines?: FileBlameLine[] | undefined;
  blameLoading?: boolean | undefined;
  blameSkippedMessage?: string | undefined;
  onToggleBlame?: (() => void) | undefined;
  onOpenBlameCommit?: ((sha: string) => void) | undefined;
}

/** Clickable path breadcrumb; folder segments reveal that folder in the tree. */
function Breadcrumb({
  path,
  symbolTrail,
  onNavigate,
  onSymbolNavigate,
}: {
  path: string;
  symbolTrail: FileSymbol[];
  onNavigate?: ((dirPath: string) => void) | undefined;
  onSymbolNavigate?: ((symbol: FileSymbol) => void) | undefined;
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
      {symbolTrail.map((symbol) => (
        <span
          key={`${symbol.name}:${symbol.line}:${symbol.kind}`}
          className={styles.crumb}
        >
          <span className={styles.crumbSep} aria-hidden="true">
            &gt;
          </span>
          <button
            type="button"
            className={styles.crumbLink}
            onClick={() => onSymbolNavigate?.(symbol)}
            title={`Go to ${symbol.name}`}
          >
            {symbol.name}
          </button>
        </span>
      ))}
    </nav>
  );
}

const emptySymbolState: FileSymbolState = { symbols: [], trail: [] };

/**
 * WorkspaceFilePane is the docked, always-visible viewer column of the
 * workspace file browser. Unlike FileViewer (a modal drawer used by the agent
 * files tab), this is a persistent right-hand pane in a split layout.
 */
export function WorkspaceFilePane({
  path,
  fileData,
  isActive,
  isLoading,
  error,
  content,
  isDirty,
  isSaving,
  searchOpen,
  onContentChange,
  onSave,
  historyOpen,
  onToggleHistory,
  onToggleSearch,
  onSplitRight,
  onNavigate,
  lineTarget,
  onLineTargetApplied,
  gitGutterMarks,
  blameEnabled,
  blameLines,
  blameLoading,
  blameSkippedMessage,
  onToggleBlame,
  onOpenBlameCommit,
}: WorkspaceFilePaneProps) {
  const language = useMemo(
    () => (path ? detectLanguage(path) : undefined),
    [path],
  );
  const [symbolState, setSymbolState] =
    useState<FileSymbolState>(emptySymbolState);
  const [symbolPaletteOpen, setSymbolPaletteOpen] = useState(false);
  const [symbolLineTarget, setSymbolLineTarget] = useState<
    { line: number; token: number } | undefined
  >(undefined);
  const isReadOnly = isSaving || !!fileData?.truncated || !!fileData?.binary;
  const requestedLineTarget = lineTarget ?? symbolLineTarget;
  const usingLocalSymbolTarget = !lineTarget && !!symbolLineTarget;
  const editorLineTarget =
    requestedLineTarget &&
    path &&
    !isLoading &&
    fileData &&
    fileData.path === path &&
    !fileData.binary &&
    (usingLocalSymbolTarget || content === (fileData.content ?? ""))
      ? requestedLineTarget
      : undefined;
  const handleLineTargetApplied = useCallback(() => {
    if (path && editorLineTarget) {
      if (usingLocalSymbolTarget) {
        setSymbolLineTarget((current) =>
          current?.token === editorLineTarget.token ? undefined : current,
        );
      } else {
        onLineTargetApplied?.(path, editorLineTarget.token);
      }
    }
  }, [editorLineTarget, onLineTargetApplied, path, usingLocalSymbolTarget]);
  const jumpToSymbol = useCallback((symbol: FileSymbol) => {
    setSymbolLineTarget((prev) => ({
      line: symbol.line,
      token: (prev?.token ?? 0) + 1,
    }));
  }, []);
  const handleSymbolsChange = useCallback(
    (nextSymbolState: FileSymbolState) => {
      setSymbolState(nextSymbolState);
    },
    [],
  );

  useEffect(() => {
    setSymbolState(emptySymbolState);
    setSymbolLineTarget(undefined);
    setSymbolPaletteOpen(false);
  }, [path, language]);

  useEffect(() => {
    const handleKeyDown = (event: KeyboardEvent) => {
      const key = event.key.toLowerCase();
      const mod = event.metaKey || event.ctrlKey;
      if (!mod || !event.shiftKey || key !== "o") return;
      if (!isActive) return;
      if (!path || symbolState.symbols.length === 0 || fileData?.binary) return;
      event.preventDefault();
      setSymbolPaletteOpen(true);
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [fileData?.binary, isActive, path, symbolState.symbols.length]);

  return (
    <div className={styles.viewerColumn}>
      {path && (
        <div className={styles.viewerHeader}>
          <Breadcrumb
            path={path}
            symbolTrail={symbolState.trail}
            onNavigate={onNavigate}
            onSymbolNavigate={jumpToSymbol}
          />
          <div className={styles.viewerActions}>
            {isDirty && <span className={styles.dirtyLabel}>Modified</span>}
            <button
              type="button"
              className={styles.iconButton}
              aria-label="Toggle blame"
              title="Toggle blame"
              aria-pressed={!!blameEnabled}
              onClick={onToggleBlame}
              disabled={!fileData || fileData.binary || !!fileData.truncated}
            >
              <svg viewBox="0 0 16 16" aria-hidden="true">
                <path
                  d="M4 3h8M4 8h5M4 13h8"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.4"
                  strokeLinecap="round"
                />
                <circle cx="12" cy="8" r="1.5" fill="currentColor" />
              </svg>
            </button>
            <button
              type="button"
              className={styles.iconButton}
              aria-label="Go to symbol in file"
              title="Go to symbol in file"
              onClick={() => setSymbolPaletteOpen(true)}
              disabled={symbolState.symbols.length === 0 || !!fileData?.binary}
            >
              <svg viewBox="0 0 16 16" aria-hidden="true">
                <path
                  d="M4 3h8M4 8h8M4 13h8"
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
        {path && blameEnabled && blameLoading && (
          <div className={styles.limitBanner} role="status">
            Loading blame...
          </div>
        )}
        {path && blameEnabled && blameSkippedMessage && (
          <div className={styles.limitBanner} role="status">
            {blameSkippedMessage}
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
              scrollToLine={editorLineTarget?.line}
              scrollToLineKey={editorLineTarget?.token}
              onScrollToLineApplied={handleLineTargetApplied}
              onSymbolsChange={handleSymbolsChange}
              gitGutterMarks={gitGutterMarks}
              blameEnabled={blameEnabled}
              blameLines={blameLines}
              onBlameCommitClick={onOpenBlameCommit}
            />
          </Suspense>
        )}
      </div>
      <SymbolPalette
        isOpen={symbolPaletteOpen}
        symbols={symbolState.symbols}
        onClose={() => setSymbolPaletteOpen(false)}
        onOpen={jumpToSymbol}
      />
    </div>
  );
}
