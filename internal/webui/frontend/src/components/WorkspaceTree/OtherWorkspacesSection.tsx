/**
 * OtherWorkspacesSection renders the non-active workspaces at the bottom
 * of the sidebar. Supports drag-and-drop reorder, inline rename, delete
 * with undo, and context menu actions.
 */

import { useState, useCallback, useEffect, useRef, useMemo } from "react";

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
} from "@/hooks/api";
import type { WorkspaceSummary } from "@/api/workspace";
import { useToast } from "@/hooks";
import { ConfirmDialog } from "@/components/ConfirmDialog";

import { SortableWorkspaceEntry } from "./nav/SortableWorkspaceEntry";
import { WorkspaceContextMenu } from "./menus/WorkspaceContextMenu";
import styles from "./OtherWorkspacesSection.module.css";

export interface OtherWorkspacesSectionProps {
  /** All workspace summaries (including active) for display ordering */
  workspaces: WorkspaceSummary[];
  /** Name of the active workspace (excluded from the rendered list) */
  activeWorkspaceName: string | null;
  /** Called when user clicks a workspace to switch to it */
  onWorkspaceSwitch?: ((workspaceName: string) => void) | undefined;
  /** Called after mutations to refresh workspace data */
  refetchWorkspaces: () => void;
}

export function OtherWorkspacesSection({
  workspaces,
  activeWorkspaceName,
  onWorkspaceSwitch,
  refetchWorkspaces,
}: OtherWorkspacesSectionProps): JSX.Element | null {
  const { showToast } = useToast();

  // Filter to non-active workspaces (memoized for stable dep)
  const otherWorkspaces = useMemo(
    () => workspaces.filter((w) => w.name !== activeWorkspaceName),
    [workspaces, activeWorkspaceName],
  );

  // Workspace ordering state for drag-and-drop reorder
  const [workspaceOrder, setWorkspaceOrder] = useState<string[]>([]);

  // Initialize/sync order from prop data
  useEffect(() => {
    setWorkspaceOrder(otherWorkspaces.map((ws) => ws.name));
  }, [otherWorkspaces]);

  // DnD sensors with 5px activation distance to prevent accidental drags
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  // Re-fetch order from server on error via parent-provided refetch
  const rollbackOrder = useCallback(() => {
    refetchWorkspaces();
  }, [refetchWorkspaces]);

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

  // Alt+Up/Down keyboard reorder
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

  // Workspace delete state
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [pendingDeleteName, setPendingDeleteName] = useState<string | null>(
    null,
  );
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deletionPendingRef = useRef(false);

  // Workspace summaries for resolving name→ID
  const wsIdByName = useCallback(
    (name: string) => workspaces.find((w) => w.name === name)?.id ?? "",
    [workspaces],
  );

  // Focus rename input when entering edit mode
  useEffect(() => {
    if (editingWorkspace && renameInputRef.current) {
      renameInputRef.current.focus();
      renameInputRef.current.select();
    }
  }, [editingWorkspace]);

  // Cleanup delete timer on unmount
  useEffect(() => {
    return () => {
      if (deleteTimerRef.current) {
        clearTimeout(deleteTimerRef.current);
      }
    };
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
    if (!trimmed) {
      setRenameError("Name cannot be empty");
      setTimeout(() => renameInputRef.current?.focus(), 0);
      return;
    }
    if (trimmed === editingWorkspace) {
      setEditingWorkspace(null);
      return;
    }
    setIsSaving(true);
    setRenameError(null);
    try {
      await renameWorkspace(wsIdByName(editingWorkspace), trimmed);
      setEditingWorkspace(null);
      refetchWorkspaces();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to rename workspace";
      setRenameError(message);
      setTimeout(() => renameInputRef.current?.focus(), 0);
    } finally {
      setIsSaving(false);
    }
  }, [editingWorkspace, draftName, refetchWorkspaces, wsIdByName]);

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
    if (deletionPendingRef.current) return;
    const nameToDelete = pendingDeleteName;
    const idToDelete = wsIdByName(pendingDeleteName);
    setConfirmDeleteOpen(false);
    setPendingDeleteName(null);
    deletionPendingRef.current = true;

    deleteTimerRef.current = setTimeout(async () => {
      deleteTimerRef.current = null;
      try {
        await deleteWorkspace(idToDelete);
        refetchWorkspaces();
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove workspace";
        showToast(message, { type: "error" });
        refetchWorkspaces();
      } finally {
        deletionPendingRef.current = false;
      }
    }, 5000);

    showToast(`Workspace "${nameToDelete}" removed`, {
      type: "success",
      duration: 5000,
      onUndo: () => {
        if (deleteTimerRef.current) {
          clearTimeout(deleteTimerRef.current);
          deleteTimerRef.current = null;
          deletionPendingRef.current = false;
          showToast(`Workspace "${nameToDelete}" restored`, { type: "info" });
          refetchWorkspaces();
          return;
        }
        showToast("Deletion already in progress", { type: "info" });
      },
    });
  }, [pendingDeleteName, refetchWorkspaces, showToast, wsIdByName]);

  if (otherWorkspaces.length === 0) return null;

  return (
    <>
      <div className={styles.section}>
        <div className={styles.header}>Workspaces</div>
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
              const ws = otherWorkspaces.find((w) => w.name === name);
              if (!ws) return null;
              return (
                <SortableWorkspaceEntry
                  key={ws.name}
                  ws={ws}
                  isActive={false}
                  isEditing={editingWorkspace === ws.name}
                  draftName={draftName}
                  isSaving={isSaving}
                  renameError={renameError}
                  renameInputRef={renameInputRef}
                  {...(onWorkspaceSwitch ? { onClick: onWorkspaceSwitch } : {})}
                  onDraftChange={setDraftName}
                  onSaveRename={handleSaveRename}
                  onRenameKeyDown={handleRenameKeyDown}
                  onContextMenu={handleContextMenu}
                  onOverflowClick={handleOverflowClick}
                  onMoveUp={idx > 0 ? () => handleMoveUp(name) : undefined}
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
    </>
  );
}
