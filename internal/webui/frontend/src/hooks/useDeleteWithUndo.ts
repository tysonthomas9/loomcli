/**
 * useDeleteWithUndo hook.
 * Manages a delete-with-undo flow: schedule deletion after a delay,
 * allow cancellation via undo. Guards against double-delete and
 * properly tracks pending state.
 */

import { useRef, useCallback, useEffect } from "react";

export interface UseDeleteWithUndoOptions {
  /** Delay in ms before deletion is executed (default: 5000) */
  delay?: number;
  /** Callback to perform the actual deletion */
  onDelete: (name: string) => Promise<void>;
  /** Callback to show a toast (returns an object with undo callback) */
  onShowToast?: (message: string, options?: { onUndo?: () => void }) => void;
  /** Callback to show an info toast */
  onShowInfoToast?: (message: string) => void;
  /** Callback to show an error toast */
  onShowErrorToast?: (message: string) => void;
  /** Callback on successful deletion */
  onSuccess?: () => void;
}

export interface UseDeleteWithUndoReturn {
  /** Initiate a delete with undo flow */
  handleConfirmDelete: (name: string) => void;
  /** Whether a deletion is currently pending or in progress */
  isPending: () => boolean;
}

/**
 * Hook providing a delete-with-undo pattern.
 * - Sets deletionPending=true when deletion is scheduled (not false).
 * - Guards against rapid double-clicks through the confirm dialog.
 * - Nulls deleteTimerRef inside the setTimeout callback.
 * - Resets deletionPending after completion (success or error).
 */
export function useDeleteWithUndo({
  delay = 5000,
  onDelete,
  onShowToast,
  onShowInfoToast,
  onShowErrorToast,
  onSuccess,
}: UseDeleteWithUndoOptions): UseDeleteWithUndoReturn {
  const deletionPendingRef = useRef(false);
  const deleteTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (deleteTimerRef.current !== null) {
        clearTimeout(deleteTimerRef.current);
        deleteTimerRef.current = null;
      }
    };
  }, []);

  const handleConfirmDelete = useCallback(
    (name: string) => {
      if (deletionPendingRef.current) return;

      deletionPendingRef.current = true;

      deleteTimerRef.current = setTimeout(async () => {
        deleteTimerRef.current = null;

        try {
          await onDelete(name);
          onSuccess?.();
        } catch (err) {
          const message =
            err instanceof Error ? err.message : "Failed to delete";
          onShowErrorToast?.(message);
        } finally {
          deletionPendingRef.current = false;
        }
      }, delay);

      onShowToast?.(`"${name}" will be deleted`, {
        onUndo: () => {
          if (deleteTimerRef.current !== null) {
            clearTimeout(deleteTimerRef.current);
            deleteTimerRef.current = null;
            deletionPendingRef.current = false;
            onShowInfoToast?.(`"${name}" restored`);
          } else {
            onShowInfoToast?.("Deletion already in progress");
          }
        },
      });
    },
    [delay, onDelete, onShowToast, onShowInfoToast, onShowErrorToast, onSuccess],
  );

  const isPending = useCallback(() => deletionPendingRef.current, []);

  return { handleConfirmDelete, isPending };
}
