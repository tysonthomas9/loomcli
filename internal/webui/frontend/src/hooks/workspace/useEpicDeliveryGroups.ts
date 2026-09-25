/**
 * Read-only: active delivery groups linked to one epic (STACKED-PRS-7).
 *
 * Pages `listDeliveryGroups({ epicId })` on `has_more`/`next_cursor` only when
 * asked (Load more). Members without a decorated readiness view get one batched
 * readiness read; failures there leave members "Not observed" rather than
 * blanking the groups. Never writes and never infers groups.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  classifyDeliveryGroupWriteError,
  listDeliveryGroups,
  type DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import {
  fetchPullRequestReadiness,
  type PullRequestReadinessView,
} from "@/api/workspace/pullRequests";
import { prKeysMissingReadiness } from "@/utils/pullRequest/epicDeliveryLineage";
import { useWorkspaceContext } from "./useWorkspaceContext";

/** Server caps `?pr=` keys per readiness read at 100. */
const READINESS_BATCH = 100;

export interface EpicDeliveryGroupsError {
  kind: "unavailable" | "error";
  message: string;
}

export interface UseEpicDeliveryGroupsReturn {
  groups: DeliveryGroupView[];
  /** Readiness read separately for members that arrived undecorated. */
  extraReadiness: ReadonlyMap<string, PullRequestReadinessView>;
  warnings: string[];
  hasMore: boolean;
  loading: boolean;
  loadingMore: boolean;
  error: EpicDeliveryGroupsError | null;
  /** Error from a Load more page; already-loaded groups stay visible. */
  loadMoreError: EpicDeliveryGroupsError | null;
  loadMore: () => void;
}

function toError(err: unknown): EpicDeliveryGroupsError {
  const classified = classifyDeliveryGroupWriteError(err);
  return {
    kind: classified.kind === "unavailable" ? "unavailable" : "error",
    message: classified.message,
  };
}

export function useEpicDeliveryGroups(
  epicId: string,
  pageSize = 20,
): UseEpicDeliveryGroupsReturn {
  const { workspaceId } = useWorkspaceContext();
  const [groups, setGroups] = useState<DeliveryGroupView[]>([]);
  const [warnings, setWarnings] = useState<string[]>([]);
  const [cursor, setCursor] = useState<string | undefined>(undefined);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<EpicDeliveryGroupsError | null>(null);
  const [loadMoreError, setLoadMoreError] =
    useState<EpicDeliveryGroupsError | null>(null);
  const [extraReadiness, setExtraReadiness] = useState<
    Map<string, PullRequestReadinessView>
  >(() => new Map());
  // Bumped per (workspace, epic) so late responses for a previous epic are dropped.
  const generation = useRef(0);
  const requested = useRef<Set<string>>(new Set());

  useEffect(() => {
    const gen = ++generation.current;
    requested.current = new Set();
    setGroups([]);
    setWarnings([]);
    setCursor(undefined);
    setHasMore(false);
    setError(null);
    setLoadMoreError(null);
    setExtraReadiness(new Map());
    if (!workspaceId || !epicId) {
      setLoading(false);
      return;
    }
    setLoading(true);
    listDeliveryGroups(workspaceId, {
      epicId,
      state: "active",
      limit: pageSize,
    })
      .then((page) => {
        if (gen !== generation.current) return;
        setGroups(page.delivery_groups);
        setWarnings(page.warnings ?? []);
        setHasMore(page.has_more);
        setCursor(page.next_cursor);
      })
      .catch((err: unknown) => {
        if (gen !== generation.current) return;
        setError(toError(err));
      })
      .finally(() => {
        if (gen === generation.current) setLoading(false);
      });
  }, [workspaceId, epicId, pageSize]);

  const loadMore = useCallback(() => {
    if (!hasMore || !cursor || loadingMore) return;
    const gen = generation.current;
    setLoadingMore(true);
    setLoadMoreError(null);
    listDeliveryGroups(workspaceId, {
      epicId,
      state: "active",
      limit: pageSize,
      cursor,
    })
      .then((page) => {
        if (gen !== generation.current) return;
        setGroups((prev) => {
          const seen = new Set(prev.map((g) => g.id));
          return [
            ...prev,
            ...page.delivery_groups.filter((g) => !seen.has(g.id)),
          ];
        });
        if (page.warnings?.length) {
          setWarnings((prev) => [...new Set([...prev, ...page.warnings!])]);
        }
        setHasMore(page.has_more);
        setCursor(page.next_cursor);
      })
      .catch((err: unknown) => {
        if (gen !== generation.current) return;
        setLoadMoreError(toError(err));
      })
      .finally(() => {
        if (gen === generation.current) setLoadingMore(false);
      });
  }, [workspaceId, epicId, pageSize, cursor, hasMore, loadingMore]);

  // Fill in readiness for members the list returned without a decorated view.
  useEffect(() => {
    const missing = prKeysMissingReadiness(groups, extraReadiness).filter(
      (key) => !requested.current.has(key),
    );
    if (!workspaceId || missing.length === 0) return;
    for (const key of missing) requested.current.add(key);
    const gen = generation.current;
    for (let i = 0; i < missing.length; i += READINESS_BATCH) {
      const batch = missing.slice(i, i + READINESS_BATCH);
      fetchPullRequestReadiness(workspaceId, batch)
        .then((list) => {
          if (gen !== generation.current) return;
          setExtraReadiness((prev) => {
            const next = new Map(prev);
            for (const view of list.pull_requests) next.set(view.pr_key, view);
            return next;
          });
        })
        .catch(() => {
          // Leave these members "Not observed"; groups stay visible.
        });
    }
  }, [groups, extraReadiness, workspaceId]);

  return {
    groups,
    extraReadiness,
    warnings,
    hasMore,
    loading,
    loadingMore,
    error,
    loadMoreError,
    loadMore,
  };
}
