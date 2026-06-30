import {
  useState,
  useCallback,
  useEffect,
  useRef,
  type CSSProperties,
  type FormEvent,
} from "react";
import { ErrorDisplay, LoadingSkeleton } from "@/components";
import { ResizeHandle } from "@/components/ResizeHandle";
import { useWorkspaceContext } from "@/hooks";
import { useWorkspaceFileTree, useWorkspaceFileContent } from "@/hooks/common";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { FileTree } from "./FileTree";
import { FileTabBar } from "./FileTabBar";
import { WorkspaceFilePane } from "./WorkspaceFilePane";
import styles from "./FileExplorer.module.css";

const TABS_STORAGE_KEY = "file-browser-tabs";

/** Load persisted open tabs for a workspace, dropping anything malformed. */
function loadTabs(wsId: string): {
  openTabs: string[];
  activePath: string | null;
} {
  try {
    const raw = wsGet(wsId, TABS_STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as {
        openTabs?: unknown;
        activePath?: unknown;
      };
      if (Array.isArray(parsed.openTabs)) {
        const openTabs = parsed.openTabs.filter(
          (p): p is string => typeof p === "string",
        );
        const activePath =
          typeof parsed.activePath === "string" &&
          openTabs.includes(parsed.activePath)
            ? parsed.activePath
            : (openTabs[0] ?? null);
        return { openTabs, activePath };
      }
    }
  } catch {
    // malformed or unavailable storage — start fresh
  }
  return { openTabs: [], activePath: null };
}

function saveTabs(
  wsId: string,
  openTabs: string[],
  activePath: string | null,
): void {
  wsSet(wsId, TABS_STORAGE_KEY, JSON.stringify({ openTabs, activePath }));
}

const TREE_WIDTH_KEY = "loom:file-browser:tree-width";
const DEFAULT_TREE_WIDTH = 320;
const MIN_TREE_WIDTH = 240;
const MAX_TREE_WIDTH = 400;

function clampTreeWidth(w: number): number {
  return Math.min(MAX_TREE_WIDTH, Math.max(MIN_TREE_WIDTH, w));
}

function getStoredTreeWidth(): number {
  try {
    const raw = localStorage.getItem(TREE_WIDTH_KEY);
    if (raw !== null) {
      const n = Number(raw);
      if (Number.isFinite(n) && n > 0) return clampTreeWidth(n);
    }
  } catch {
    // localStorage unavailable
  }
  return DEFAULT_TREE_WIDTH;
}

function storeTreeWidth(w: number): void {
  try {
    localStorage.setItem(TREE_WIDTH_KEY, String(w));
  } catch {
    // localStorage unavailable
  }
}

/**
 * WorkspaceFileBrowser is the read-only file browser rooted at the workspace
 * folder. Layout: a resizable tree on the left and a tabbed, docked viewer on
 * the right. Opening a file from the tree adds (or activates) a tab; the active
 * tab drives the viewer.
 */
