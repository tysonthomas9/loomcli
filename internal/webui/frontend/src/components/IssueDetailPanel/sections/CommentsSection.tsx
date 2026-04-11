/**
 * CommentsSection component.
 * Displays a list of comments on an issue with author avatars,
 * timestamp, and markdown-rendered text.
 */

import { formatDate } from "@/components/table/columns";
import type { Comment } from "@/types";

import { AuthorAvatar } from "./AuthorAvatar";
import { MarkdownRenderer } from "./MarkdownRenderer";
import styles from "./CommentsSection.module.css";

/**
 * Props for the CommentsSection component.
 */
export interface CommentsSectionProps {
  /** Array of comments to display */
  comments?: Comment[] | undefined;
  /** Additional CSS class name */
  className?: string;
}

/**
 * CommentsSection displays a chronological list of comments on an issue.
 * Shows author avatar, relative timestamp, and markdown-rendered comment text.
 * Displays an empty state when no comments exist.
 */
export function CommentsSection({
  comments,
  className,
}: CommentsSectionProps): JSX.Element {
  const hasComments = comments && comments.length > 0;

  // Sort comments chronologically (oldest first)
  const sortedComments = hasComments
    ? [...comments].sort(
        (a, b) =>
          new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      )
    : [];

  const rootClassName = [styles.section, className].filter(Boolean).join(" ");

  return (
    <section className={rootClassName} data-testid="comments-section">
      <h3 className={styles.sectionTitle}>
        Comments{hasComments ? ` (${sortedComments.length})` : ""}
      </h3>

      {hasComments ? (
        <ul className={styles.commentList}>
          {sortedComments.map((comment) => (
            <li
              key={comment.id}
              className={styles.commentItem}
              data-testid="comment-item"
            >
              <div className={styles.commentHeader}>
                <AuthorAvatar name={comment.author || "Unknown"} />
                <span className={styles.commentAuthor}>
                  {comment.author || "Unknown"}
                </span>
                <time
                  className={styles.commentTime}
                  dateTime={comment.created_at}
                >
                  {formatDate(comment.created_at)}
                </time>
              </div>
              <div className={styles.commentBody}>
                <MarkdownRenderer content={comment.text} />
              </div>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.emptyState} data-testid="comments-empty">
          No comments yet.
        </p>
      )}
    </section>
  );
}
