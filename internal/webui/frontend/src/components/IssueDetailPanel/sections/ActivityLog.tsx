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

const FIELD_LABELS: Record<string, string> = {
  issue_type: "type",
  defer_until: "defer date",
};
const BOOKKEEPING_FIELDS = new Set(["created_at", "updated_at"]);

function getTimestamp(item: ActivityItem): number {
  return new Date(item.data.created_at).getTime();
}

function fieldLabel(field: string): string {
  return FIELD_LABELS[field] ?? field;
}

function visibleUpdateFields(fields: string[] | undefined): string[] {
  return (fields ?? []).filter((field) => !BOOKKEEPING_FIELDS.has(field));
}

function updateFieldSummary(event: Event, who: string): string {
  const { field, fields, field_count, old_value, new_value } = event;
  const visibleFields = Array.isArray(fields)
    ? visibleUpdateFields(fields)
    : [];
  const visibleField =
    field && !BOOKKEEPING_FIELDS.has(field) ? field : undefined;

  if ((field_count ?? 0) > 1) {
    if (visibleFields.length > 0) {
      return `${who} updated ${visibleFields.map(fieldLabel).join(", ")}`;
    }
    if (Array.isArray(fields) && fields.length > 0) {
      return `${who} updated this issue`;
    }
    return `${who} updated ${field_count} fields`;
  }
  if (visibleFields.length > 1) {
    return `${who} updated ${visibleFields.map(fieldLabel).join(", ")}`;
  }
  if (visibleFields.length === 1 && !visibleField) {
    const onlyField = visibleFields[0];
    if (onlyField) {
      return `${who} updated ${fieldLabel(onlyField)}`;
    }
  }
  if (visibleField && old_value && new_value) {
    return `${who} updated ${fieldLabel(visibleField)} from ${old_value} to ${new_value}`;
  }
  if (old_value && new_value && !field) {
    return `${who} updated ${old_value} to ${new_value}`;
  }
  if (visibleFields.length > 0) {
    return `${who} updated ${visibleFields.map(fieldLabel).join(", ")}`;
  }
  if (Array.isArray(fields) && fields.length > 0) {
    return `${who} updated this issue`;
  }
  return `${who} updated this issue`;
}

/** Human-readable description for system events. */
export function describeEvent(event: Event): string {
  const { event_type, actor, old_value, new_value, comment } = event;
  const who = actor || "Someone";

  switch (event_type as EventType) {
    case "issue.created":
      return `${who} created this issue`;
    case "issue.status_changed":
      if (old_value && new_value) {
        return `${who} changed status from ${old_value} to ${new_value}`;
      }
      return `${who} changed the status`;
    case "issue.claimed":
      return `${who} claimed this issue`;
    case "issue.released":
      return `${who} released this issue`;
    case "issue.deferred":
      if (new_value) {
        return `${who} deferred this issue until ${new_value}`;
      }
      return `${who} deferred this issue`;
    case "issue.undeferred":
      return `${who} un-deferred this issue`;
    case "issue.closed":
      return comment
        ? `${who} closed this issue: ${comment}`
        : `${who} closed this issue`;
    case "issue.reopened":
      return `${who} reopened this issue`;
    case "issue.assigned":
      if (new_value) {
        return `${who} assigned this issue to ${new_value}`;
      }
      return `${who} assigned this issue`;
    case "issue.deleted":
      return `${who} deleted this issue`;
    case "issue.updated":
      return updateFieldSummary(event, who);
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

/** Icon for each event type. */
function EventIcon({ eventType }: { eventType: EventType }): JSX.Element {
  switch (eventType) {
    case "issue.status_changed":
    case "issue.deferred":
    case "issue.undeferred":
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
    case "issue.deleted":
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <path
            d="M5.25 5.75v6.5m2.75-6.5v6.5m2.75-6.5v6.5M3.75 4h8.5M6.25 4V2.75h3.5V4m-5 0 .45 9.25h5.6L11.25 4"
            stroke="currentColor"
            strokeWidth="1.25"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      );
    case "issue.claimed":
    case "issue.released":
    case "issue.assigned":
      return (
        <svg className={styles.eventIcon} viewBox="0 0 16 16" fill="none">
          <circle
            cx="8"
            cy="5"
            r="2.25"
            stroke="currentColor"
            strokeWidth="1.3"
          />
          <path
            d="M3.75 13c.55-2.25 2-3.4 4.25-3.4s3.7 1.15 4.25 3.4"
            stroke="currentColor"
            strokeWidth="1.3"
            strokeLinecap="round"
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
              key={`e-${event.id}`}
              className={styles.eventEntry}
              data-testid="activity-event"
            >
              <EventIcon eventType={event.event_type as EventType} />
              <span className={styles.eventDescription}>
                {describeEvent(event)}
              </span>
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
