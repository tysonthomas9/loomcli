import styles from "./ReadinessBadge.module.css";
import {
  readinessDisplay,
  type ReadinessDisplay,
} from "@/utils/pullRequest/readinessDisplay";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";

export interface ReadinessBadgeProps {
  view?: PullRequestReadinessView | null;
  display?: ReadinessDisplay;
  compact?: boolean;
}

export function ReadinessBadge({
  view,
  display,
  compact = false,
}: ReadinessBadgeProps): JSX.Element {
  const d = display ?? readinessDisplay(view);
  return (
    <span
      className={styles.badge}
      data-key={d.key}
      data-historical={d.isHistorical || undefined}
      title={d.reasons.join(", ") || d.label}
      data-testid="readiness-badge"
    >
      {!compact && d.isHistorical ? (
        <span className={styles.histMark} aria-hidden="true">
          ◌
        </span>
      ) : null}
      {d.label}
    </span>
  );
}
