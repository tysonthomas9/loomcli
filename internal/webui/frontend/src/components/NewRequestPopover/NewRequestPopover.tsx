import {
  useState,
  useRef,
  useCallback,
  useEffect,
  type KeyboardEvent,
} from "react";
import { createPortal } from "react-dom";

import { useRegisterEscapeLayer, LAYER_MODAL } from "@/hooks";
import { useFocusTrap } from "@/hooks/useFocusTrap";
import { useFocusReturn } from "@/hooks/useFocusReturn";

import styles from "./NewRequestPopover.module.css";

export interface NewRequestPopoverProps {
  /** Controlled open state. */
  isOpen: boolean;
  /** Close handler (Cancel button, Escape, overlay click). */
  onClose: () => void;
  /**
   * Submit handler. Resolves on success; rejects with an Error to surface the
   * message inline and keep the modal open with the text intact.
   */
  onSubmit: (text: string) => Promise<void>;
  /**
   * When true, submit is disabled even if the textarea has content. Use this to
   * block submission while dependencies (e.g. backend config) are still loading.
   */
  disabled?: boolean;
}

/**
 * Centered modal dialog with a multi-line textarea for a free-text agent
 * request. Enter submits, Shift+Enter inserts a newline, Escape cancels.
 * On submit failure, shows an inline error and keeps the text so the user can
 * retry.
 *
 * Structurally mirrors the old `CreateIssueModal`: rendered via `createPortal`
 * into `document.body`, with a fixed backdrop and a centered dialog card.
 */
export function NewRequestPopover({
  isOpen,
  onClose,
  onSubmit,
  disabled = false,
}: NewRequestPopoverProps): JSX.Element | null {
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const dialogRef = useRef<HTMLDivElement>(null);

  // Reset state when opening.
  useEffect(() => {
    if (isOpen) {
      setText("");
      setError("");
      setIsSubmitting(false);
    }
  }, [isOpen]);

  // Escape layering + focus trap + focus return — matches CreateWorkspaceModal
  // and other project dialogs.
  useRegisterEscapeLayer(LAYER_MODAL, onClose, isOpen);
  useFocusTrap(dialogRef, isOpen, { initialFocus: textareaRef });
  useFocusReturn(isOpen);

  const canSubmit = text.trim() !== "" && !isSubmitting && !disabled;

  const handleSubmit = useCallback(async () => {
    const trimmed = text.trim();
    if (!trimmed || isSubmitting || disabled) return;
    setIsSubmitting(true);
    setError("");
    try {
      await onSubmit(trimmed);
      setText("");
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : "Failed to start agent");
    } finally {
      setIsSubmitting(false);
    }
  }, [text, isSubmitting, disabled, onSubmit]);

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        void handleSubmit();
      }
      // Escape is handled by useRegisterEscapeLayer, not here.
    },
    [handleSubmit],
  );

  if (!isOpen) return null;

  return createPortal(
    <div
      className={styles.overlay}
      onClick={onClose}
      data-testid="new-request-overlay"
    >
      <div
        ref={dialogRef}
        className={styles.dialog}
        role="dialog"
        aria-modal="true"
        aria-label="Ask the agent"
        onClick={(e) => e.stopPropagation()}
        data-testid="new-request-dialog"
      >
        <h2 className={styles.title}>New Issue</h2>
        <textarea
          ref={textareaRef}
          className={styles.textarea}
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Ask the agent... (Enter to send, Shift+Enter for newline)"
          disabled={isSubmitting}
          rows={4}
          data-testid="new-request-textarea"
        />
        {error && (
          <p className={styles.error} data-testid="new-request-error">
            {error}
          </p>
        )}
        <div className={styles.actions}>
          <button
            type="button"
            className={styles.cancelButton}
            onClick={onClose}
            disabled={isSubmitting}
            data-testid="new-request-cancel"
          >
            Cancel
          </button>
          <button
            type="button"
            className={styles.submitButton}
            onClick={() => void handleSubmit()}
            disabled={!canSubmit}
            data-testid="new-request-submit"
          >
            {isSubmitting ? "Starting..." : "Go"}
          </button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
