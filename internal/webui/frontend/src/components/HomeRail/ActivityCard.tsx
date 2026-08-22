import { useElapsedTime } from "@/hooks/common";
import { SEED_ISSUE_COUNT, useWorkspaceContext } from "@/hooks/workspace";
import type { RecentActivityItem } from "@/hooks/workspace";
import { plural } from "@/utils/plural";
import { repoNameForSource } from "@/utils/workspace/repoPresentation";

import { RailCard } from "@/components/HomeRail/RailCard";

import styles from "./HomeRail.module.css";

const MAX_VISIBLE_ACTIVITY = 7;

function formatActivityTime(timestamp: string): string {
  const date = new Date(timestamp);
  if (Number.isNaN(date.getTime())) return "--:--:--";
  return date.toLocaleTimeString("en-GB", { hour12: false });
}

export function activityMeta(activity: readonly RecentActivityItem[]): string {
  const timestamp = activity[0]?.timestamp;
  if (!timestamp) return "idle";
  const elapsed = Math.max(0, Date.now() - Date.parse(timestamp));
  if (elapsed < 60_000) return "live";
  return `idle ${Math.floor(elapsed / 60_000)}m`;
}

export interface ActivityCardProps {
  activity: readonly RecentActivityItem[];
}

export function ActivityCard({ activity }: ActivityCardProps): JSX.Element {
  const { repos } = useWorkspaceContext();
  const newestTimestamp = activity[0]?.timestamp;
  useElapsedTime(
    newestTimestamp && Number.isFinite(Date.parse(newestTimestamp))
      ? Date.parse(newestTimestamp)
      : null,
  );
  const visible = activity.slice(0, MAX_VISIBLE_ACTIVITY);

  return (
    <RailCard
      title="Activity"
      meta={activityMeta(activity)}
      testId="rail-activity"
    >
      {visible.length > 0 ? (
        <ul className={styles.feed}>
          {visible.map((item) => (
            <li data-testid="activity-row" key={item.id}>
              <time>{formatActivityTime(item.timestamp)}</time>
              <span className={styles.marker} data-marker={item.marker} />
              <span className={styles.activitySentence}>
                <strong data-operator={item.isOperator || undefined}>
                  {item.actor}
                </strong>{" "}
                {item.issueId && <code>{item.issueId}</code>}{" "}
                {repos.length > 1 && item.sourceRepo && (
                  <span
                    className={styles.activityRepo}
                    data-testid="activity-repo"
                  >
                    {repoNameForSource(repos, item.sourceRepo)}
                  </span>
                )}{" "}
                {item.text}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className={styles.emptyActivity}>No workspace activity yet.</p>
      )}
      <footer className={styles.feedFoot}>
        Showing {visible.length} of {activity.length}{" "}
        {plural(activity.length, "event", "events")} from the {SEED_ISSUE_COUNT}{" "}
        most recently updated tasks, plus live changes
      </footer>
    </RailCard>
  );
}
