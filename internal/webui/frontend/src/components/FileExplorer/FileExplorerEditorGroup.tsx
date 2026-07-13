import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { DiffFileViewer } from "@/components/AgentDetailPanel";
import type { FileBlameData } from "@/api/workspace";
import {
  blameScopedFile,
  diffScopedFile,
  readScopedFile,
  writeScopedFile,
} from "@/hooks/api";
import {
  useFileBrowserStoreInstance,
  useScopedFileContent,
  useWorkspaceContext,
  type FileBrowserGroup,
  type FileBrowserTab,
} from "@/hooks";
import { useFileEditorBuffer } from "@/hooks/common/useFileEditorBuffer";
import {
  checkoutRefKey,
  tabIdentityKey,
  type CheckoutRef,
} from "@/utils/fileExplorerRefs";

import { FileHistoryPanel, type HistorySubject } from "./FileHistoryPanel";
import { FileRevisionPane } from "./FileRevisionPane";
import { FileTabBar } from "./FileTabBar";
import { computeGitGutterLineMarks, type GitGutterLineMark } from "./gitGutter";
import { WorkspaceFilePane } from "./WorkspaceFilePane";
import styles from "./FileExplorer.module.css";
import type {
  DiffViewState,
  FileReloadRequest,
  LineTarget,
  OpenDiffRequest,
  OpenRevisionRequest,
  RevisionViewState,
} from "./workspaceFileBrowserTypes";

function DiffEditorPane({
  diffView,
  historyOpen,
  onToggleHistory,
  onClose,
  onOpenFile,
  onRestore,
}: {
  diffView: DiffViewState;
  historyOpen: boolean;
  onToggleHistory: () => void;
  onClose: () => void;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
  onRestore:
    | ((ref: CheckoutRef, path: string, content: string) => Promise<void>)
    | undefined;
}) {
  const { workspaceId } = useWorkspaceContext();
  const [patch, setPatch] = useState<string | null>(diffView.patch ?? null);
  const [isLoading, setIsLoading] = useState(!diffView.patch);
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    let canceled = false;
    setPatch(diffView.patch ?? null);
    setError(undefined);
    if (diffView.patch !== undefined) {
      setIsLoading(false);
      return () => {
        canceled = true;
      };
    }
    setIsLoading(true);
    diffScopedFile(
      workspaceId,
      diffView.ref,
      diffView.path,
      diffView.from,
      diffView.to,
    )
      .then((res) => {
        if (!canceled) setPatch(res.patch);
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
  }, [diffView, workspaceId]);

  return (
    <div className={styles.viewerColumn}>
      <div className={styles.viewerHeader}>
        <div className={styles.diffTitle}>
          <span className={styles.diffTitlePath}>{diffView.path}</span>
          <span className={styles.diffTitleMeta}>{diffView.title}</span>
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
          {diffView.canOpenFile && (
            <button
              type="button"
              className={styles.saveButton}
              onClick={() => onOpenFile(diffView.ref, diffView.path)}
            >
              Open file
            </button>
          )}
          {diffView.restoreContent !== undefined && onRestore && (
            <button
              type="button"
              className={styles.saveButton}
              onClick={() =>
                void onRestore(
                  diffView.ref,
                  diffView.path,
                  diffView.restoreContent ?? "",
                )
              }
            >
              Restore
            </button>
          )}
          <button
            type="button"
            className={styles.iconButton}
            aria-label="Close diff"
            title="Close diff"
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
      <div className={styles.diffEditorBody}>
        <DiffFileViewer
          patch={
            patch === null
              ? null
              : {
                  patch,
                  is_binary: false,
                  is_too_large: false,
                  additions: 0,
                  deletions: 0,
                }
          }
          isLoading={isLoading}
          error={error}
        />
      </div>
    </div>
  );
}

