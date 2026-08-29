/** Comments shown below the issue's audit-bearing Journey trace. */

import { formatDate } from "@/components/table";
import type { Comment } from "@/types";

import { AuthorAvatar } from "./AuthorAvatar";
import { MarkdownRenderer } from "./MarkdownRenderer";
import styles from "./ActivityLog.module.css";

export interface ActivityLogProps {
  comments: Comment[];
  issueId: string;
}

export function ActivityLog({ comments }: ActivityLogProps): JSX.Element {
  const orderedComments = [...comments].sort(
    (left, right) =>
      new Date(left.created_at).getTime() -
      new Date(right.created_at).getTime(),
  );

  if (orderedComments.length === 0) {
    return (
      <section className={styles.section} data-testid="activity-log">
        <h3 className={styles.sectionTitle}>Comments (0)</h3>
        <p className={styles.emptyState} data-testid="activity-empty">
          No comments yet.
        </p>
      </section>
    );
  }

  return (
    <section className={styles.section} data-testid="activity-log">
      <h3 className={styles.sectionTitle}>
        Comments ({orderedComments.length})
      </h3>
      <div className={styles.timeline}>
        {orderedComments.map((comment) => (
          <div
            key={`c-${comment.id}`}
            className={styles.commentEntry}
            data-testid="activity-comment"
          >
            <AuthorAvatar name={comment.author || "Unknown"} />
            <div className={styles.commentContent}>
              <div className={styles.commentMeta}>
                <span className={styles.author}>
                  {comment.author || "Unknown"}
                </span>
                <time
                  className={styles.timestamp}
                  dateTime={comment.created_at}
                >
                  {formatDate(comment.created_at)}
                </time>
              </div>
              <div className={styles.commentBodyWrap}>
                <MarkdownRenderer content={comment.text} />
              </div>
            </div>
          </div>
        ))}
      </div>
    </section>
  );
}
