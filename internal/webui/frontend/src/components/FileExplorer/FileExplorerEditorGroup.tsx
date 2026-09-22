import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { DiffFileViewer } from "@/components/AgentDetailPanel";
import type { FileBlameData } from "@/api/workspace";
import {
  blameScopedFile,
  diffScopedFile,
  fetchDiffFile,
  readScopedFile,
} from "@/hooks/api";
import {
  useFileDocument,
  useWorkspaceContext,
  type FileBrowserGroup,
  type FileBrowserTab,
} from "@/hooks";
import { type CheckoutRef } from "@/utils/fileExplorerRefs";
import {
  asCheckoutRef,
  checkoutExplorerRef,
  explorerRefKey,
  tabIdentityKey,
  type ExplorerRef,
  type SkillsExplorerRef,
} from "@/utils/explorerRefs";
import { useSkillsActions } from "@/hooks";

import {
  FileHistoryPanel,
  type HistoryOpenDiffRequest,
  type HistoryOpenRevisionRequest,
  type HistorySubject,
} from "./FileHistoryPanel";
import { FileRevisionPane } from "./FileRevisionPane";
import { FileTabBar } from "./FileTabBar";
import { computeGitGutterLineMarks, type GitGutterLineMark } from "./gitGutter";
import { WorkspaceFilePane } from "./WorkspaceFilePane";
import { SkillMetadataBar } from "./skills";
import { buildContentDiffPatch } from "./fileExplorerLocalUtils";
import styles from "./FileExplorer.module.css";
import type {
  DiffViewState,
  LineTarget,
  PatchDiffViewState,
  RevisionViewState,
} from "./workspaceFileBrowserTypes";

