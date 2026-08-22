import { RailCard } from "@/components/HomeRail/RailCard";
import type { PipelineCounts } from "@/hooks/issues";
import { plural } from "@/utils/plural";

import styles from "./HomeRail.module.css";

interface PipelineRow {
  id:
    | "backlog"
    | "designing"
    | "awaiting-approval"
    | "building"
    | "deferred"
    | "merged"
    | "awaiting-merge";
  label: string;
  count: number;
  operatorOwned?: boolean;
  muted?: boolean;
  title?: string;
}

export interface PipelineCardProps {
  counts: PipelineCounts;
}

export function PipelineCard({ counts }: PipelineCardProps): JSX.Element {
  const awaitingMergeCount = counts.awaitingMerge.reduce(
    (total, group) => total + group.count,
    0,
  );
  const awaitingMergeLabel =
    awaitingMergeCount === 0
      ? "Awaiting merge · nothing ahead"
      : counts.awaitingMerge.length === 1
        ? `Awaiting merge · ${awaitingMergeCount} ${plural(
            awaitingMergeCount,
            "branch",
            "branches",
          )} ahead of ${counts.awaitingMerge[0]?.branch ?? "the target branch"}`
        : `Awaiting merge · ${counts.awaitingMerge
            .map((group) => `${group.count} ahead of ${group.branch}`)
            .join(" · ")}`;
  const taskRows: PipelineRow[] = [
    { id: "backlog", label: "Backlog", count: counts.backlog, muted: true },
    { id: "designing", label: "Designing", count: counts.designing },
    {
      id: "awaiting-approval",
      label: "Awaiting approval",
      count: counts.awaitingApproval,
      operatorOwned: true,
    },
    {
      id: "building",
      label: "Building",
      count: counts.building,
      title: "in progress + blocked",
    },
    { id: "deferred", label: "Deferred", count: counts.deferred, muted: true },
    { id: "merged", label: "Merged & closed", count: counts.merged },
  ];
  const awaitingMerge: PipelineRow = {
    id: "awaiting-merge",
    label: awaitingMergeLabel,
    count: awaitingMergeCount,
    operatorOwned: true,
    muted: awaitingMergeCount === 0,
  };

  const renderRow = (row: PipelineRow): JSX.Element => (
    <div
      className={styles.pipelineRow}
      data-count={row.count}
      data-muted={row.muted || undefined}
      data-row={row.id}
      data-testid="pipeline-row"
      key={row.id}
      title={row.title}
    >
      <span className={styles.pipelineName}>{row.label}</span>
      {row.operatorOwned && <span className={styles.operatorFlag}>you</span>}
      <span className={styles.pipelineCount} data-zero={row.count === 0}>
        {row.count}
      </span>
    </div>
  );

  return (
    <RailCard
      title="Pipeline"
      meta={`${counts.taskCount} ${plural(counts.taskCount, "task", "tasks")}`}
      testId="rail-pipeline"
      bodyClassName={styles.pipelineBody}
    >
      {taskRows.map(renderRow)}
      <div className={styles.pipelineDivider} />
      {renderRow(awaitingMerge)}
    </RailCard>
  );
}
