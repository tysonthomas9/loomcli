/**
 * Bounded direct lookup for a named delivery group deep-link target.
 *
 * Used by `/prs?group=G&pr=P` so StackedPRWorkspace can resolve G via
 * `getDeliveryGroup` when it is absent from the first list page — never by
 * client-side paging or inference. Prefer a page-list hit when present.
 */

import { useEffect, useMemo, useRef, useState } from "react";

import {
  classifyDeliveryGroupWriteError,
  getDeliveryGroup,
  type DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import { useWorkspaceContext } from "./useWorkspaceContext";

export type FocusedDeliveryGroupErrorKind =
  | "not_found"
  | "unavailable"
  | "error";

export interface FocusedDeliveryGroupError {
  kind: FocusedDeliveryGroupErrorKind;
  message: string;
}

export interface UseFocusedDeliveryGroupReturn {
  /** Resolved group from the page list or a direct get. */
  group: DeliveryGroupView | null;
  /** True while a direct get is in flight for a missing page hit. */
  loading: boolean;
  error: FocusedDeliveryGroupError | null;
}

function toError(err: unknown): FocusedDeliveryGroupError {
  const classified = classifyDeliveryGroupWriteError(err);
  if (classified.kind === "not_found") {
    return { kind: "not_found", message: classified.message };
  }
  if (classified.kind === "unavailable") {
    return { kind: "unavailable", message: classified.message };
  }
  return { kind: "error", message: classified.message };
}

export function useFocusedDeliveryGroup(
  groupId: string | undefined,
  pageGroups: readonly DeliveryGroupView[],
): UseFocusedDeliveryGroupReturn {
  const { workspaceId } = useWorkspaceContext();
  // Depend on id presence, not the group object identity (page arrays churn).
  const pageHasGroup = Boolean(
    groupId && pageGroups.some((g) => g.id === groupId),
  );
  const pageHit = useMemo(
    () =>
      groupId && pageHasGroup
        ? (pageGroups.find((g) => g.id === groupId) ?? null)
        : null,
    [groupId, pageHasGroup, pageGroups],
  );
  const [fetched, setFetched] = useState<DeliveryGroupView | null>(null);
  // Start loading when a named group may need a direct get so consumers do not
  // treat the first render (pre-effect) as a resolved miss.
  const [loading, setLoading] = useState(() => Boolean(groupId));
  const [error, setError] = useState<FocusedDeliveryGroupError | null>(null);
  const seqRef = useRef(0);

  useEffect(() => {
    const seq = ++seqRef.current;
    setFetched(null);
    setError(null);

    if (!groupId || !workspaceId) {
      setLoading(false);
      return;
    }
    if (pageHasGroup) {
      setLoading(false);
      return;
    }

    setLoading(true);
    getDeliveryGroup(workspaceId, groupId)
      .then((write) => {
        if (seq !== seqRef.current) return;
        setFetched(write.group);
        setError(null);
      })
      .catch((err: unknown) => {
        if (seq !== seqRef.current) return;
        setFetched(null);
        setError(toError(err));
      })
      .finally(() => {
        if (seq === seqRef.current) setLoading(false);
      });

    return () => {
      // Invalidate with the captured seq (avoid cleanup-time .current read).
      seqRef.current = seq + 1;
    };
  }, [workspaceId, groupId, pageHasGroup]);

  return {
    group: pageHit ?? fetched,
    loading: Boolean(groupId) && !pageHasGroup && loading,
    error: pageHasGroup ? null : error,
  };
}
