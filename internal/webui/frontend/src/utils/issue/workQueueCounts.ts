import type { Issue } from "@/types";

export interface WorkQueueCounts {
  backlog: number;
  open: number;
  blocked: number;
  inProgress: number;
  needsReview: number;
  done: number;
}

export function getWorkQueueCounts(issues: Issue[]): WorkQueueCounts {
  const counts: WorkQueueCounts = {
    backlog: 0,
    open: 0,
    blocked: 0,
    inProgress: 0,
    needsReview: 0,
    done: 0,
  };

  for (const issue of issues) {
    if (issue.issue_type === "epic") {
      continue;
    }

    if (issue.status === "closed") {
      counts.done++;
      continue;
    }

    if (issue.status === "review") {
      counts.needsReview++;
      continue;
    }

    if (issue.status === "in_progress") {
      counts.inProgress++;
      continue;
    }

    const isDeferred =
      issue.is_deferred === true || issue.status === "deferred";
    if (isDeferred) {
      counts.backlog++;
      continue;
    }

    const isBlocked = issue.is_blocked === true || issue.status === "blocked";
    if (isBlocked) {
      counts.blocked++;
      continue;
    }

    const isReadyOpen =
      issue.is_ready === true ||
      (issue.is_ready === undefined &&
        (issue.status === "open" || issue.status === undefined));
    if (isReadyOpen) {
      counts.open++;
    }
  }

  return counts;
}
