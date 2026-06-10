/**
 * EpicRollup section.
 *
 * Shown in the issue detail panel when the open issue is an epic. Gives the
 * epic the "minimal epic view" from the Aether V3 design: a summary-chip row
 * (tickets / done / PRs / repos), a progress roll-up (distribution bar + "N of
 * M complete"), and a clickable list of its child tickets — each row carrying a
 * status dot, ID, title, a PR chip (when the ticket has a linked PR), a colored
 * status badge, and an assignee avatar (matching the design's data placement).
 *
 * Lives inside the existing IssueDetailPanel (one panel system, one set of
 * tokens) rather than a parallel slide-over, so it can't drift from the panel
 * it lives in.
 */
import { useMemo } from "react";

import type { Issue } from "@/types";
import { formatStatusLabel, isPRUrl } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./EpicRollup.module.css";

export interface EpicRollupProps {
  /** Child tickets of this epic (issues whose parent === epic.id). */
  tickets: Issue[];
  /** Lead agent currently running this epic, when one has claimed it. */
  claimedBy?: string | undefined;
  /** Open a child ticket in the panel. */
  onTicketClick?: (issue: Issue) => void;
}

/** Coarse status bucket used for the distribution bar + dot/badge colors. */
type Bucket = "in_progress" | "review" | "open" | "blocked" | "closed";

/** Bar segments render in this fixed order (left → right). */
const SEGMENTS: { key: Bucket; label: string }[] = [
  { key: "in_progress", label: "In progress" },
  { key: "review", label: "Review" },
  { key: "open", label: "Open" },
  { key: "blocked", label: "Blocked" },
  { key: "closed", label: "Done" },
];

function bucketFor(status: string | undefined): Bucket {
  switch (status) {
    case "closed":
      return "closed";
    case "in_progress":
      return "in_progress";
    case "review":
      return "review";
    case "blocked":
      return "blocked";
    default:
      // open, deferred, and any custom status roll up as "open" work.
      return "open";
  }
}

/** Extract the PR number from a validated PR URL (e.g. .../pull/142 → "142"). */
function prNumberFrom(ref: string | null | undefined): string | null {
  if (!isPRUrl(ref)) return null;
  return ref?.match(/\/pulls?\/(\d+)/)?.[1] ?? null;
}

/** Small avatar circle for a ticket assignee. */
function Avatar({ name }: { name: string }): JSX.Element {
  const initial = name.replace(/^\[H\]\s*/, "").charAt(0).toUpperCase() || "?";
  const color = getAvatarColor(name);
  return (
    <span
      className={styles.avatar}
      style={{ background: color, color: shouldUseWhiteText(color) ? "#fff" : "#111" }}
      title={name}
      aria-label={`Assignee ${name}`}
    >
      {initial}
    </span>
  );
}

