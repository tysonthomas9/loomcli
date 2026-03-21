/**
 * PasteConfirmDialog component.
 * Modal dialog shown when pasting multi-line text into the terminal.
 * Shows a preview of the text and asks user to confirm before sending.
 */

import { useEffect, useRef, useCallback } from "react";

import styles from "./PasteConfirmDialog.module.css";

const MAX_PREVIEW_LINES = 10;

interface PasteConfirmDialogProps {
  isOpen: boolean;
  text: string;
  onConfirm: () => void;
  onCancel: () => void;
}

export function PasteConfirmDialog({
  isOpen,
  text,
  onConfirm,
  onCancel,
}: PasteConfirmDialogProps): JSX.Element | null {
  const confirmRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (isOpen) {
      confirmRef.current?.focus();
    }
  }, [isOpen]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        onCancel();
      } else if (e.key === "Enter") {
        e.preventDefault();
        onConfirm();
      }
    },
    [onConfirm, onCancel],
  );

  if (!isOpen) return null;

  const lines = text.split("\n");
  // Drop trailing empty line from a final newline
  const meaningfulLines =
    lines.length > 1 && lines[lines.length - 1] === ""
      ? lines.slice(0, -1)
      : lines;
  const totalLines = meaningfulLines.length;
  const previewLines = meaningfulLines.slice(0, MAX_PREVIEW_LINES);
  const remainingLines = totalLines - MAX_PREVIEW_LINES;

  return (
    <div className={styles.overlay} onClick={onCancel}>
      <div
        className={styles.dialog}
        onClick={(e) => e.stopPropagation()}
        onKeyDown={handleKeyDown}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="paste-dialog-title"
        aria-describedby="paste-dialog-desc"
        tabIndex={-1}
      >
        <div className={styles.header}>
          <h3 id="paste-dialog-title" className={styles.title}>
            Paste {totalLines} lines?
          </h3>
          <p id="paste-dialog-desc" className={styles.subtitle}>
            You are about to paste multi-line text into the terminal.
          </p>
        </div>
        <div className={styles.preview}>
          <pre className={styles.previewText}>{previewLines.join("\n")}</pre>
          {remainingLines > 0 && (
            <div className={styles.truncated}>
              ... and {remainingLines} more line
              {remainingLines !== 1 ? "s" : ""}
            </div>
          )}
        </div>
        <div className={styles.footer}>
          <button
            type="button"
            className={styles.buttonSecondary}
            onClick={onCancel}
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.buttonPrimary}
            onClick={onConfirm}
            ref={confirmRef}
          >
            Paste
          </button>
        </div>
      </div>
    </div>
  );
}
