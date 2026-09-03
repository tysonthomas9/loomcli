/**
 * ActivityLog component.
 * Unified timeline that interleaves comments with system events
 * in chronological order.
 */

import { useMemo } from "react";

import { formatDate } from "@/components/table";
import type { Comment, Event, EventType } from "@/types";

import { AuthorAvatar } from "./AuthorAvatar";
import { MarkdownRenderer } from "./MarkdownRenderer";
import styles from "./ActivityLog.module.css";

type ActivityItem =
  | { kind: "comment"; data: Comment }
  | { kind: "event"; data: Event };

function getTimestamp(item: ActivityItem): number {
  return new Date(item.data.created_at).getTime();
}

/**
 * Human-readable description for system events.
 *
 * The backend's own summary wins when it has one. fleet-db names the actions it
 * emits ("issue.update", "dep.add") in a different vocabulary from the
 * EventType cases below, so without this every fleet event fell through to
 * "performed an action" and the whole feed said nothing.
 */
function describeEvent(event: Event): string {
  const { event_type, actor, old_value, new_value, summary } = event;
  const who = actor || "Someone";
  if (summary) {
    return `${who}: ${summary}`;
  }

  switch (event_type as EventType) {
    case "issue.created":
      return `${who} created this issue`;
    case "issue.status_changed":
      if (old_value && new_value) {
        return `${who} changed status from ${old_value} to ${new_value}`;
      }
      return `${who} changed the status`;
    case "issue.closed":
      return `${who} closed this issue`;
    case "issue.reopened":
      return `${who} reopened this issue`;
    case "issue.updated":
      if (old_value && new_value) {
        return `${who} updated ${old_value} to ${new_value}`;
      }
      return `${who} updated this issue`;
    case "issue.dependency_added":
      return `${who} added dependency ${new_value || ""}`.trim();
    case "issue.dependency_removed":
      return `${who} removed dependency ${old_value || ""}`.trim();
    case "issue.label_added":
      return `${who} added label ${new_value || ""}`.trim();
    case "issue.label_removed":
      return `${who} removed label ${old_value || ""}`.trim();
    case "issue.commented":
      return `${who} commented`;
    case "issue.compacted":
      return "Earlier activity was summarized";
    default:
      return `${who} performed an action`;
  }
}

function truncateValue(value: string): string {
  const maxLength = 80;
  return value.length > maxLength ? `${value.slice(0, maxLength - 1)}…` : value;
}

/**
 * A field being set for the first time has no before, and a field being cleared
 * has no after. Both arrive as an omitted key, so name the absence rather than
 * rendering a gap the reader has to guess at.
 */
function presentValue(value: string | undefined): string {
  return value ? value : "(empty)";
}

/** Icon for each event type. */
function EventIcon({ eventType }: { eventType: EventType }): JSX.Element {
  switch (eventType) {
    case "issue.status_changed":
    case "issue.closed":
    case "issue.reopened":
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="6" stroke="currentColor" strokeWidth="1.5" />
          <path
            d="M5.5 8l2 2 3-3"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      );
    case "issue.label_added":
    case "issue.label_removed":
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <path
            d="M2 8.5l5.5 5.5L15 6.5V2h-4.5L2 8.5z"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinejoin="round"
          />
          <circle cx="12" cy="4.5" r="1" fill="currentColor" />
        </svg>
      );
    case "issue.dependency_added":
    case "issue.dependency_removed":
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <path
            d="M4 8h8M8 4v8"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
          />
        </svg>
      );
    default:
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <circle cx="8" cy="8" r="3" fill="currentColor" opacity="0.4" />
        </svg>
      );
  }
}

export interface ActivityLogProps {
  comments: Comment[];
  events: Event[];
  issueId: string;
}

export function ActivityLog({
  comments,
  events,
}: ActivityLogProps): JSX.Element {
  const items = useMemo<ActivityItem[]>(() => {
    const all: ActivityItem[] = [
      ...comments.map((c): ActivityItem => ({ kind: "comment", data: c })),
      ...events
        .filter((e) => e.event_type !== "issue.commented") // avoid duplicates with comments
        .map((e): ActivityItem => ({ kind: "event", data: e })),
    ];
    all.sort((a, b) => getTimestamp(a) - getTimestamp(b));
    return all;
  }, [comments, events]);

  const totalCount = items.length;

  if (totalCount === 0) {
    return (
      <section className={styles.section} data-testid="activity-log">
        <h3 className={styles.sectionTitle}>Activity</h3>
        <p className={styles.emptyState} data-testid="activity-empty">
          No activity yet.
        </p>
      </section>
    );
  }

  return (
    <section className={styles.section} data-testid="activity-log">
      <h3 className={styles.sectionTitle}>Activity ({totalCount})</h3>
      <div className={styles.timeline}>
        {items.map((item) => {
          if (item.kind === "comment") {
            const comment = item.data;
            return (
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
            );
          }

          const event = item.data;
          return (
            <div
              key={`e-${event.event_id || event.id}-${event.created_at}`}
              className={styles.eventEntry}
              data-testid="activity-event"
            >
              <EventIcon eventType={event.event_type as EventType} />
              <div className={styles.eventDetails}>
                <span className={styles.eventDescription}>
                  {describeEvent(event)}
                </span>
                {event.changes?.length ? (
                  <div className={styles.eventChanges}>
                    {event.changes.map((change, i) => {
                      const before = presentValue(change.before);
                      const after = presentValue(change.after);
                      return (
                        <span
                          key={`${change.field}-${i}`}
                          title={`${change.field}: ${before} → ${after}`}
                        >
                          {change.field}: {truncateValue(before)} →{" "}
                          {truncateValue(after)}
                        </span>
                      );
                    })}
                  </div>
                ) : null}
              </div>
              <time className={styles.timestamp} dateTime={event.created_at}>
                {formatDate(event.created_at)}
              </time>
            </div>
          );
        })}
      </div>
    </section>
  );
}
