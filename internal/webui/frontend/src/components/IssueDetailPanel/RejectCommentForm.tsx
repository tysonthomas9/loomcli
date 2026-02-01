/**
 * RejectCommentForm component for IssueDetailPanel.
 * Inline form for collecting feedback when rejecting an issue from the side panel.
 */

import { useState, useRef, useCallback, type FormEvent, type KeyboardEvent } from 'react';
import styles from './RejectCommentForm.module.css';

/**
 * Props for the RejectCommentForm component.
 */
export interface RejectCommentFormProps {
  /** Issue ID being rejected (for context) */
  issueId: string;
  /** Callback when form is submitted with comment text */
  onSubmit: (text: string) => void;
  /** Callback when form is cancelled */
  onCancel: () => void;
  /** Whether the form is currently submitting */
  isSubmitting?: boolean;
  /** Error message to display */
  error?: string | null;
}

/**
 * RejectCommentForm provides an inline form for entering rejection feedback in the detail panel.
 * Features:
 * - Cmd/Ctrl+Enter keyboard shortcut to submit
 * - Escape key to cancel
 * - Submit disabled until text is entered
 * - Auto-focus on mount
 */
export function RejectCommentForm({
  issueId,
  onSubmit,
  onCancel,
  isSubmitting = false,
  error = null,
}: RejectCommentFormProps): JSX.Element {
  const [text, setText] = useState('');
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const handleSubmit = useCallback(
    (e?: FormEvent) => {
      e?.preventDefault();
      e?.stopPropagation();

      const trimmedText = text.trim();
      if (!trimmedText || isSubmitting) return;

      onSubmit(trimmedText);
    },
    [text, isSubmitting, onSubmit]
  );

  const handleCancel = useCallback(
    (e?: React.MouseEvent) => {
      e?.preventDefault();
      e?.stopPropagation();
      onCancel();
    },
    [onCancel]
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      // Cmd/Ctrl+Enter to submit
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        e.stopPropagation();
        handleSubmit();
      }
      // Escape to cancel
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        handleCancel();
      }
    },
    [handleSubmit, handleCancel]
  );

  const handleTextChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value);
  }, []);

  const canSubmit = text.trim().length > 0 && !isSubmitting;

  return (
    <form
      className={styles.rejectForm}
      onSubmit={handleSubmit}
      data-testid="reject-comment-form"
      aria-label={`Rejection feedback for issue ${issueId}`}
    >
      <label className={styles.label} htmlFor={`panel-reject-textarea-${issueId}`}>
        Why are you rejecting this?
      </label>
      <textarea
        ref={textareaRef}
        id={`panel-reject-textarea-${issueId}`}
        className={styles.textarea}
        value={text}
        onChange={handleTextChange}
        onKeyDown={handleKeyDown}
        placeholder="Enter feedback..."
        rows={3}
        disabled={isSubmitting}
        autoFocus
        data-testid="reject-textarea"
      />
      {error && (
        <div className={styles.error} role="alert" data-testid="reject-error">
          {error}
        </div>
      )}
      <div className={styles.actions}>
        <button
          type="button"
          className={styles.cancelButton}
          onClick={handleCancel}
          disabled={isSubmitting}
          data-testid="reject-cancel"
        >
          Cancel
        </button>
        <button
          type="submit"
          className={styles.submitButton}
          disabled={!canSubmit}
          data-testid="reject-submit"
        >
          {isSubmitting ? 'Submitting...' : 'Reject'}
        </button>
      </div>
    </form>
  );
}
