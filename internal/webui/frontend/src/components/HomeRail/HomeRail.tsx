import type { Issue, UsageResponse } from "@/types";
import type { PipelineCounts } from "@/hooks/issues";
import type { RecentActivityItem } from "@/hooks/workspace";

import { ActivityCard } from "./ActivityCard";
import { BudgetCard } from "./BudgetCard";
import styles from "./HomeRail.module.css";
import { PipelineCard } from "./PipelineCard";
import { ThisWorkspaceCard } from "./ThisWorkspaceCard";

export interface HomeRailProps {
  issues: readonly Issue[];
  workspaceId: string;
  pipeline: PipelineCounts;
  activity: readonly RecentActivityItem[];
  onUsageChange: (usage: UsageResponse | null) => void;
}

export function HomeRail({
  issues,
  workspaceId,
  pipeline,
  activity,
  onUsageChange,
}: HomeRailProps): JSX.Element {
  return (
    <aside className={styles.rail} data-testid="home-rail">
      <ThisWorkspaceCard issues={issues} workspaceId={workspaceId} />
      <PipelineCard counts={pipeline} />
      <ActivityCard activity={activity} />
      <BudgetCard onUsageChange={onUsageChange} />
    </aside>
  );
}
