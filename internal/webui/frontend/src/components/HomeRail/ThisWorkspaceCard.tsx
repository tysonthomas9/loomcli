import type { CSSProperties } from "react";

import { RailCard } from "@/components/HomeRail/RailCard";
import type { Issue } from "@/types";
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

export interface ThisWorkspaceCounts {
  total: number;
  closed: number;
  inProgress: number;
  review: number;
  open: number;
  blocked: number;
  deferred: number;
}

export function deriveThisWorkspaceCounts(
  issues: readonly Issue[],
): ThisWorkspaceCounts {
  const counts: ThisWorkspaceCounts = {
    total: 0,
    closed: 0,
    inProgress: 0,
    review: 0,
    open: 0,
    blocked: 0,
    deferred: 0,
  };

  for (const issue of issues) {
    if (issue.issue_type === "epic") continue;
    counts.total += 1;
    if (issue.status === "closed") counts.closed += 1;
    if (issue.status === "in_progress") counts.inProgress += 1;
    if (issue.status === "review") counts.review += 1;
    if (issue.status === "open") counts.open += 1;
    if (issue.status === "blocked") counts.blocked += 1;
    if (issue.status === "deferred") counts.deferred += 1;
  }

  return counts;
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

export interface ThisWorkspaceCardProps {
  issues: readonly Issue[];
  workspaceId: string;
}

export function ThisWorkspaceCard({
  issues,
  workspaceId,
}: ThisWorkspaceCardProps): JSX.Element {
  const counts = deriveThisWorkspaceCounts(issues);
  const rows = statusRows(counts);

  return (
    <RailCard
      title="This workspace"
      meta={workspaceId}
      testId="rail-this-workspace"
    >
      <div className={styles.runline}>
        <strong>
          {counts.total} {plural(counts.total, "task", "tasks")}
        </strong>{" "}
        · <strong>{counts.closed}</strong> closed
      </div>
      <div className={styles.segmentBar} aria-label="Issue statuses">
        {rows.map((row) =>
          row.count > 0 ? (
            <i
              className={styles.segment}
              data-status={row.status}
              key={row.status}
              style={
                {
                  width: `${(row.count / counts.total) * 100}%`,
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