export function FileExplorerEditorGroup({
  groupIndex,
  group,
  diffView,
  revisionView,
  isActiveGroup,
  dirty,
  onSelectTab,
  onCloseTab,
  onSplitRight,
  onNavigate,
  onSaved,
  onOpenDiff,
  onCloseDiff,
  onOpenRevision,
  onCloseRevision,
  onOpenEditableFile,
  onRestoreSnapshot,
  historyRefreshKey,
  reloadToken,
  lineTarget,
  onLineTargetApplied,
}: {
  groupIndex: number;
  group: FileBrowserGroup;
  diffView: DiffViewState | null;
  revisionView: RevisionViewState | null;
  isActiveGroup: boolean;
  dirty: Record<string, boolean>;
  onSelectTab: (groupIndex: number, tabKey: string) => void;
  onCloseTab: (groupIndex: number, tabKey: string) => void;
  onSplitRight: (groupIndex: number) => void;
  onNavigate: (ref: CheckoutRef, dirPath: string) => void;
  onSaved: (tab: FileBrowserTab) => void;
  onOpenDiff: (groupIndex: number, request: OpenDiffRequest) => void;
  onCloseDiff: (groupIndex: number) => void;
  onOpenRevision: (groupIndex: number, request: OpenRevisionRequest) => void;
  onCloseRevision: (groupIndex: number) => void;
  onOpenEditableFile: (
    groupIndex: number,
    ref: CheckoutRef,
    path: string,
  ) => void;
  onRestoreSnapshot: (
    ref: CheckoutRef,
    path: string,
    content: string,
  ) => Promise<void>;
  historyRefreshKey: number;
  reloadToken?: number | undefined;
  lineTarget?: LineTarget | undefined;
  onLineTargetApplied: (tabKey: string, token: number) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const store = useFileBrowserStoreInstance();
  const activeTab =
    group.tabs.find((tab) => tabIdentityKey(tab) === group.active) ?? null;
  const scopeRef = useMemo<CheckoutRef>(
    () => activeTab?.ref ?? { scope: "workspace" },
    [activeTab],
  );
  const { fileData, isLoading, error, fetchFile, clearFile } =
    useScopedFileContent(scopeRef);
  const fetchFileRef = useRef(fetchFile);
  const clearFileRef = useRef(clearFile);
  fetchFileRef.current = fetchFile;
  clearFileRef.current = clearFile;
  const activePath = activeTab?.path ?? null;
  const activeKey = activeTab ? tabIdentityKey(activeTab) : null;
  const scopeKey = checkoutRefKey(scopeRef);
  const pathDirty = activeKey ? !!dirty[activeKey] : false;
  const appliedReloadRef = useRef<FileReloadRequest>({
    key: null,
    token: undefined,
  });
  const [searchOpen, setSearchOpen] = useState(false);
  const [basePath, setBasePath] = useState<string | null>(null);
  const [baseContent, setBaseContent] = useState<string | null>(null);
  const [gitGutterMarks, setGitGutterMarks] = useState<GitGutterLineMark[]>([]);
  const [blameEnabled, setBlameEnabled] = useState(false);
  const [blameData, setBlameData] = useState<FileBlameData | null>(null);
  const [blameLoading, setBlameLoading] = useState(false);
  const [blameError, setBlameError] = useState<string | null>(null);
  const [historyOpen, setHistoryOpen] = useState(false);
  const historySubject = useMemo<HistorySubject | null>(() => {
    if (diffView || revisionView || !activeTab || !activePath) return null;
    return { ref: scopeRef, path: activePath };
  }, [activePath, activeTab, diffView, revisionView, scopeRef]);
  const toggleHistory = useCallback(() => setHistoryOpen((open) => !open), []);
  const closeHistory = useCallback(() => setHistoryOpen(false), []);
  const renderHistoryPanel = () =>
    historyOpen ? (
      <FileHistoryPanel
        subject={historySubject}
        refreshKey={historyRefreshKey}
        onClose={closeHistory}
        onOpenDiff={(request) => onOpenDiff(groupIndex, request)}
        onOpenRevision={(request) => onOpenRevision(groupIndex, request)}
        onRestoreSnapshot={onRestoreSnapshot}
      />
    ) : null;

  useEffect(() => {
    setSearchOpen(false);
    const hasReloadRequest =
      activeKey !== null &&
      reloadToken !== undefined &&
      (appliedReloadRef.current.key !== activeKey ||
        appliedReloadRef.current.token !== reloadToken);
    if (hasReloadRequest) {
      appliedReloadRef.current = { key: activeKey, token: reloadToken };
    }
    if (activePath) {
      if (!pathDirty || hasReloadRequest) {
        void fetchFileRef.current(activePath);
      }
    } else {
      appliedReloadRef.current = { key: null, token: undefined };
      clearFileRef.current();
    }
  }, [activeKey, activePath, pathDirty, reloadToken, scopeKey]);

  const writeFile = useCallback(
    async (path: string, content: string) => {
      await writeScopedFile(workspaceId, scopeRef, path, content);
    },
    [workspaceId, scopeRef],
  );
  const setDirty = useCallback(
    (_path: string, isDirty: boolean) => {
      if (activeKey) store.getState().setDirty(activeKey, isDirty);
    },
    [activeKey, store],
  );

  const canSave =
    !!activePath && !!fileData && !fileData.binary && !fileData.truncated;

  const loadBaseContent = useCallback(
    async (path: string) => {
      try {
        const data = await readScopedFile(workspaceId, scopeRef, path, "HEAD");
        if (!data.binary && !data.truncated) {
          setBasePath(path);
          setBaseContent(data.content ?? "");
        } else {
          setBasePath(null);
          setBaseContent(null);
        }
      } catch {
        setBasePath(null);
        setBaseContent(null);
      }
    },
    [scopeRef, workspaceId],
  );

  const editor = useFileEditorBuffer({
    path: activePath,
    fileData,
    isActive: isActiveGroup,
    canSave,
    writeFile,
    onDirtyChange: setDirty,
    onSaved: () => {
      if (activeTab && activePath) {
        onSaved(activeTab);
        void loadBaseContent(activePath);
      }
    },
  });

  useEffect(() => {
    setBasePath(null);
    setBaseContent(null);
    setGitGutterMarks([]);
    setBlameEnabled(false);
    setBlameData(null);
    setBlameError(null);
    if (activePath && canSave) {
      void loadBaseContent(activePath);
    }
  }, [activePath, canSave, loadBaseContent]);

  useEffect(() => {
    const handleFocus = () => {
      if (activePath && canSave) {
        void loadBaseContent(activePath);
      }
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [activePath, canSave, loadBaseContent]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!activePath || basePath !== activePath || baseContent === null) {
        setGitGutterMarks([]);
        return;
      }
      setGitGutterMarks(computeGitGutterLineMarks(baseContent, editor.content));
    }, 150);
    return () => window.clearTimeout(timer);
  }, [activePath, baseContent, basePath, editor.content]);

  useEffect(() => {
    let canceled = false;
    if (!activePath || !blameEnabled || !canSave) {
      setBlameData(null);
      setBlameLoading(false);
      setBlameError(null);
      return () => {
        canceled = true;
      };
    }
    setBlameLoading(true);
    setBlameError(null);
    blameScopedFile(workspaceId, scopeRef, activePath)
      .then((data) => {
        if (!canceled) setBlameData(data);
      })
      .catch((err) => {
        if (!canceled) {
          setBlameData(null);
          setBlameError(err instanceof Error ? err.message : String(err));
        }
      })
      .finally(() => {
        if (!canceled) setBlameLoading(false);
      });
    return () => {
      canceled = true;
    };
  }, [activePath, blameEnabled, canSave, scopeRef, workspaceId]);

  if (diffView) {
    return (
      <section
        className={styles.editorGroup}
        data-active={isActiveGroup || undefined}
      >
        <FileTabBar
          tabs={group.tabs}
          activeKey={group.active}
          dirtyPaths={dirty}
          groupLabel={`group ${groupIndex + 1}`}
          onSelect={(key) => onSelectTab(groupIndex, key)}
          onClose={(key) => onCloseTab(groupIndex, key)}
        />
        <div className={styles.editorGroupBody}>
          <div className={styles.editorPrimaryPane}>
            <DiffEditorPane
              diffView={diffView}
              historyOpen={historyOpen}
              onToggleHistory={toggleHistory}
              onClose={() => onCloseDiff(groupIndex)}
              onOpenFile={(ref, path) =>
                onOpenEditableFile(groupIndex, ref, path)
              }
              onRestore={onRestoreSnapshot}
            />
          </div>
          {renderHistoryPanel()}
        </div>
      </section>
    );
  }

  if (revisionView) {
    return (
      <section
        className={styles.editorGroup}
        data-active={isActiveGroup || undefined}
      >
        <FileTabBar
          tabs={group.tabs}
          activeKey={group.active}
          dirtyPaths={dirty}
          groupLabel={`group ${groupIndex + 1}`}
          onSelect={(key) => onSelectTab(groupIndex, key)}
          onClose={(key) => onCloseTab(groupIndex, key)}
        />
        <div className={styles.editorGroupBody}>
          <div className={styles.editorPrimaryPane}>
            <FileRevisionPane
              revisionView={revisionView}
              historyOpen={historyOpen}
              onToggleHistory={toggleHistory}
              onClose={() => onCloseRevision(groupIndex)}
            />
          </div>
          {renderHistoryPanel()}
        </div>
      </section>
    );
  }

  return (
    <section
      className={styles.editorGroup}
      data-active={isActiveGroup || undefined}
    >
      <FileTabBar
        tabs={group.tabs}
        activeKey={group.active}
        dirtyPaths={dirty}
        groupLabel={`group ${groupIndex + 1}`}
        onSelect={(key) => onSelectTab(groupIndex, key)}
        onClose={(key) => onCloseTab(groupIndex, key)}
      />
      <div className={styles.editorGroupBody}>
        <div className={styles.editorPrimaryPane}>
          <WorkspaceFilePane
            path={activePath}
            fileData={fileData}
            isActive={isActiveGroup}
            isLoading={isLoading}
            error={error}
            content={editor.content}
            isDirty={editor.isDirty}
            isSaving={editor.isSaving}
            searchOpen={searchOpen}
            onContentChange={editor.handleContentChange}
            onSave={() => void editor.save()}
            historyOpen={historyOpen}
            onToggleHistory={toggleHistory}
            onToggleSearch={() => setSearchOpen((open) => !open)}
            onSplitRight={() => onSplitRight(groupIndex)}
            onNavigate={(dirPath) => onNavigate(scopeRef, dirPath)}
            lineTarget={lineTarget}
            onLineTargetApplied={(_path, token) => {
              if (activeKey) onLineTargetApplied(activeKey, token);
            }}
            gitGutterMarks={gitGutterMarks}
            blameEnabled={blameEnabled}
            blameLines={blameData?.skipped ? [] : blameData?.lines}
            blameLoading={blameLoading}
            blameSkippedMessage={
              blameError ?? (blameData?.skipped ? blameData.message : undefined)
            }
            onToggleBlame={() => setBlameEnabled((enabled) => !enabled)}
            onOpenBlameCommit={(sha) => {
              if (!activePath) return;
              onOpenDiff(groupIndex, {
                ref: scopeRef,
                path: activePath,
                from: `${sha}^`,
                to: sha,
                title: sha.slice(0, 8),
              });
            }}
          />
        </div>
        {renderHistoryPanel()}
      </div>
    </section>
  );
}
