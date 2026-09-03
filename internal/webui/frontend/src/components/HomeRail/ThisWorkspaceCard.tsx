import type { CSSProperties } from "react";

import { RailCard } from "@/components/HomeRail/RailCard";
import type { Statistics } from "@/types";
import { plural } from "@/utils/plural";

import styles from "./HomeRail.module.css";

type WorkspaceStatus =
  | "closed"
  | "in_progress"
  | "review"
  | "open"
  | "blocked"
  | "deferred";

interface StatusCount {
  status: WorkspaceStatus;
  label: string;
  count: number;
}

/**
 * Workspace-wide counts rendered by ThisWorkspaceCard.
 *
 * Every field means "issues whose STATUS is X", and every field comes from
 * GET /api/workspaces/{ws}/stats — never from an issue collection a view
 * happens to have fetched. Counts are workspace-wide and INCLUDE epics.
 */
export interface ThisWorkspaceCounts {
  /**
   * Total issues in the workspace, epics included. It EXCLUDES deferred
   * issues, because fleet-db's grouped status count skips the deferred index
   * and /issues/count exposes no opt-in param — so `total` is not the sum of
   * the six rows whenever `deferred` is nonzero. That is expected; see the
   * barTotal note in ThisWorkspaceCard.
   */
  total: number;
  closed: number;
  inProgress: number;
  review: number;
  open: number;
  /**
   * Issues whose STATUS is "blocked". Not the computed dependency-blocked
   * view (`blocked_issues`), whose members mostly carry status "open" and
   * would therefore double-count against `open` in the segment bar.
   */
  blocked: number;
  /**
   * Issues whose STATUS is "deferred". `deferred_issues` is the only route to
   * this number — fleet-db's /issues/deferred view IS the deferred status
   * index — so do not "fix" it to read from the grouped count.
   */
  deferred: number;
}

/**
 * Maps the workspace stats payload onto the card's counts.
 *
 * This function is the single place the field-meaning decisions above are
 * written down. Field map:
 *   total       <- total_issues          (epics included, deferred excluded)
 *   closed      <- closed_issues
 *   inProgress  <- in_progress_issues
 *   open        <- open_issues
 *   review      <- review_issues
 *   blocked     <- status_blocked_issues (STATUS, not the computed view)
 *   deferred    <- deferred_issues       (STATUS)
 *
 * The two newest fields are coerced with `?? 0`: an older server, or the
 * legacy daemon-pool /stats path, omits them at runtime despite the spec
 * marking them required.
 *
 * If a future ticket wants epics excluded again, the route is an extra
 * `type=epic` grouped call in FleetBackend.Stats — not counting issues here.
 */
export function workspaceCountsFromStats(
  stats: Statistics | null,
): ThisWorkspaceCounts | null {
  if (!stats) return null;
  return {
    total: stats.total_issues,
    closed: stats.closed_issues,
    inProgress: stats.in_progress_issues,
    review: stats.review_issues ?? 0,
    open: stats.open_issues,
    blocked: stats.status_blocked_issues ?? 0,
    deferred: stats.deferred_issues,
  };
}

function statusRows(counts: ThisWorkspaceCounts): StatusCount[] {
  return [
    { status: "closed", label: "closed", count: counts.closed },
    {
      status: "in_progress",
      label: "in progress",
      count: counts.inProgress,
    },
    { status: "review", label: "review", count: counts.review },
    { status: "open", label: "open", count: counts.open },
    { status: "blocked", label: "blocked", count: counts.blocked },
    { status: "deferred", label: "deferred", count: counts.deferred },
  ];
}

const EMPTY_COUNTS: ThisWorkspaceCounts = {
  total: 0,
  closed: 0,
  inProgress: 0,
  review: 0,
  open: 0,
  blocked: 0,
  deferred: 0,
};

export interface ThisWorkspaceCardProps {
  /** Workspace-wide counts; null until the stats request resolves. */
  counts: ThisWorkspaceCounts | null;
  workspaceId: string;
}

export function ThisWorkspaceCard({
  counts,
  workspaceId,
}: ThisWorkspaceCardProps): JSX.Element {
  const resolved = counts ?? EMPTY_COUNTS;
  const rows = statusRows(resolved);
  // `total` omits deferred issues, so the rows can sum past it. Normalise the
  // bar against whichever is larger so a segment can never exceed 100%.
  const rowSum = rows.reduce((sum, row) => sum + row.count, 0);
  const barTotal = Math.max(resolved.total, rowSum);

  return (
    <RailCard
      title="This workspace"
      meta={workspaceId}
      testId="rail-this-workspace"
    >
      <div className={styles.runline}>
        <strong>
          {resolved.total} {plural(resolved.total, "issue", "issues")}
        </strong>{" "}
        · <strong>{resolved.closed}</strong> closed
      </div>
      <div className={styles.segmentBar} aria-label="Issue statuses">
        {rows.map((row) =>
          row.count > 0 && barTotal > 0 ? (
            <i
              className={styles.segment}
              data-status={row.status}
              key={row.status}
              style={
                {
                  width: `${(row.count / barTotal) * 100}%`,
                } as CSSProperties
              }
            />
          ) : null,
        )}
      </div>
      <div className={styles.legend}>
        {rows.map((row) => (
          <span data-zero={row.count === 0 || undefined} key={row.status}>
            <i data-status={row.status} />
            {row.label} {row.count}
          </span>
        ))}
      </div>
    </RailCard>
  );
}
