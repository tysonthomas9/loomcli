/**
 * useWorkspaceManagementActions — shared rename/delete/default logic for any
 * workspace surface (sidebar, Cmd+K overlay, etc.). Encapsulates the
 * toast-with-undo pattern, ConfirmDialog state, and active-workspace
 * navigation so both call sites stay in sync.
 *
 * The hook returns the pending-delete state plus confirm/cancel handlers;
 * callers render their own ConfirmDialog. This keeps the hook in the hooks
 * layer (which is forbidden from depending on components per the frontend
 * boundaries DAG) while still co-locating the delete orchestration.
 */

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";

import { renameWorkspace, deleteWorkspace } from "@/hooks/api";
import { buildWorkspaceSwitchUrl } from "@/utils/workspaceUrl";

import { useToast } from "../ui";
import { useWorkspaceContext } from "./useWorkspaceContext";

export interface PendingWorkspaceDelete {
  id: string;
  name: string;
}

export interface UseWorkspaceManagementActionsReturn {
  onRename: (wsId: string, newName: string) => Promise<void>;
  onDelete: (wsId: string, wsName: string) => void;
  onSetDefault: (wsName: string) => void;
  onClearDefault: () => void;
  defaultWorkspaceId: string | undefined;
  /** Currently-pending delete; non-null while the ConfirmDialog should show. */
  pendingDelete: PendingWorkspaceDelete | null;
  /** Confirms the pending delete: shows toast-with-undo + 5s deferred API call. */
  onConfirmDelete: () => void;
  /** Cancels the pending delete and clears state. */
  onCancelDelete: () => void;
}

export function useWorkspaceManagementActions(): UseWorkspaceManagementActionsReturn {
  const {
    workspace,
    workspaceId,
    refetch,
    defaultWorkspaceName,
    setDefaultWorkspace,
  } = useWorkspaceContext();
  const { showToast } = useToast();
  const navigate = useNavigate();

  const [pendingDelete, setPendingDelete] =
    useState<PendingWorkspaceDelete | null>(null);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const deletionPendingRef = useRef(false);

  // Cleanup any in-flight delete timer on unmount
  useEffect(() => {
    return () => {
      if (deleteTimerRef.current) {
        clearTimeout(deleteTimerRef.current);
      }
    };
  }, []);

  const workspaces = useMemo(
    () => workspace?.workspaces ?? [],
    [workspace?.workspaces],
  );
  const defaultWorkspaceId = defaultWorkspaceName
    ? workspaces.find((w) => w.name === defaultWorkspaceName)?.id
    : undefined;

  const handleRename = useCallback(
    async (wsId: string, newName: string): Promise<void> => {
      await renameWorkspace(wsId, newName);
      refetch();
    },
    [refetch],
  );

  const handleDeleteRequest = useCallback((wsId: string, wsName: string) => {
    setPendingDelete({ id: wsId, name: wsName });
  }, []);

  const handleSetDefault = useCallback(
    (wsName: string) => {
      setDefaultWorkspace(wsName).catch((err: unknown) => {
        const message =
          err instanceof Error ? err.message : "Failed to set default";
        showToast(message, { type: "error" });
      });
    },
    [setDefaultWorkspace, showToast],
  );

  const handleClearDefault = useCallback(() => {
    setDefaultWorkspace(null).catch((err: unknown) => {
      const message =
        err instanceof Error ? err.message : "Failed to clear default";
      showToast(message, { type: "error" });
    });
  }, [setDefaultWorkspace, showToast]);

  const handleConfirmDelete = useCallback(() => {
    if (!pendingDelete) return;
    if (deletionPendingRef.current) return;
    const { id: idToDelete, name: nameToDelete } = pendingDelete;
    setPendingDelete(null);
    deletionPendingRef.current = true;

    // Resolve next workspace BEFORE the timer fires; the workspace list may
    // re-render in the meantime but the user's intent (where to land after
    // deleting the active workspace) was set when they confirmed.
    const isActive = idToDelete === workspaceId;
    const nextWs = isActive
      ? workspaces.find((w) => w.id !== idToDelete)
      : undefined;

    deleteTimerRef.current = setTimeout(async () => {
      deleteTimerRef.current = null;
      try {
        await deleteWorkspace(idToDelete);
        refetch();
        if (isActive && nextWs) {
          navigate(buildWorkspaceSwitchUrl(nextWs.id), { flushSync: true });
        }
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to remove workspace";
        showToast(message, { type: "error" });
        refetch();
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
          refetch();
          return;
        }
        showToast("Deletion already in progress", { type: "info" });
      },
    });
  }, [pendingDelete, workspaceId, workspaces, refetch, navigate, showToast]);

  const handleCancelDelete = useCallback(() => {
    setPendingDelete(null);
  }, []);

  return {
    onRename: handleRename,
    onDelete: handleDeleteRequest,
    onSetDefault: handleSetDefault,
    onClearDefault: handleClearDefault,
    defaultWorkspaceId,
    pendingDelete,
    onConfirmDelete: handleConfirmDelete,
    onCancelDelete: handleCancelDelete,
  };
}
