/**
 * WorkspaceTree component displays a collapsible sidebar with repo navigation.
 * Shows all repos in the workspace with per-repo agent counts and status.
 * Includes workspace entries with context menu rename support.
 */

import { useState, useCallback, useEffect, useMemo, useRef } from "react";

import {
  DndContext,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  verticalListSortingStrategy,
  arrayMove,
} from "@dnd-kit/sortable";

import {
  renameWorkspace,
  deleteWorkspace,
  reorderWorkspaces,
  refreshWorkspace,
} from "@/api/workspace";
import type { WorkspaceSummary } from "@/api/workspace";
import { useWorkspaceRepos, useAgentContext, useToast } from "@/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";

import { SortableWorkspaceEntry } from "./SortableWorkspaceEntry";
import styles from "./WorkspaceTree.module.css";
import { WorkspaceContextMenu } from "./WorkspaceContextMenu";

/**
 * Props for the WorkspaceTree component.
 */
export interface WorkspaceTreeProps {
  /** Additional CSS class name */
  className?: string;
  /** Default collapsed state */
  defaultCollapsed?: boolean;
  /** Currently active repo name, or null/undefined for "All Workspaces" */
  activeRepoName?: string | null | undefined;
  /** Callback when a workspace/repo is selected. null = "All Workspaces" */
  onWorkspaceSelect?: (repoName: string | null) => void;
}

const COLLAPSE_STORAGE_KEY = "workspace-tree-collapsed";

/**
 * WorkspaceTree displays a collapsible sidebar with repo navigation.
 * Consumes useWorkspaceRepos for repo list and useAgents for agent counts.
 */
