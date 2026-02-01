/**
 * CommentForm component.
 * Allows users to add new comments to an issue.
 */

import { useState, useRef, useCallback, type FormEvent, type KeyboardEvent } from 'react';
import type { Comment } from '@/types';
import { addComment } from '@/api';
import styles from './CommentForm.module.css';

/**
 * Props for the CommentForm component.
 */
export interface CommentFormProps {
  /** Issue ID to add comment to */
  issueId: string;
  /** Callback when comment is successfully added */
  onCommentAdded: (comment: Comment) => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * CommentForm provides a textarea and submit button for adding comments.
 * Features:
 * - Cmd/Ctrl+Enter keyboard shortcut to submit
 * - Disabled state while submitting
 * - Error display with retry capability
 * - Clears form on successful submission
 */
export function CommentForm({
  issueId,
  onCommentAdded,
  className,
}: CommentFormProps): JSX.Element {
  const [text, setText] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  const handleSubmit = useCallback(async (e?: FormEvent) => {
    e?.preventDefault();

    const trimmedText = text.trim();
    if (!trimmedText || isSubmitting) return;

    setError(null);
    setIsSubmitting(true);

    try {
      const newComment = await addComment(issueId, trimmedText);
      setText('');
      onCommentAdded(newComment);
      // Keep focus in textarea for follow-up comments
      textareaRef.current?.focus();
    } catch (err) {
      const message = err instanceof Error ? err.message : 'Failed to add comment';
      setError(message);
    } finally {
      setIsSubmitting(false);
    }
  }, [text, isSubmitting, issueId, onCommentAdded]);

  const handleKeyDown = useCallback((e: KeyboardEvent<HTMLTextAreaElement>) => {
    // Cmd/Ctrl+Enter to submit
    if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
      e.preventDefault();
      handleSubmit();
    }
  }, [handleSubmit]);

  const handleTextChange = useCallback((e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value);
    // Clear error when user types
    if (error) {
      setError(null);
    }
  }, [error]);

  const rootClassName = [styles.commentForm, className].filter(Boolean).join(' ');
  const canSubmit = text.trim().length > 0 && !isSubmitting;

  return (
    <form className={rootClassName} onSubmit={handleSubmit} data-testid="comment-form">
      <textarea
        ref={textareaRef}
        className={styles.textarea}
        value={text}
        onChange={handleTextChange}
        onKeyDown={handleKeyDown}
        placeholder="Add a comment..."
        disabled={isSubmitting}
        aria-label="Add a comment"
        data-testid="comment-textarea"
      />
      {error && (
        <div className={styles.error} role="alert" data-testid="comment-error">
          {error}
        </div>
      )}
      <div className={styles.actions}>
        <span className={styles.hint}>Cmd+Enter to submit</span>
        <button
          type="submit"
          className={styles.submitButton}
          disabled={!canSubmit}
          data-testid="comment-submit"
        >
          {isSubmitting ? 'Adding...' : 'Add Comment'}
        </button>
      </div>
    </form>
  );
}