export function WorkspaceFileBrowser() {
  const {
    expanded,
    treeData,
    isLoading,
    error,
    filterText,
    debouncedFilterText,
    toggle,
    revealPath,
    setFilterText,
  } = useWorkspaceFileTree();

  const {
    fileData,
    isLoading: isFileLoading,
    error: fileError,
    fetchFile,
    clearFile,
  } = useWorkspaceFileContent();

  // Workspace-scoped tab persistence. FilesPage keys this component by
  // workspaceId, so workspaceId is stable for the component's lifetime and the
  // lazy initializers restore the right workspace's tabs on mount/reload.
  const { workspaceId } = useWorkspaceContext();
  const [openTabs, setOpenTabs] = useState<string[]>(
    () => loadTabs(workspaceId).openTabs,
  );
  const [activePath, setActivePath] = useState<string | null>(
    () => loadTabs(workspaceId).activePath,
  );
  const activePathRef = useRef<string | null>(null);
  activePathRef.current = activePath;

  const [treeWidth, setTreeWidth] = useState<number>(getStoredTreeWidth);
  const [jumpText, setJumpText] = useState<string>("");
  const [scrollTarget, setScrollTarget] = useState<string | null>(null);

  // Fetch the active tab's content (latest-wins is handled inside the hook).
  useEffect(() => {
    if (activePath) {
      fetchFile(activePath);
    } else {
      clearFile();
    }
  }, [activePath, fetchFile, clearFile]);

  // Persist open tabs per workspace so they survive a reload.
  useEffect(() => {
    saveTabs(workspaceId, openTabs, activePath);
  }, [workspaceId, openTabs, activePath]);

  // On mount, reveal the restored active file in the tree (expand its ancestors
  // and scroll to it). Runs once — tab switches don't auto-reveal.
  useEffect(() => {
    const initial = activePathRef.current;
    if (initial) {
      void revealPath(initial).then(() => setScrollTarget(initial));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const openFile = useCallback((path: string) => {
    setOpenTabs((prev) => (prev.includes(path) ? prev : [...prev, path]));
    setActivePath(path);
  }, []);

  const closeTab = useCallback((path: string) => {
    setOpenTabs((prev) => {
      const idx = prev.indexOf(path);
      if (idx === -1) return prev;
      const next = prev.filter((p) => p !== path);
      // If we closed the active tab, activate a neighbor (next, else previous).
      if (path === activePathRef.current) {
        setActivePath(next[idx] ?? next[idx - 1] ?? null);
      }
      return next;
    });
  }, []);

  const handleResizeDelta = useCallback((deltaPx: number) => {
    setTreeWidth((w) => {
      const next = clampTreeWidth(w + deltaPx);
      storeTreeWidth(next);
      return next;
    });
  }, []);

  const handleResizeReset = useCallback(() => {
    setTreeWidth(DEFAULT_TREE_WIDTH);
    storeTreeWidth(DEFAULT_TREE_WIDTH);
  }, []);

  const handleJump = useCallback(
    (e: FormEvent) => {
      e.preventDefault();
      const target = jumpText.trim();
      if (!target) return;
      // Reveal, then scroll/highlight the target once it's loaded into the tree.
      void revealPath(target).then(() => setScrollTarget(target));
      setJumpText("");
    },
    [jumpText, revealPath],
  );

  return (
    <div className={styles.container}>
      <div
        className={styles.treePanel}
        style={{ ["--tree-width"]: `${treeWidth}px` } as CSSProperties}
      >
        <div className={styles.toolbar}>
          <form onSubmit={handleJump} style={{ display: "contents" }}>
            <input
              className={styles.filterInput}
              type="text"
              value={jumpText}
              onChange={(e) => setJumpText(e.target.value)}
              placeholder="Jump to folder… (e.g. src/api)"
              aria-label="Jump to folder"
            />
          </form>
          <input
            className={styles.filterInput}
            type="text"
            value={filterText}
            onChange={(e) => setFilterText(e.target.value)}
            placeholder="Filter files..."
            aria-label="Filter files"
          />
        </div>
        {isLoading ? (
          <div className={styles.treeScroll}>
            {Array.from({ length: 6 }, (_, i) => (
              <div
                key={i}
                style={{
                  padding: "4px 8px",
                  paddingLeft: `${(i % 3) * 16 + 8}px`,
                }}
              >
                <LoadingSkeleton
                  shape="text"
                  width={100 + (i % 4) * 20}
                  height={12}
                />
              </div>
            ))}
          </div>
        ) : error ? (
          <div className={styles.treeScroll}>
            <ErrorDisplay
              variant="fetch-error"
              title="Failed to load files"
              error={new Error(error)}
              showDetails
            />
          </div>
        ) : (
          <div className={styles.treeScroll}>
            <FileTree
              treeData={treeData}
              expanded={expanded}
              selectedPath={activePath}
              filterText={debouncedFilterText}
              onToggle={toggle}
              onSelectFile={(p) => {
                if (p) openFile(p);
              }}
              scrollToPath={scrollTarget}
            />
          </div>
        )}
      </div>
      <ResizeHandle
        width={treeWidth}
        minWidth={MIN_TREE_WIDTH}
        maxWidth={MAX_TREE_WIDTH}
        edge="right"
        onDelta={handleResizeDelta}
        onReset={handleResizeReset}
        ariaLabel="Resize file tree"
        testId="file-tree-resize-handle"
        className={styles.resizeHandle}
      />
      <div className={styles.mainColumn}>
        <FileTabBar
          tabs={openTabs}
          activePath={activePath}
          onSelect={setActivePath}
          onClose={closeTab}
        />
        <WorkspaceFilePane
          path={activePath}
          fileData={fileData}
          isLoading={isFileLoading}
          error={fileError}
          onNavigate={(dir) => void revealPath(dir)}
        />
      </div>
    </div>
  );
}
