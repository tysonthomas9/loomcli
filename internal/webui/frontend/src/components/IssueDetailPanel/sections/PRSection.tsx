/**
 * PRSection — the design's pr-card / pr-empty block for review-stage issues.
 *
 * When the issue carries a validated PR URL in external_ref, renders a card
 * with the PR number, state tag, and a "View PR" link. When a review issue
 * has no PR yet, renders the design's dashed "No pull request yet"
 * placeholder so the review surface explains itself instead of being blank.
 */

import { isPRUrl } from "@/utils/issue";

import styles from "./PRSection.module.css";

export interface PRSectionProps {
  /** Structural subset so both Issue and IssueDetails fit. */
  issue: {
    status?: string | undefined;
    external_ref?: string | null | undefined;
  };
}

/** Extract the PR number from a validated PR URL (.../pull/142 → "142"). */
function prNumberFrom(ref: string | null | undefined): string | null {
  if (!isPRUrl(ref)) return null;
  return ref?.match(/\/pulls?\/(\d+)/)?.[1] ?? null;
}

export function PRSection({ issue }: PRSectionProps): JSX.Element | null {
  const hasPR = isPRUrl(issue.external_ref);
  const isReview = issue.status === "review";

  // Only render where the design does: a PR card whenever a PR exists, and
  // the "no PR yet" placeholder only on review-stage issues.
  if (!hasPR && !isReview) return null;

  if (!hasPR) {
    return (
      <section
        className={styles.empty}
        aria-label="Pull request"
        data-testid="pr-section-empty"
      >
        <span className={styles.emptyText}>No pull request yet</span>
        <span className={styles.emptyHint}>
          The agent hasn&apos;t pushed a branch for this task.
        </span>
      </section>
    );
  }

  const prNumber = prNumberFrom(issue.external_ref);
  const stateLabel = issue.status === "closed" ? "Merged" : "Open";

  return (
    <section
      className={styles.card}
      aria-label="Pull request"
      data-testid="pr-section"
    >
      <div className={styles.cardHead}>
        <span className={styles.prPill}>
          {prNumber ? `#${prNumber}` : "PR"}
        </span>
        <span className={styles.stateTag} data-state={stateLabel.toLowerCase()}>
          {stateLabel}
        </span>
        <a
          className={styles.link}
          href={issue.external_ref ?? undefined}
          target="_blank"
          rel="noopener noreferrer"
        >
          View PR ↗
        </a>
      </div>
    </section>
  );
}
