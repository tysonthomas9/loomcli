import type { Issue, IssueDetails } from "@/types";

import styles from "./MoveLineageBanner.module.css";

export interface MoveLineageBannerProps {
  issue: Issue | IssueDetails;
}

/**
 * Canonical cross-workspace move lineage used by every issue-detail surface.
 */
export function MoveLineageBanner({
  issue,
}: MoveLineageBannerProps): JSX.Element | null {
  const reference = issue.moved_to ?? issue.moved_from;
  if (!reference) return null;

  const isSource = !!issue.moved_to;
  return (
    <div className={styles.banner} data-testid="move-lineage-banner">
      <span>{isSource ? "Moved to" : "Moved from"}</span>
      <a
        href={`/ws/${encodeURIComponent(reference.workspace)}/issues/${encodeURIComponent(reference.issue_id)}`}
        data-testid="move-lineage-link"
      >
        {reference.issue_id} in {reference.workspace}
      </a>
      {isSource && (
        <span className={styles.immutable}>This source is read-only.</span>
      )}
    </div>
  );
}
