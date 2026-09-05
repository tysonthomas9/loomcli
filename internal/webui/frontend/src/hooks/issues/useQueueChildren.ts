/**
 * useQueueChildren - parent-child dependents for the operator queue's candidates.
 *
 * The Kanban collection the queue derives from does not contain the children of a
 * decomposed parent (the list endpoint clamps well below its requested limit, and
 * the collection rows carry no usable child signal), so the child link has to be
 * read from the issue-detail endpoint. Only issues that are already queue
 * candidates are fetched, which keeps the cost bounded to the set a human is being
 * asked to look at.
 *
 * The hook never throws and never rejects: an id whose fetch failed is simply
 * absent from the returned index, and `deriveOperatorQueue` fails open on absence.
 */

import { useEffect, useMemo, useRef, useState } from "react";

import { getIssue } from "@/api/issues";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, IssueDetails } from "@/types";
import { DepParentChild } from "@/types/issue/dependency";

/** A parent-child dependent of a queue candidate, reduced to what the rule reads. */
export interface QueueChild {
  id: string;
  status?: string;
}

/** id -> its parent-child dependents. An absent id means "children unknown". */
export type QueueChildIndex = ReadonlyMap<string, readonly QueueChild[]>;

const EMPTY_INDEX: QueueChildIndex = new Map<string, readonly QueueChild[]>();

/** Cached children per `${id}@${updated_at}`; null records a failed fetch. */
type CacheEntry = readonly QueueChild[] | null;

const childCache = new Map<string, CacheEntry>();

/** Beyond this, entries for ids that are no longer candidates are dropped. */
const CACHE_PRUNE_THRESHOLD = 200;

/** Test-only: clear the module-level cache between cases. */
export function resetQueueChildrenCache(): void {
  childCache.clear();
}

interface Candidate {
  id: string;
  key: string;
}

/**
 * The queue's own candidate set: an agent park is `status: "blocked"` plus a note.
 * Anything else never reaches the "help" branch of the queue, so it is never fetched.
 */
function isCandidate(issue: Issue): boolean {
  return issue.status === "blocked" && !!issue.notes?.trim();
}

function parentChildDependents(details: IssueDetails): readonly QueueChild[] {
  return (details.dependents ?? [])
    .filter((dependent) => dependent.dependency_type === DepParentChild)
    .map(
      (dependent): QueueChild =>
        dependent.status === undefined
          ? { id: dependent.id }
          : { id: dependent.id, status: dependent.status },
    );
}

export function useQueueChildren(issues: readonly Issue[]): QueueChildIndex {
  const { workspaceId } = useWorkspaceContext();

  const candidates = useMemo<Candidate[]>(
    () =>
      issues.filter(isCandidate).map((issue) => ({
        id: issue.id,
        key: `${issue.id}@${issue.updated_at}`,
      })),
    [issues],
  );

  const [version, setVersion] = useState<number>(0);
  const mountedRef = useRef<boolean>(true);
  const requestIdRef = useRef<number>(0);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (!workspaceId) return;

    const missing = candidates.filter(({ key }) => !childCache.has(key));
    if (missing.length === 0) return;

    const requestId = ++requestIdRef.current;
    let cancelled = false;

    void Promise.all(
      missing.map(async ({ id, key }) => {
        try {
          childCache.set(
            key,
            parentChildDependents(await getIssue(workspaceId, id)),
          );
        } catch {
          // Fail open: the id stays out of the index and keeps today's behaviour.
          childCache.set(key, null);
        }
      }),
    ).then(() => {
      if (cancelled || !mountedRef.current) return;
      if (requestId !== requestIdRef.current) return;
      setVersion((previous) => previous + 1);
    });

    return () => {
      cancelled = true;
    };
  }, [workspaceId, candidates]);

  return useMemo(() => {
    // The cache is mutated outside React's knowledge, so the bump that follows a
    // fetch is what makes this memo recompute.
    void version;

    const index = new Map<string, readonly QueueChild[]>();
    for (const { id, key } of candidates) {
      const entry = childCache.get(key);
      if (entry) index.set(id, entry);
    }

    if (childCache.size > CACHE_PRUNE_THRESHOLD) {
      const live = new Set(candidates.map(({ key }) => key));
      for (const key of childCache.keys()) {
        if (!live.has(key)) childCache.delete(key);
      }
    }

    return index.size === 0 ? EMPTY_INDEX : index;
  }, [candidates, version]);
}
