import { RailCard } from "@/components/HomeRail/RailCard";
import type { PipelineCounts } from "@/hooks/issues";
import { useWorkspaceContext } from "@/hooks/workspace";
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
  const { repos } = useWorkspaceContext();
  const defaultBranch = repos[0]?.default_branch || "the target branch";
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
    label: `Awaiting merge · ${counts.awaitingMerge} ${plural(
      counts.awaitingMerge,
      "branch",
      "branches",
    )} ahead of ${defaultBranch}`,
    count: counts.awaitingMerge,
    operatorOwned: true,
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
