/**
 * EpicTaskTree renders a hierarchical epic/task tree for a workspace.
 * Root component: TalkToLeadEntry, EpicRows with nested TaskRows, and orphan tasks.
 */

import type React from "react";
import { useState, useCallback, useRef, useEffect } from "react";

import { updateIssue, closeIssue, createWorkspaceEpic } from "@/hooks/api";
import { useWorkspaceTree, useWorkspaceContext } from "@/hooks/workspace";
import { useToast } from "@/hooks/ui";
import { useInlineCreate } from "@/hooks/issues";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import {
  EpicContextMenu,
  TaskContextMenu,
} from "@/components/WorkspaceTree/menus";

import { TalkToLeadEntry } from "./TalkToLeadEntry";
import { EpicRow } from "./EpicRow";
import { TaskRow } from "./TaskRow";
import { InlineAddInput } from "./InlineAddInput";
import styles from "./EpicTaskTree.module.css";

// Scoped key suffix for epic collapse state
const SK_EPIC_COLLAPSED = "tree-epic-collapsed";

export interface EpicTaskTreeProps {
  workspaceName: string;
  activeFilter: "active" | "all";
  selectedId?: string | undefined;
  onSelect?: ((issueId: string) => void) | undefined;
  sourceRepos?: string[] | undefined;
  /** Backend name for TalkToLeadEntry display. */
  backend?: string | undefined;
  /** Callback when Talk to Lead is clicked. */
  onTalkToLead?: ((workspaceName: string) => void) | undefined;
  /** Callback when a task with an active agent is clicked for terminal. */
  onTaskTerminalOpen?:
    | ((issueId: string, agentName: string) => void)
    | undefined;
}

interface ContextMenuState {
  type: "epic" | "task";
  id: string;
  title: string;
  x: number;
  y: number;
}

/** Load collapse state from scoped localStorage. */
function loadCollapseState(wsId: string | null): Record<string, boolean> {
  if (!wsId) return {};
  const stored = wsGet(wsId, SK_EPIC_COLLAPSED);
  if (!stored) return {};
  try {
    return JSON.parse(stored);
  } catch {
    return {};
  }
}

/** Save collapse state to scoped localStorage. */
function saveCollapseState(
  wsId: string | null,
  state: Record<string, boolean>,
): void {
  if (!wsId) return;
  wsSet(wsId, SK_EPIC_COLLAPSED, JSON.stringify(state));
}

