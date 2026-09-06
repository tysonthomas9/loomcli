/**
 * CommentForm component.
 * Allows users to add new comments to an issue.
 */

import {
  useState,
  useRef,
  useCallback,
  useMemo,
  useLayoutEffect,
  type FormEvent,
  type KeyboardEvent,
} from "react";

import { addComment } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Comment } from "@/types";

import styles from "./CommentForm.module.css";

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
  const { workspaceId } = useWorkspaceContext();
  const [text, setText] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const owner = useMemo(
    () => ({ workspaceId, issueId, pending: null as object | null }),
    [workspaceId, issueId],
  );
  const committedOwner = useRef<typeof owner | null>(null);
  useLayoutEffect(() => {
    committedOwner.current = owner;
    setText("");
    setError(null);
    setIsSubmitting(false);
    return () => {
      if (committedOwner.current === owner) committedOwner.current = null;
      owner.pending = null;
    };
  }, [owner]);

  const handleSubmit = useCallback(
    async (e?: FormEvent) => {
      e?.preventDefault();

      const trimmedText = text.trim();
      if (!trimmedText || committedOwner.current !== owner || owner.pending)
        return;
      const attempt = {};
      owner.pending = attempt;
      const current = () =>
        committedOwner.current === owner && owner.pending === attempt;

      setError(null);
      setIsSubmitting(true);

      try {
        const newComment = await addComment(workspaceId, issueId, trimmedText);
        if (!current()) return;
        if (newComment.issue_id !== owner.issueId)
          throw new Error("Comment response belongs to another issue");
        setText("");
        onCommentAdded(newComment);
        if (!current()) return;
        // Keep focus in textarea for follow-up comments
        textareaRef.current?.focus();
      } catch (err) {
        if (!current()) return;
        const message =
          err instanceof Error ? err.message : "Failed to add comment";
        setError(message);
      } finally {
        if (current()) {
          owner.pending = null;
          setIsSubmitting(false);
        }
      }
    },
    [text, workspaceId, issueId, onCommentAdded, owner],
  );

  const handleKeyDown = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      // Cmd/Ctrl+Enter to submit
      if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        handleSubmit();
      }
    },
    [handleSubmit],
  );

  const handleTextChange = useCallback(
    (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      setText(e.target.value);
      // Clear error when user types
      if (error) {
        setError(null);
      }
    },
    [error],
  );

  const rootClassName = [styles.commentForm, className]
    .filter(Boolean)
    .join(" ");
  const canSubmit = text.trim().length > 0 && !isSubmitting;

  return (
    <form
      className={rootClassName}
      onSubmit={handleSubmit}
      data-testid="comment-form"
    >
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
          {isSubmitting ? "Adding..." : "Add Comment"}
        </button>
      </div>
    </form>
  );
}
