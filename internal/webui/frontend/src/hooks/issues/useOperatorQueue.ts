import { useMemo } from "react";

import type { Issue } from "@/types";
import {
  getReviewType,
  hasDesign,
  hasNeedsRevision,
} from "@/utils/issue/issueCategory";

import { useQueueChildren } from "./useQueueChildren";
import type { QueueChild, QueueChildIndex } from "./useQueueChildren";

export type { QueueChild, QueueChildIndex };

export type OperatorQueueKind = "design-gate" | "blocked" | "needs-revision";

export interface OperatorQueueItem {
  issue: Issue;
  kind: OperatorQueueKind;
  /** Milliseconds since epoch; derived from updated_at as a current-state proxy. */
  waitingSince: number;
}

/**
 * A parked parent whose decomposition is still in flight is *waiting*, not stuck:
 * nothing a human does to it helps, and un-parking it would release a parent whose
 * children have not landed. Children are read from the issue's `parent-child`
 * dependents, never from `notes`.
 *
 * Fails open by design: an id absent from the index has unknown children (not yet
 * fetched, or the fetch failed) and keeps today's behaviour.
 */
function isWaitingOnChildren(
  issue: Issue,
  children: QueueChildIndex | undefined,
): boolean {
  const known = children?.get(issue.id);
  if (!known) return false;
  return known.some((child) => child.status !== "closed");
}

function queueKind(
  issue: Issue,
  children?: QueueChildIndex,
): OperatorQueueKind | null {
  if (issue.issue_type === "epic") return null;

  const reviewType = getReviewType(issue);
  if (reviewType === "plan" && hasDesign(issue)) return "design-gate";
  if (reviewType === "help") {
    return isWaitingOnChildren(issue, children) ? null : "blocked";
  }
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
 *
 * Pure: `children` is supplied by the caller so every case is fixture-testable
 * with no running stack.
 */
export function deriveOperatorQueue(
  issues: readonly Issue[],
  children?: QueueChildIndex,
): OperatorQueueItem[] {
  return issues
    .flatMap((issue): OperatorQueueItem[] => {
      const kind = queueKind(issue, children);
      return kind ? [{ issue, kind, waitingSince: waitingSince(issue) }] : [];
    })
    .sort(
      (a, b) =>
        a.waitingSince - b.waitingSince || a.issue.id.localeCompare(b.issue.id),
    );
}

/**
 * Both call sites keep the 1-argument signature: the nav badge and the rendered
 * panel must never disagree about what is in the queue.
 */
export function useOperatorQueue(
  issues: readonly Issue[],
): OperatorQueueItem[] {
  const children = useQueueChildren(issues);
  return useMemo(
    () => deriveOperatorQueue(issues, children),
    [issues, children],
  );
}