export function EpicTaskTree({
  workspaceName,
  activeFilter,
  selectedId,
  onSelect,
  sourceRepos,
  backend,
  onTalkToLead,
  onTaskTerminalOpen,
}: EpicTaskTreeProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const { epics, orphanTasks, isLoading, refetch } = useWorkspaceTree(
    workspaceName,
    activeFilter,
    sourceRepos,
  );

  const { showToast } = useToast();

  const [collapseState, setCollapseState] = useState<Record<string, boolean>>(
    () => loadCollapseState(workspaceId),
  );

  // Re-read collapse state when workspace changes (SPA navigation)
  useEffect(() => {
    setCollapseState(loadCollapseState(workspaceId));
  }, [workspaceId]);

  const handleToggle = useCallback(
    (epicId: string) => {
      setCollapseState((prev) => {
        const next = { ...prev, [epicId]: !prev[epicId] };
        saveCollapseState(workspaceId, next);
        return next;
      });
    },
    [workspaceId],
  );

  // Show orphan tasks in an "Ungrouped" collapsible section
  const [orphansCollapsed, setOrphansCollapsed] = useState(false);

  // Context menu state
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);

  // Inline editing state
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingType, setEditingType] = useState<"epic" | "task" | null>(null);
  const [draftTitle, setDraftTitle] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const renameInputRef = useRef<HTMLInputElement | null>(null);

  const handleCreateEpic = useCallback(
    (title: string) => createWorkspaceEpic(workspaceId, title),
    [workspaceId],
  );

  const addEpic = useInlineCreate({
    createFn: handleCreateEpic,
    onSuccess: (issue) => {
      showToast(`Epic ${issue.id} created`, { type: "success" });
      refetch();
    },
    onError: (msg) => {
      showToast(msg, { type: "error" });
    },
  });

  const handleOverflowClick = useCallback(
    (e: React.MouseEvent, type: "epic" | "task", id: string, title: string) => {
      e.preventDefault();
      setContextMenu({ type, id, title, x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleEpicOverflowClick = useCallback(
    (e: React.MouseEvent, epicId: string) => {
      const epic = epics.find((entry) => entry.epic.id === epicId);
      handleOverflowClick(e, "epic", epicId, epic?.epic.title ?? "");
    },
    [epics, handleOverflowClick],
  );

  const handleTaskOverflowClick = useCallback(
    (e: React.MouseEvent, taskId: string) => {
      // Search all tasks (epic children + orphans)
      let title = "";
      for (const entry of epics) {
        const found = entry.tasks.find((t) => t.id === taskId);
        if (found) {
          title = found.title;
          break;
        }
      }
      if (!title) {
        const found = orphanTasks.find((t) => t.id === taskId);
        if (found) title = found.title;
      }
      handleOverflowClick(e, "task", taskId, title);
    },
    [epics, orphanTasks, handleOverflowClick],
  );

  const handleCloseContextMenu = useCallback(() => {
    setContextMenu(null);
  }, []);

  const handleStartRename = useCallback(() => {
    if (!contextMenu) return;
    setEditingId(contextMenu.id);
    setEditingType(contextMenu.type);
    setDraftTitle(contextMenu.title);
    setRenameError(null);
    // Focus the input after render
    setTimeout(() => renameInputRef.current?.focus(), 0);
  }, [contextMenu]);

  const handleSaveRename = useCallback(async () => {
    if (!editingId || isSaving) return;

    const trimmed = draftTitle.trim();
    if (!trimmed) {
      setRenameError("Title cannot be empty");
      return;
    }

    // Find the current title to check if it changed
    let currentTitle = "";
    if (editingType === "epic") {
      const entry = epics.find((e) => e.epic.id === editingId);
      currentTitle = entry?.epic.title ?? "";
    } else {
      for (const entry of epics) {
        const found = entry.tasks.find((t) => t.id === editingId);
        if (found) {
          currentTitle = found.title;
          break;
        }
      }
      if (!currentTitle) {
        const found = orphanTasks.find((t) => t.id === editingId);
        currentTitle = found?.title ?? "";
      }
    }

    // No-op if title unchanged
    if (trimmed === currentTitle) {
      setEditingId(null);
      setEditingType(null);
      setDraftTitle("");
      setRenameError(null);
      return;
    }

    setIsSaving(true);
    try {
      await updateIssue(workspaceId, editingId, { title: trimmed });
      setEditingId(null);
      setEditingType(null);
      setDraftTitle("");
      setRenameError(null);
      await refetch();
      showToast(`${editingType === "epic" ? "Epic" : "Task"} renamed`, {
        type: "success",
      });
    } catch {
      setRenameError("Failed to rename");
    } finally {
      setIsSaving(false);
    }
  }, [
    editingId,
    editingType,
    draftTitle,
    isSaving,
    epics,
    orphanTasks,
    refetch,
    showToast,
  ]);

  const handleCancelRename = useCallback(() => {
    setEditingId(null);
    setEditingType(null);
    setDraftTitle("");
    setRenameError(null);
  }, []);

  const handleMarkDone = useCallback(async () => {
    if (!contextMenu) return;
    const { id, type } = contextMenu;
    try {
      await closeIssue(workspaceId, id);
      await refetch();
      showToast(`${type === "epic" ? "Epic" : "Task"} marked as done`, {
        type: "success",
      });
    } catch {
      showToast("Failed to mark as done", { type: "error" });
    }
  }, [contextMenu, refetch, showToast]);

  const handleArchive = useCallback(async () => {
    if (!contextMenu) return;
    const { id, type } = contextMenu;
    try {
      await updateIssue(workspaceId, id, { status: "tombstone" });
      await refetch();
      showToast(`${type === "epic" ? "Epic" : "Task"} archived`, {
        type: "success",
      });
    } catch {
      showToast("Failed to archive", { type: "error" });
    }
  }, [contextMenu, refetch, showToast]);

  const hasContent = epics.length > 0 || orphanTasks.length > 0;

  if (isLoading) {
    return (
      <div className={styles.treeSection}>
        <TalkToLeadEntry
          workspaceName={workspaceName}
          backend={backend}
          onTalkToLead={onTalkToLead}
        />
        <div className={styles.loadingSkeleton}>
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
        </div>
      </div>
    );
  }

  return (
    <div className={styles.treeSection}>
      <TalkToLeadEntry
        workspaceName={workspaceName}
        backend={backend}
        onTalkToLead={onTalkToLead}
      />

      {!hasContent && (
        <div className={styles.emptyTree}>
          {activeFilter === "active" ? "No active tasks" : "No epics or tasks"}
        </div>
      )}

      {epics.map(({ epic, tasks }) => (
        <EpicRow
          key={epic.id}
          epic={epic}
          tasks={tasks}
          isCollapsed={!!collapseState[epic.id]}
          onToggle={() => handleToggle(epic.id)}
          selectedId={selectedId}
          onSelect={onSelect}
          onOverflowClick={handleEpicOverflowClick}
          isEditing={editingId === epic.id && editingType === "epic"}
          draftTitle={editingId === epic.id ? draftTitle : undefined}
          onDraftChange={editingId === epic.id ? setDraftTitle : undefined}
          onSaveRename={editingId === epic.id ? handleSaveRename : undefined}
          onCancelRename={
            editingId === epic.id ? handleCancelRename : undefined
          }
          renameInputRef={editingId === epic.id ? renameInputRef : undefined}
          renameError={editingId === epic.id ? renameError : undefined}
          isSaving={editingId === epic.id ? isSaving : undefined}
          taskOverflowClick={handleTaskOverflowClick}
          editingTaskId={
            editingType === "task" ? (editingId ?? undefined) : undefined
          }
          taskDraftTitle={editingType === "task" ? draftTitle : undefined}
          onTaskDraftChange={editingType === "task" ? setDraftTitle : undefined}
          onTaskSaveRename={
            editingType === "task" ? handleSaveRename : undefined
          }
          onTaskCancelRename={
            editingType === "task" ? handleCancelRename : undefined
          }
          taskRenameInputRef={
            editingType === "task" ? renameInputRef : undefined
          }
          taskRenameError={editingType === "task" ? renameError : undefined}
          taskIsSaving={editingType === "task" ? isSaving : undefined}
          onTaskCreated={refetch}
          onTaskTerminalOpen={onTaskTerminalOpen}
        />
      ))}

      {/* Inline add epic */}
      {addEpic.isAdding ? (
        <InlineAddInput
          placeholder="New epic name"
          onSubmit={addEpic.submitTitle}
          onCancel={addEpic.cancelAdding}
          isSubmitting={addEpic.isSubmitting}
          error={addEpic.error}
        />
      ) : (
        <button
          type="button"
          className={styles.addButton}
          onClick={addEpic.startAdding}
          data-testid="add-epic"
        >
          + Add epic
        </button>
      )}

      {orphanTasks.length > 0 && (
        <div className={styles.epicGroup}>
          <div className={styles.epicRow}>
            <button
              type="button"
              className={styles.epicRowButton}
              onClick={() => setOrphansCollapsed((p) => !p)}
              title="Ungrouped tasks"
            >
              <span className={styles.epicIcon}>
                <svg
                  width="16"
                  height="16"
                  viewBox="0 0 16 16"
                  fill="none"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <rect
                    x="2"
                    y="3"
                    width="12"
                    height="10"
                    rx="1.5"
                    stroke="currentColor"
                    strokeWidth="1.3"
                    strokeDasharray="2 1.5"
                  />
                </svg>
              </span>
              <span className={styles.titleText}>Ungrouped</span>
            </button>
            <span
              className={styles.collapseChevron}
              data-expanded={!orphansCollapsed}
              role="img"
              aria-label={
                orphansCollapsed ? "Expand ungrouped" : "Collapse ungrouped"
              }
              onClick={() => setOrphansCollapsed((p) => !p)}
            >
              &rsaquo;
            </span>
          </div>
          {!orphansCollapsed && (
            <div className={styles.epicChildren}>
              {orphanTasks.map((task) => (
                <TaskRow
                  key={task.id}
                  task={task}
                  isSelected={selectedId === task.id}
                  onSelect={onSelect}
                  onOverflowClick={handleTaskOverflowClick}
                  onTaskTerminalOpen={onTaskTerminalOpen}
                  isEditing={editingId === task.id && editingType === "task"}
                  draftTitle={
                    editingId === task.id && editingType === "task"
                      ? draftTitle
                      : undefined
                  }
                  onDraftChange={
                    editingId === task.id && editingType === "task"
                      ? setDraftTitle
                      : undefined
                  }
                  onSaveRename={
                    editingId === task.id && editingType === "task"
                      ? handleSaveRename
                      : undefined
                  }
                  onCancelRename={
                    editingId === task.id && editingType === "task"
                      ? handleCancelRename
                      : undefined
                  }
                  renameInputRef={
                    editingId === task.id && editingType === "task"
                      ? renameInputRef
                      : undefined
                  }
                  renameError={
                    editingId === task.id && editingType === "task"
                      ? renameError
                      : undefined
                  }
                  isSaving={
                    editingId === task.id && editingType === "task"
                      ? isSaving
                      : undefined
                  }
                />
              ))}
            </div>
          )}
        </div>
      )}

      {/* Context menus */}
      {contextMenu?.type === "epic" && (
        <EpicContextMenu
          isOpen
          position={{ x: contextMenu.x, y: contextMenu.y }}
          onRename={handleStartRename}
          onMarkDone={handleMarkDone}
          onArchive={handleArchive}
          onClose={handleCloseContextMenu}
        />
      )}
      {contextMenu?.type === "task" && (
        <TaskContextMenu
          isOpen
          position={{ x: contextMenu.x, y: contextMenu.y }}
          onRename={handleStartRename}
          onMarkDone={handleMarkDone}
          onArchive={handleArchive}
          onClose={handleCloseContextMenu}
        />
      )}
    </div>
  );
}
