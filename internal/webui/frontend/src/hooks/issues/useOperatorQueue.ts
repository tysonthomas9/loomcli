import { useMemo } from "react";

import type { Issue } from "@/types";
import {
  getReviewType,
  hasDesign,
  hasNeedsRevision,
} from "@/utils/issue/issueCategory";

export type OperatorQueueKind = "design-gate" | "blocked" | "needs-revision";

export interface OperatorQueueItem {
  issue: Issue;
  kind: OperatorQueueKind;
  /** Milliseconds since epoch; derived from updated_at as a current-state proxy. */
  waitingSince: number;
}

function queueKind(issue: Issue): OperatorQueueKind | null {
  if (issue.issue_type === "epic") return null;

  const reviewType = getReviewType(issue);
  if (reviewType === "plan" && hasDesign(issue)) return "design-gate";
  if (reviewType === "help") return "blocked";
  if (issue.status === "open" && hasNeedsRevision(issue)) {
    return "needs-revision";
  }

  return null;
}

function waitingSince(issue: Issue): number {
  const parsed = Date.parse(issue.updated_at);
  return Number.isNaN(parsed) ? Number.POSITIVE_INFINITY : parsed;
}

/**
 * Derive the present-tense operator queue from the shared Kanban collection.
 * Oldest updated_at sorts first; ids make equal timestamps deterministic.
 */
export function deriveOperatorQueue(
  issues: readonly Issue[],
): OperatorQueueItem[] {
  return issues
    .flatMap((issue): OperatorQueueItem[] => {
      const kind = queueKind(issue);
      return kind ? [{ issue, kind, waitingSince: waitingSince(issue) }] : [];
    })
    .sort(
      (a, b) =>
        a.waitingSince - b.waitingSince || a.issue.id.localeCompare(b.issue.id),
    );
}

export function useOperatorQueue(
  issues: readonly Issue[],
): OperatorQueueItem[] {
  return useMemo(() => deriveOperatorQueue(issues), [issues]);
}