function DiffEditorPane({
  diffView,
  historyOpen,
  onToggleHistory,
  onClose,
  onOpenFile,
}: {
  diffView: DiffViewState;
  historyOpen: boolean;
  onToggleHistory: () => void;
  onClose: () => void;
  onOpenFile: (ref: CheckoutRef, path: string) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const initialPatch = diffView.kind === "patch" ? diffView.patch : null;
  const [patch, setPatch] = useState<string | null>(initialPatch);
  const [isLoading, setIsLoading] = useState(diffView.kind !== "patch");
  const [error, setError] = useState<string | undefined>(undefined);

  useEffect(() => {
    let canceled = false;
    const suppliedPatch =
      diffView.kind === "patch" ? diffView.patch : undefined;
    setPatch(suppliedPatch ?? null);
    setError(undefined);
    if (suppliedPatch !== undefined) {
      setIsLoading(false);
      return () => {
        canceled = true;
      };
    }
    setIsLoading(true);
    const branchAgent =
      diffView.kind === "checkout" &&
      diffView.source === "branch" &&
      diffView.ref.scope === "agent"
        ? diffView.ref.target
        : undefined;
    const loadPatch =
      branchAgent && diffView.kind === "checkout"
        ? fetchDiffFile(workspaceId, branchAgent, diffView.path, "HEAD").then(
            (res) => {
              if (canceled) return;
              if (res.is_binary) {
                setError("Binary file — no text diff.");
                setPatch(null);
                return;
              }
              if (res.is_too_large) {
                setError("Diff too large to display.");
                setPatch(null);
                return;
              }
              setPatch(res.patch);
            },
          )
        : diffView.kind === "checkout"
          ? diffScopedFile(
              workspaceId,
              diffView.ref,
              diffView.path,
              diffView.from,
              diffView.to,
            ).then((res) => {
              if (canceled) return;
              setPatch(res.patch);
            })
          : Promise.resolve();
    loadPatch
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
          {diffView.kind === "checkout" && (
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
          )}
          {diffView.kind === "checkout" && diffView.canOpenFile && (
            <button
              type="button"
              className={styles.saveButton}
              onClick={() => onOpenFile(diffView.ref, diffView.path)}
            >
              Open file
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
  historyRefreshKey,
  canWrite,
  lineTarget,
  onLineTargetApplied,
  onDeleteSkill,
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
  onNavigate: (ref: ExplorerRef, dirPath: string) => void;
  onSaved: (tab: FileBrowserTab) => void;
  onOpenDiff: (
    groupIndex: number,
    request: HistoryOpenDiffRequest | PatchDiffViewState,
  ) => void;
  onCloseDiff: (groupIndex: number) => void;
  onOpenRevision: (
    groupIndex: number,
    request: HistoryOpenRevisionRequest,
  ) => void;
  onCloseRevision: (groupIndex: number) => void;
  onOpenEditableFile: (
    groupIndex: number,
    ref: CheckoutRef,
    path: string,
  ) => void;
  historyRefreshKey: number;
  canWrite: boolean;
  lineTarget?: LineTarget | undefined;
  onLineTargetApplied: (tabKey: string, token: number) => void;
  onDeleteSkill: (ref: SkillsExplorerRef, name: string) => void;
}) {
  const { workspaceId } = useWorkspaceContext();
  const skillActions = useSkillsActions(workspaceId);
  const activeTab =
    group.tabs.find((tab) => tabIdentityKey(tab) === group.active) ?? null;
  const explorerRef = useMemo<ExplorerRef>(
    () => activeTab?.ref ?? checkoutExplorerRef({ scope: "workspace" }),
    [activeTab],
  );
  const checkoutRef = asCheckoutRef(explorerRef);
  const activePath = activeTab?.path ?? null;
  const activeKey = activeTab ? tabIdentityKey(activeTab) : null;
  const scopeKey = explorerRefKey(explorerRef);
  const fileDocument = useFileDocument(
    workspaceId,
    explorerRef,
    activePath ?? "",
  );
  const { fileData, isLoading, error } = fileDocument;
  const refreshDocumentRef = useRef(fileDocument.refresh);
  refreshDocumentRef.current = fileDocument.refresh;
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
    return checkoutRef ? { ref: checkoutRef, path: activePath } : null;
  }, [activePath, activeTab, checkoutRef, diffView, revisionView]);
  const toggleHistory = useCallback(() => setHistoryOpen((open) => !open), []);
  const closeHistory = useCallback(() => setHistoryOpen(false), []);
  const renderHistoryPanel = () =>
    historyOpen && historySubject ? (
      <FileHistoryPanel
        subject={historySubject}
        refreshKey={historyRefreshKey}
        onClose={closeHistory}
        onOpenDiff={(request) => onOpenDiff(groupIndex, request)}
        onOpenRevision={(request) => onOpenRevision(groupIndex, request)}
      />
    ) : null;

  useEffect(() => {
    setSearchOpen(false);
    if (activePath) {
      void refreshDocumentRef.current();
    }
  }, [activePath, scopeKey]);

  const canDisplayText =
    !!activePath && !!fileData && !fileData.binary && !fileData.truncated;
  const effectiveCanWrite =
    explorerRef.kind === "skills"
      ? skillActions.canEdit(explorerRef.group)
      : canWrite;
  const canSave = effectiveCanWrite && canDisplayText;

  const loadBaseContent = useCallback(
    async (path: string) => {
      if (!checkoutRef) {
        setBasePath(null);
        setBaseContent(null);
        return;
      }
      try {
        const data = await readScopedFile(
          workspaceId,
          checkoutRef,
          path,
          "HEAD",
        );
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
    [checkoutRef, workspaceId],
  );

  const save = useCallback(async () => {
    if (!canSave || !activeTab || !activePath) return;
    const result = await fileDocument.save();
    if (result) {
      onSaved(activeTab);
      void loadBaseContent(activePath);
    }
  }, [activePath, activeTab, canSave, fileDocument, loadBaseContent, onSaved]);

  const compareExternal = useCallback(() => {
    if (!activePath || !fileDocument.externalConflict) return;
    onOpenDiff(groupIndex, {
      kind: "patch",
      path: activePath,
      title: "External vs local draft",
      patch: buildContentDiffPatch(
        activePath,
        fileDocument.externalConflict.content,
        fileDocument.content,
      ),
    });
  }, [
    activePath,
    fileDocument.content,
    fileDocument.externalConflict,
    groupIndex,
    onOpenDiff,
  ]);

  const overwriteExternal = useCallback(async () => {
    if (!effectiveCanWrite || !activeTab || !activePath) return;
    const result = await fileDocument.overwriteExternal();
    if (result) {
      onSaved(activeTab);
      void loadBaseContent(activePath);
    }
  }, [
    activePath,
    activeTab,
    effectiveCanWrite,
    fileDocument,
    loadBaseContent,
    onSaved,
  ]);

  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    if (!isActiveGroup) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        void saveRef.current();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isActiveGroup]);

  useEffect(() => {
    setBasePath(null);
    setBaseContent(null);
    setGitGutterMarks([]);
    setBlameEnabled(false);
    setBlameData(null);
    setBlameError(null);
    if (activePath && canDisplayText) {
      void loadBaseContent(activePath);
    }
  }, [activePath, canDisplayText, loadBaseContent]);

  useEffect(() => {
    const handleFocus = () => {
      if (activePath && canDisplayText) {
        void refreshDocumentRef.current();
        void loadBaseContent(activePath);
      }
    };
    window.addEventListener("focus", handleFocus);
    return () => window.removeEventListener("focus", handleFocus);
  }, [activePath, canDisplayText, loadBaseContent]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      if (!activePath || basePath !== activePath || baseContent === null) {
        setGitGutterMarks([]);
        return;
      }
      setGitGutterMarks(
        computeGitGutterLineMarks(baseContent, fileDocument.content),
      );
    }, 150);
    return () => window.clearTimeout(timer);
  }, [activePath, baseContent, basePath, fileDocument.content]);

  useEffect(() => {
    let canceled = false;
    if (!activePath || !blameEnabled || !canDisplayText) {
      setBlameData(null);
      setBlameLoading(false);
      setBlameError(null);
      return () => {
        canceled = true;
      };
    }
    setBlameLoading(true);
    setBlameError(null);
    if (!checkoutRef)
      return () => {
        canceled = true;
      };
    blameScopedFile(workspaceId, checkoutRef, activePath)
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
  }, [activePath, blameEnabled, canDisplayText, checkoutRef, workspaceId]);

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
              canWrite={canWrite}
              historyOpen={historyOpen}
              onToggleHistory={toggleHistory}
              onClose={() => onCloseRevision(groupIndex)}
              onRestored={() => {
                void fileDocument.refresh();
                if (activeTab) onSaved(activeTab);
                onCloseRevision(groupIndex);
              }}
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
      {activeTab?.ref.kind === "skills" && activePath && (
        <SkillMetadataBar
          workspaceId={workspaceId}
          refInfo={activeTab.ref}
          path={activePath}
          onDelete={(name) =>
            onDeleteSkill(activeTab.ref as SkillsExplorerRef, name)
          }
        />
      )}
      <div className={styles.editorGroupBody}>
        <div className={styles.editorPrimaryPane}>
          <WorkspaceFilePane
            path={activePath}
            fileData={fileData}
            isActive={isActiveGroup}
            isLoading={isLoading}
            error={error}
            content={fileDocument.content}
            isDirty={fileDocument.dirty}
            isSaving={fileDocument.isSaving}
            canWrite={effectiveCanWrite}
            showGitAffordances={!!checkoutRef}
            externalConflict={fileDocument.externalConflict}
            searchOpen={searchOpen}
            onContentChange={fileDocument.edit}
            onSave={() => void save()}
            onReloadExternal={() => {
              fileDocument.useExternal();
              if (activePath) void loadBaseContent(activePath);
            }}
            onCompareExternal={compareExternal}
            onOverwriteExternal={() => void overwriteExternal()}
            historyOpen={historyOpen}
            onToggleHistory={toggleHistory}
            onToggleSearch={() => setSearchOpen((open) => !open)}
            onSplitRight={() => onSplitRight(groupIndex)}
            onNavigate={(dirPath) => onNavigate(explorerRef, dirPath)}
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
              if (!checkoutRef) return;
              onOpenDiff(groupIndex, {
                ref: checkoutRef,
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
