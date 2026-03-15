/**
 * ConfirmDialog component.
 * Reusable confirmation modal dialog with cancel/confirm actions.
 * Renders via React portal above all other content.
 */

import { useEffect, useRef, useCallback } from "react";
import { createPortal } from "react-dom";

import { useRegisterEscapeLayer, LAYER_CONFIRM_DIALOG } from "@/hooks";
import styles from "./ConfirmDialog.module.css";

export interface ConfirmDialogProps {
  isOpen: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  variant?: "danger" | "default";
  onConfirm: () => void;
  onCancel: () => void;
}

export function ConfirmDialog({
  isOpen,
  title,
  message,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  variant = "default",
  onConfirm,
  onCancel,
}: ConfirmDialogProps): JSX.Element | null {
  const cancelRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Auto-focus cancel button on open
  useEffect(() => {
    if (isOpen && cancelRef.current) {
      cancelRef.current.focus();
    }
  }, [isOpen]);

  // Escape key to cancel via global shortcut layer system (highest priority)
  useRegisterEscapeLayer(LAYER_CONFIRM_DIALOG, onCancel, isOpen);

  // Focus trap: keep Tab cycling between the two buttons
  const handleKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key !== "Tab" || !dialogRef.current) return;

    const focusable = dialogRef.current.querySelectorAll<HTMLElement>(
      'button:not([disabled]), [tabindex]:not([tabindex="-1"])',
    );
    if (focusable.length === 0) return;

    const first = focusable[0] as HTMLElement | undefined;
    const last = focusable[focusable.length - 1] as HTMLElement | undefined;
    if (!first || !last) return;

    if (e.shiftKey) {
      if (document.activeElement === first) {
        e.preventDefault();
        last.focus();
      }
    } else {
      if (document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  }, []);

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onCancel}
      data-testid="confirm-dialog-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="alertdialog"
        aria-modal="true"
        aria-label={title}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
      >
        <h2 className={styles.title}>{title}</h2>
        <p className={styles.message}>{message}</p>
        <div className={styles.actions}>
          <button
            ref={cancelRef}
            type="button"
            className={styles.cancelButton}
            onClick={onCancel}
            data-testid="confirm-dialog-cancel"
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={`${styles.confirmButton} ${variant === "danger" ? styles.confirmDanger : ""}`}
            onClick={onConfirm}
            data-testid="confirm-dialog-confirm"
          >
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