export function EpicRollup({
  tickets,
  claimedBy,
  onTicketClick,
}: EpicRollupProps): JSX.Element | null {
  const { counts, total, done, prCount, repos } = useMemo(() => {
    const c: Record<Bucket, number> = {
      in_progress: 0,
      review: 0,
      open: 0,
      blocked: 0,
      closed: 0,
    };
    let prs = 0;
    const repoSet = new Set<string>();
    for (const t of tickets) {
      c[bucketFor(t.status)] += 1;
      if (isPRUrl(t.external_ref)) prs += 1;
      if (t.repo) repoSet.add(t.repo);
    }
    return {
      counts: c,
      total: tickets.length,
      done: c.closed,
      prCount: prs,
      repos: [...repoSet],
    };
  }, [tickets]);

  // Sort tickets by workflow stage so the most active work surfaces first.
  const order: Bucket[] = ["in_progress", "blocked", "review", "open", "closed"];
  const sortedTickets = useMemo(
    () =>
      [...tickets].sort(
        (a, b) =>
          order.indexOf(bucketFor(a.status)) -
          order.indexOf(bucketFor(b.status)),
      ),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [tickets],
  );

  if (total === 0) {
    return (
      <section className={styles.section} data-testid="epic-rollup">
        <h3 className={styles.sectionTitle}>Epic Progress</h3>
        <p className={styles.empty}>No child tickets yet.</p>
      </section>
    );
  }

  return (
    <section className={styles.section} data-testid="epic-rollup">
      {/* Summary chips — tickets / done / PRs / repos (design's epic header row) */}
      <div className={styles.chips}>
        <span className={styles.chip}>
          {total} {total === 1 ? "ticket" : "tickets"}
        </span>
        <span className={styles.chip}>✓ {done} done</span>
        {prCount > 0 && (
          <span className={styles.chip} data-pr="true">
            <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
              <circle cx="4" cy="4" r="1.6" stroke="currentColor" strokeWidth="1.4" />
              <circle cx="4" cy="12" r="1.6" stroke="currentColor" strokeWidth="1.4" />
              <circle cx="12" cy="12" r="1.6" stroke="currentColor" strokeWidth="1.4" />
              <path
                d="M4 5.6v4.8M12 10.4V8a2 2 0 0 0-2-2H7.5"
                stroke="currentColor"
                strokeWidth="1.4"
                strokeLinecap="round"
              />
            </svg>
            {prCount} {prCount === 1 ? "PR" : "PRs"}
          </span>
        )}
        {repos.map((r) => (
          <span key={r} className={styles.repoChip}>
            {r}
          </span>
        ))}
        {claimedBy ? (
          <span
            className={styles.runnerChip}
            title={`Epic run by ${claimedBy}`}
            data-testid="epic-runner-chip"
          >
            <span className={styles.runnerDot} aria-hidden="true" />
            {claimedBy}
          </span>
        ) : (
          <span className={styles.unclaimedChip} data-testid="epic-unclaimed-chip">
            Unclaimed
          </span>
        )}
      </div>

      <div className={styles.progressHead}>
        <h3 className={styles.sectionTitle}>Epic Progress</h3>
        <span className={styles.progressCaption}>
          {done} of {total} complete
        </span>
      </div>

      <div
        className={styles.bar}
        role="img"
        aria-label={`${done} of ${total} tickets complete`}
      >
        {SEGMENTS.map((seg) =>
          counts[seg.key] > 0 ? (
            <span
              key={seg.key}
              className={styles.barSeg}
              data-status={seg.key}
              style={{ width: `${(counts[seg.key] / total) * 100}%` }}
              title={`${seg.label}: ${counts[seg.key]}`}
            />
          ) : null,
        )}
      </div>

      <h3 className={styles.ticketsTitle}>Tickets ({total})</h3>
      <ul className={styles.ticketList}>
        {sortedTickets.map((t) => {
          const clickable = Boolean(onTicketClick);
          const bucket = bucketFor(t.status);
          const pr = prNumberFrom(t.external_ref);
          return (
            <li
              key={t.id}
              className={`${styles.ticket} ${clickable ? styles.clickable : ""}`}
              onClick={clickable ? () => onTicketClick?.(t) : undefined}
              role={clickable ? "button" : undefined}
              tabIndex={clickable ? 0 : undefined}
              onKeyDown={
                clickable
                  ? (e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        onTicketClick?.(t);
                      }
                    }
                  : undefined
              }
            >
              <span
                className={styles.dot}
                data-status={bucket}
                aria-hidden="true"
              />
              <code className={styles.ticketId}>{t.id}</code>
              <span className={styles.ticketTitle}>{t.title}</span>
              {pr && (
                <span className={styles.prChip} title={`Pull request #${pr}`}>
                  #{pr}
                </span>
              )}
              <span className={styles.ticketStatus} data-status={bucket}>
                {formatStatusLabel(t.status ?? "open")}
              </span>
              {t.assignee ? (
                <Avatar name={t.assignee} />
              ) : (
                <span className={styles.avatarEmpty} aria-label="Unassigned" />
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