export function WorkspaceTree({
  className,
  defaultCollapsed = true,
  activeRepoName,
  onWorkspaceSelect,
}: WorkspaceTreeProps): JSX.Element {
  // Load initial collapsed state from localStorage
  const [isCollapsed, setIsCollapsed] = useState(() => {
    try {
      const stored = localStorage.getItem(COLLAPSE_STORAGE_KEY);
      return stored !== null ? stored === "true" : defaultCollapsed;
    } catch {
      return defaultCollapsed;
    }
  });

  const { workspace, repos, isLoading, error, refetch } = useWorkspaceRepos();
  const { agents } = useAgentContext();
  const { showToast } = useToast();

  // Workspace delete state
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [pendingDeleteName, setPendingDeleteName] = useState<string | null>(
    null,
  );
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deletionPendingRef = useRef(false);

  // Workspace rename state
  const [editingWorkspace, setEditingWorkspace] = useState<string | null>(null);
  const [draftName, setDraftName] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const renameInputRef = useRef<HTMLInputElement>(null);

  // Context menu state
  const [contextMenu, setContextMenu] = useState<{
    workspaceName: string;
    x: number;
    y: number;
  } | null>(null);

  // Workspace ordering state for drag-and-drop reorder
  const [workspaceOrder, setWorkspaceOrder] = useState<string[]>([]);

  // Initialize/sync order from API data
  useEffect(() => {
    if (workspace?.workspaces) {
      setWorkspaceOrder(workspace.workspaces.map((ws) => ws.name));
    }
  }, [workspace?.workspaces]);

  // DnD sensors with 5px activation distance to prevent accidental drags on click
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  // Re-fetch order from server on error (safe under rapid sequential reorders)
  const rollbackOrder = useCallback(() => {
    refreshWorkspace().then((data) => {
      if (data?.workspaces) {
        setWorkspaceOrder(data.workspaces.map((w) => w.name));
      }
    });
  }, []);

  // Drag-end handler: reorder and persist
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;

      setWorkspaceOrder((prev) => {
        const oldIndex = prev.indexOf(active.id as string);
        const newIndex = prev.indexOf(over.id as string);
        if (oldIndex < 0 || newIndex < 0) return prev;
        const newOrder = arrayMove(prev, oldIndex, newIndex);
        reorderWorkspaces(newOrder).catch(rollbackOrder);
        return newOrder;
      });
    },
    [rollbackOrder],
  );

  // Alt+Up keyboard reorder
  const handleMoveUp = useCallback(
    (name: string) => {
      setWorkspaceOrder((prev) => {
        const idx = prev.indexOf(name);
        if (idx <= 0) return prev;
        const newOrder = arrayMove(prev, idx, idx - 1);
        reorderWorkspaces(newOrder).catch(rollbackOrder);
        return newOrder;
      });
    },
    [rollbackOrder],
  );

  // Alt+Down keyboard reorder
  const handleMoveDown = useCallback(
    (name: string) => {
      setWorkspaceOrder((prev) => {
        const idx = prev.indexOf(name);
        if (idx < 0 || idx >= prev.length - 1) return prev;
        const newOrder = arrayMove(prev, idx, idx + 1);
        reorderWorkspaces(newOrder).catch(rollbackOrder);
        return newOrder;
      });
    },
    [rollbackOrder],
  );

  // Persist collapsed state
  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_STORAGE_KEY, String(isCollapsed));
    } catch {
      // Ignore localStorage errors
    }
  }, [isCollapsed]);

  // Focus rename input when entering edit mode
  useEffect(() => {
    if (editingWorkspace && renameInputRef.current) {
      renameInputRef.current.focus();
      renameInputRef.current.select();
    }
  }, [editingWorkspace]);

  const handleToggle = useCallback(() => {
    setIsCollapsed((prev) => !prev);
  }, []);

  // Context menu handlers
  const handleOverflowClick = useCallback(
    (e: React.MouseEvent, wsName: string) => {
      e.stopPropagation();
      setContextMenu({ workspaceName: wsName, x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleContextMenu = useCallback(
    (e: React.MouseEvent, wsName: string) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({ workspaceName: wsName, x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleCloseContextMenu = useCallback(() => {
    setContextMenu(null);
  }, []);

  // Rename handlers
  const handleStartRename = useCallback(() => {
    if (!contextMenu) return;
    setEditingWorkspace(contextMenu.workspaceName);
    setDraftName(contextMenu.workspaceName);
    setRenameError(null);
  }, [contextMenu]);

  const handleCancelRename = useCallback(() => {
    setEditingWorkspace(null);
    setDraftName("");
    setRenameError(null);
  }, []);

  const handleSaveRename = useCallback(async () => {
    if (!editingWorkspace) return;

    const trimmed = draftName.trim();

    // Client-side pre-validation: empty name
    if (!trimmed) {
      setRenameError("Name cannot be empty");
      setTimeout(() => renameInputRef.current?.focus(), 0);
      return;
    }

    // No-op if name unchanged
    if (trimmed === editingWorkspace) {
      setEditingWorkspace(null);
      return;
    }

    setIsSaving(true);
    setRenameError(null);

    try {
      await renameWorkspace(editingWorkspace, trimmed);
      setEditingWorkspace(null);
      refetch();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to rename workspace";
      setRenameError(message);
      setTimeout(() => renameInputRef.current?.focus(), 0);
    } finally {
      setIsSaving(false);
    }
  }, [editingWorkspace, draftName, refetch]);

  const handleRenameKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        handleSaveRename();
      } else if (e.key === "Escape") {
        e.preventDefault();
        handleCancelRename();
      }
    },
    [handleSaveRename, handleCancelRename],
  );

  // Delete handlers
  const handleStartRemove = useCallback(() => {
    if (!contextMenu) return;
    setPendingDeleteName(contextMenu.workspaceName);
    setConfirmDeleteOpen(true);
  }, [contextMenu]);

  const handleCancelDelete = useCallback(() => {
    setConfirmDeleteOpen(false);
    setPendingDeleteName(null);
  }, []);

  const handleConfirmDelete = useCallback(() => {
    if (!pendingDeleteName) return;
    const nameToDelete = pendingDeleteName;
    setConfirmDeleteOpen(false);
    setPendingDeleteName(null);

    // Show undo toast with 5-second duration
    deletionPendingRef.current = false;

    // Start delayed deletion
    deleteTimerRef.current = setTimeout(async () => {
      deletionPendingRef.current = true;
      try {
        await deleteWorkspace(nameToDelete);
        refetch();
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove workspace";
        showToast(message, { type: "error" });
        refetch();
      }
    }, 5000);

    showToast(`Workspace "${nameToDelete}" removed`, {
      type: "success",
      duration: 5000,
      onUndo: () => {
        // Cancel the pending deletion
        if (deleteTimerRef.current) {
          clearTimeout(deleteTimerRef.current);
          deleteTimerRef.current = null;
        }
        if (deletionPendingRef.current) {
          showToast("Deletion already in progress", { type: "info" });
          return;
        }
        showToast(`Workspace "${nameToDelete}" restored`, { type: "info" });
        refetch();
      },
    });
  }, [pendingDeleteName, refetch, showToast]);

  // Cleanup delete timer on unmount
  useEffect(() => {
    return () => {
      if (deleteTimerRef.current) {
        clearTimeout(deleteTimerRef.current);
      }
    };
  }, []);

  // Workspace summaries from the data
  const workspaces: WorkspaceSummary[] = workspace?.workspaces ?? [];

  // Compute per-repo agent counts
  const repoAgentCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const repo of repos) {
      counts.set(repo.name, 0);
    }
    for (const agent of agents) {
      if (agent.repo) {
        for (const repo of repos) {
          if (agent.repo === repo.name || agent.repo === repo.path) {
            counts.set(repo.name, (counts.get(repo.name) ?? 0) + 1);
            break;
          }
        }
      }
    }
    return counts;
  }, [repos, agents]);

  // Count total active agents across workspace repos
  const totalActiveCount = useMemo(() => {
    let count = 0;
    for (const [, v] of repoAgentCounts) {
      count += v;
    }
    return count;
  }, [repoAgentCounts]);

  const rootClassName = [
    styles.sidebar,
    isCollapsed && styles.collapsed,
    className,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <aside className={rootClassName} data-collapsed={isCollapsed}>
      <button
        type="button"
        className={styles.toggleButton}
        onClick={handleToggle}
        aria-expanded={!isCollapsed}
        aria-label={
          isCollapsed ? "Expand workspace tree" : "Collapse workspace tree"
        }
      >
        {!isCollapsed && (
          <>
            <span className={styles.toggleText}>Workspace</span>
            <span className={styles.sectionCount}>{repos.length}</span>
          </>
        )}
        <span className={styles.toggleIcon}>{isCollapsed ? ">" : "<"}</span>
      </button>

      {!isCollapsed && (
        <div className={styles.content}>
          {isLoading && repos.length === 0 && (
            <div className={styles.loading}>
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
              <div className={styles.skeletonRow} />
            </div>
          )}

          {error && (
            <div className={styles.errorState}>
              <span className={styles.errorText}>{error}</span>
              <button
                type="button"
                className={styles.retryButton}
                onClick={refetch}
              >
                Retry
              </button>
            </div>
          )}

          {!isLoading && !error && repos.length === 0 && (
            <div className={styles.emptyState}>No repos in workspace</div>
          )}

          {workspaces.length > 1 && (
            <div className={styles.workspaceSection}>
              <div className={styles.workspaceSectionHeader}>Workspaces</div>
              <DndContext
                sensors={sensors}
                collisionDetection={closestCenter}
                onDragEnd={handleDragEnd}
              >
                <SortableContext
                  items={workspaceOrder}
                  strategy={verticalListSortingStrategy}
                >
                  {workspaceOrder.map((name, idx) => {
                    const ws = workspaces.find((w) => w.name === name);
                    if (!ws) return null;
                    return (
                      <SortableWorkspaceEntry
                        key={ws.name}
                        ws={ws}
                        isEditing={editingWorkspace === ws.name}
                        draftName={draftName}
                        isSaving={isSaving}
                        renameError={renameError}
                        renameInputRef={renameInputRef}
                        onDraftChange={setDraftName}
                        onSaveRename={handleSaveRename}
                        onRenameKeyDown={handleRenameKeyDown}
                        onContextMenu={handleContextMenu}
                        onOverflowClick={handleOverflowClick}
                        onMoveUp={
                          idx > 0 ? () => handleMoveUp(name) : undefined
                        }
                        onMoveDown={
                          idx < workspaceOrder.length - 1
                            ? () => handleMoveDown(name)
                            : undefined
                        }
                      />
                    );
                  })}
                </SortableContext>
              </DndContext>
            </div>
          )}

          {repos.length > 0 && (
            <div
              className={styles.repoList}
              role="radiogroup"
              aria-label="Workspace selection"
            >
              {/* All Workspaces option */}
              <button
                type="button"
                className={styles.repoItem}
                onClick={() => onWorkspaceSelect?.(null)}
                role="radio"
                aria-checked={
                  activeRepoName === null || activeRepoName === undefined
                }
              >
                <span
                  className={styles.radioIndicator}
                  data-active={
                    activeRepoName === null || activeRepoName === undefined
                  }
                />
                <svg
                  className={styles.allWorkspacesIcon}
                  viewBox="0 0 16 16"
                  width="14"
                  height="14"
                >
                  <rect
                    x="1"
                    y="4"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                  <rect
                    x="5"
                    y="1"
                    width="10"
                    height="8"
                    rx="1.5"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.3"
                  />
                </svg>
                <span className={styles.allWorkspacesLabel}>
                  All Workspaces
                </span>
                <span
                  className={styles.agentCount}
                  data-active={totalActiveCount > 0}
                >
                  {totalActiveCount}
                </span>
              </button>

              {repos.map((repo) => {
                const agentCount = repoAgentCounts.get(repo.name) ?? 0;
                const isActive = agentCount > 0;
                const isSelected = activeRepoName === repo.name;

                return (
                  <button
                    key={repo.name}
                    type="button"
                    className={styles.repoItem}
                    onClick={() => onWorkspaceSelect?.(repo.name)}
                    title={repo.path}
                    role="radio"
                    aria-checked={isSelected}
                  >
                    <span
                      className={styles.radioIndicator}
                      data-active={isSelected}
                    />
                    <span className={styles.repoIcon}>
                      <svg
                        width="16"
                        height="16"
                        viewBox="0 0 16 16"
                        fill="none"
                        xmlns="http://www.w3.org/2000/svg"
                      >
                        <path
                          d="M1.5 2.5C1.5 1.95 1.95 1.5 2.5 1.5H6.29L8.29 3.5H13.5C14.05 3.5 14.5 3.95 14.5 4.5V12.5C14.5 13.05 14.05 13.5 13.5 13.5H2.5C1.95 13.5 1.5 13.05 1.5 12.5V2.5Z"
                          fill="currentColor"
                        />
                      </svg>
                    </span>
                    <span className={styles.repoName}>{repo.name}</span>
                    <span className={styles.repoMeta}>
                      {isActive && (
                        <span
                          className={styles.agentCount}
                          data-active={isActive}
                        >
                          {agentCount}
                        </span>
                      )}
                      <span
                        className={styles.statusDot}
                        data-active={isActive}
                      />
                    </span>
                  </button>
                );
              })}
            </div>
          )}
        </div>
      )}

      {isCollapsed && totalActiveCount > 0 && (
        <div
          className={styles.collapsedBadge}
          title={`${totalActiveCount} active agent(s)`}
        >
          {totalActiveCount}
        </div>
      )}

      <WorkspaceContextMenu
        isOpen={contextMenu !== null}
        position={contextMenu ?? { x: 0, y: 0 }}
        onRename={handleStartRename}
        onRemove={handleStartRemove}
        onClose={handleCloseContextMenu}
      />

      <ConfirmDialog
        isOpen={confirmDeleteOpen}
        title="Remove workspace"
        message={`Are you sure you want to remove "${pendingDeleteName}"? Git worktrees will be kept on disk.`}
        confirmLabel="Remove"
        variant="danger"
        onConfirm={handleConfirmDelete}
        onCancel={handleCancelDelete}
      />
    </aside>
  );
}
