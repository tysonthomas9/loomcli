/**
 * useWorkspaceManagement — rename + remove (with undo) + context-menu state for
 * the WorkspaceSwitcher overlay. Extracted from the (now-removed) sidebar
 * OtherWorkspacesSection so the same proven mutation flow drives the switcher.
 *
 * Everything is keyed by workspace **id** (immutable), not name, so rename is
 * safe. `refetch`/`showToast` are sourced from context, which keeps both
 * switcher mount points (sidebar selector + global Cmd/Ctrl+K) working without
 * threading extra props.
 */

import {
  useState,
  useRef,
  useCallback,
  useEffect,
  type MouseEvent as ReactMouseEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  type RefObject,
} from "react";

import { renameWorkspace, deleteWorkspace } from "@/hooks/api";
import { useToast, useWorkspaceContext } from "@/hooks";

/** Grace period before a queued workspace deletion is committed (undo window). */
const DELETE_UNDO_MS = 5000;

interface ContextMenuState {
  workspaceId: string;
  workspaceName: string;
  x: number;
  y: number;
}

interface WorkspaceRef {
  id: string;
  name: string;
}

export interface UseWorkspaceManagementResult {
  // Context menu
  contextMenu: ContextMenuState | null;
  openContextMenu: (e: ReactMouseEvent, ws: WorkspaceRef) => void;
  closeContextMenu: () => void;

  // Rename
  editing: WorkspaceRef | null;
  draftName: string;
  setDraftName: (value: string) => void;
  isSaving: boolean;
  renameError: string | null;
  renameInputRef: RefObject<HTMLInputElement>;
  startRename: () => void;
  cancelRename: () => void;
  saveRename: () => Promise<void>;
  handleRenameKeyDown: (e: ReactKeyboardEvent<HTMLInputElement>) => void;

  // Remove (with undo)
  confirmDeleteOpen: boolean;
  pendingDeleteName: string | null;
  startRemove: () => void;
  cancelDelete: () => void;
  confirmDelete: () => void;
}

export function useWorkspaceManagement(): UseWorkspaceManagementResult {
  const { showToast } = useToast();
  const { refetch } = useWorkspaceContext();

  // ---- Context menu -------------------------------------------------------
  const [contextMenu, setContextMenu] = useState<ContextMenuState | null>(null);

  const openContextMenu = useCallback(
    (e: ReactMouseEvent, ws: WorkspaceRef) => {
      e.preventDefault();
      e.stopPropagation();
      setContextMenu({
        workspaceId: ws.id,
        workspaceName: ws.name,
        x: e.clientX,
        y: e.clientY,
      });
    },
    [],
  );

  const closeContextMenu = useCallback(() => setContextMenu(null), []);

  // ---- Rename -------------------------------------------------------------
  const [editing, setEditing] = useState<WorkspaceRef | null>(null);
  const [draftName, setDraftName] = useState("");
  const [renameError, setRenameError] = useState<string | null>(null);
  const [isSaving, setIsSaving] = useState(false);
  const renameInputRef = useRef<HTMLInputElement>(null);

  // Focus + select the rename input when entering edit mode.
  useEffect(() => {
    if (editing && renameInputRef.current) {
      renameInputRef.current.focus();
      renameInputRef.current.select();
    }
  }, [editing]);

  const startRename = useCallback(() => {
    if (!contextMenu) return;
    setEditing({
      id: contextMenu.workspaceId,
      name: contextMenu.workspaceName,
    });
    setDraftName(contextMenu.workspaceName);
    setRenameError(null);
  }, [contextMenu]);

  const cancelRename = useCallback(() => {
    setEditing(null);
    setDraftName("");
    setRenameError(null);
  }, []);

  const saveRename = useCallback(async () => {
    if (!editing) return;
    const trimmed = draftName.trim();
    if (!trimmed) {
      setRenameError("Name cannot be empty");
      setTimeout(() => renameInputRef.current?.focus(), 0);
      return;
    }
    if (trimmed === editing.name) {
      setEditing(null);
      return;
    }
    setIsSaving(true);
    setRenameError(null);
    try {
      await renameWorkspace(editing.id, trimmed);
      setEditing(null);
      refetch();
    } catch (err) {
      const message =
        err instanceof Error ? err.message : "Failed to rename workspace";
      setRenameError(message);
      setTimeout(() => renameInputRef.current?.focus(), 0);
    } finally {
      setIsSaving(false);
    }
  }, [editing, draftName, refetch]);

  const handleRenameKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLInputElement>) => {
      if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        void saveRename();
      } else if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        cancelRename();
      }
    },
    [saveRename, cancelRename],
  );

  // ---- Remove (with undo) -------------------------------------------------
  const [confirmDeleteOpen, setConfirmDeleteOpen] = useState(false);
  const [pendingDelete, setPendingDelete] = useState<WorkspaceRef | null>(null);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deletionPendingRef = useRef(false);

  // Clear any pending delete timer on unmount.
  useEffect(() => {
    return () => {
      if (deleteTimerRef.current) clearTimeout(deleteTimerRef.current);
    };
  }, []);

  const startRemove = useCallback(() => {
    if (!contextMenu) return;
    setPendingDelete({
      id: contextMenu.workspaceId,
      name: contextMenu.workspaceName,
    });
    setConfirmDeleteOpen(true);
  }, [contextMenu]);

  const cancelDelete = useCallback(() => {
    setConfirmDeleteOpen(false);
    setPendingDelete(null);
  }, []);

  const confirmDelete = useCallback(() => {
    if (!pendingDelete) return;
    if (deletionPendingRef.current) return;
    const { id: idToDelete, name: nameToDelete } = pendingDelete;
    setConfirmDeleteOpen(false);
    setPendingDelete(null);
    deletionPendingRef.current = true;

    deleteTimerRef.current = setTimeout(async () => {
      deleteTimerRef.current = null;
      try {
        await deleteWorkspace(idToDelete);
        refetch();
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove workspace";
        showToast(message, { type: "error" });
        refetch();
      } finally {
        deletionPendingRef.current = false;
      }
    }, DELETE_UNDO_MS);

    showToast(`Workspace "${nameToDelete}" removed`, {
      type: "success",
      duration: DELETE_UNDO_MS,
      onUndo: () => {
        if (deleteTimerRef.current) {
          clearTimeout(deleteTimerRef.current);
          deleteTimerRef.current = null;
          deletionPendingRef.current = false;
          showToast(`Workspace "${nameToDelete}" restored`, { type: "info" });
          refetch();
          return;
        }
        showToast("Deletion already in progress", { type: "info" });
      },
    });
  }, [pendingDelete, refetch, showToast]);

  return {
    contextMenu,
    openContextMenu,
    closeContextMenu,
    editing,
    draftName,
    setDraftName,
    isSaving,
    renameError,
    renameInputRef,
    startRename,
    cancelRename,
    saveRename,
    handleRenameKeyDown,
    confirmDeleteOpen,
    pendingDeleteName: pendingDelete?.name ?? null,
    startRemove,
    cancelDelete,
    confirmDelete,
  };
}
