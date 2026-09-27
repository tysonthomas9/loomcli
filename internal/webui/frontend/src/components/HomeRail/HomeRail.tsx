import type { RecentActivityItem } from "@/hooks/workspace";

import { ActivityCard } from "./ActivityCard";
import styles from "./HomeRail.module.css";
import { ThisWorkspaceCard } from "./ThisWorkspaceCard";
import type { ThisWorkspaceCounts } from "./ThisWorkspaceCard";

export interface HomeRailProps {
  /** Workspace-wide counts; null until the stats request resolves. */
  counts: ThisWorkspaceCounts | null;
  workspaceId: string;
  activity: readonly RecentActivityItem[];
}

export function HomeRail({
  counts,
  workspaceId,
  activity,
}: HomeRailProps): JSX.Element {
  return (
    <aside className={styles.rail} data-testid="home-rail">
      <ThisWorkspaceCard counts={counts} workspaceId={workspaceId} />
      <ActivityCard activity={activity} key={workspaceId} />
    </aside>
  );
}
