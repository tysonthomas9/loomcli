import type { Issue } from "@/types";
import type { RecentActivityItem } from "@/hooks/workspace";

import { ActivityCard } from "./ActivityCard";
import styles from "./HomeRail.module.css";
import { ThisWorkspaceCard } from "./ThisWorkspaceCard";

export interface HomeRailProps {
  issues: readonly Issue[];
  workspaceId: string;
  activity: readonly RecentActivityItem[];
}

export function HomeRail({
  issues,
  workspaceId,
  activity,
}: HomeRailProps): JSX.Element {
  return (
    <aside className={styles.rail} data-testid="home-rail">
      <ThisWorkspaceCard issues={issues} workspaceId={workspaceId} />
      <ActivityCard activity={activity} key={workspaceId} />
    </aside>
  );
}
