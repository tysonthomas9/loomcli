/**
 * EpicTicketsSection — the "minimal epic detail" from the Aether design
 * (pin 25): claim status, progress, a status distribution bar, and the
 * epic's child tickets. Rendered in the Details tab when the open issue is
 * an epic.
 */

import { useMemo } from "react";

import type { Issue } from "@/types";
import { statusBucket } from "@/utils/statusBuckets";

import styles from "./EpicTicketsSection.module.css";

export interface EpicTicketsSectionProps {
  /** Child issues of the epic (non-epic issues whose parent is the epic). */
  childIssues: Issue[];
  /** Lead agent currently running the epic, when one has claimed it. */
  claimedBy?: string | undefined;
  /** Open a child ticket in the panel. */
  onNavigateToIssue?: ((issue: Issue) => void) | undefined;
}

const BUCKET_ORDER = [
  "in_progress",
  "open",
  "review",
  "blocked",
  "done",
] as const;

const BUCKET_LABEL: Record<(typeof BUCKET_ORDER)[number], string> = {
  in_progress: "in progress",
  open: "open",
  review: "review",
  blocked: "blocked",
  done: "done",
};

const STATUS_RANK: Record<string, number> = {
  in_progress: 0,
  open: 1,
  blocked: 2,
  review: 3,
  done: 4,
};

export function EpicTicketsSection({
  childIssues,
  claimedBy,
  onNavigateToIssue,
}: EpicTicketsSectionProps): JSX.Element | null {
  const { sorted, counts, doneCount } = useMemo(() => {
    const tally: Record<(typeof BUCKET_ORDER)[number], number> = {
      in_progress: 0,
      open: 0,
      review: 0,
      blocked: 0,
      done: 0,
    };
    for (const child of childIssues) {
      tally[statusBucket(child.status ?? "open")]++;
    }
    const ordered = [...childIssues].sort(
      (a, b) =>
        (STATUS_RANK[statusBucket(a.status ?? "open")] ?? 5) -
        (STATUS_RANK[statusBucket(b.status ?? "open")] ?? 5),
    );
    return { sorted: ordered, counts: tally, doneCount: tally.done };
  }, [childIssues]);

  if (childIssues.length === 0) return null;

  const segments = BUCKET_ORDER.map((kind) => ({
    kind,
    value: counts[kind],
  })).filter((segment) => segment.value > 0);
  const distSummary = segments
    .map((segment) => `${segment.value} ${BUCKET_LABEL[segment.kind]}`)
    .join(", ");

  return (
    <section className={styles.section} data-testid="epic-tickets-section">
      <div className={styles.headerRow}>
        <h3 className={styles.sectionTitle}>
          Tickets ({childIssues.length})
        </h3>
        <span className={styles.progressText}>
          {doneCount} of {childIssues.length} done
        </span>
        {claimedBy ? (
          <span className={styles.runnerBadge} title={`Run by ${claimedBy}`}>
            <span className={styles.runnerDot} aria-hidden="true" />
            {claimedBy}
          </span>
        ) : (
          <span className={styles.unclaimedBadge}>Unclaimed</span>
        )}
      </div>

      <div
        className={styles.distBar}
        role="img"
        aria-label={`Status distribution: ${distSummary}`}
      >
        {segments.map((segment) => (
          <span
            key={segment.kind}
            className={styles.distSeg}
            data-k={segment.kind}
            style={{ flexGrow: segment.value }}
          />
        ))}
      </div>

      <ul className={styles.ticketList}>
        {sorted.map((child) => {
          const bucket = statusBucket(child.status ?? "open");
          const clickable = onNavigateToIssue !== undefined;
          return (
            <li key={child.id}>
              <button
                type="button"
                className={styles.ticketRow}
                data-status={bucket}
                onClick={
                  clickable ? () => onNavigateToIssue(child) : undefined
                }
                disabled={!clickable}
                aria-label={`Open ticket ${child.id}: ${child.title}`}
              >
                <span
                  className={styles.statusDot}
                  data-k={bucket}
                  aria-hidden="true"
                />
                <code className={styles.ticketId}>{child.id}</code>
                <span className={styles.ticketTitle}>{child.title}</span>
                <span className={styles.statusChip} data-k={bucket}>
                  {BUCKET_LABEL[bucket]}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
