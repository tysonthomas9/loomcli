import {
  useCallback,
  useContext,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { getIssueEvents } from "@/api";
import { QueryRecoveryContext } from "@/hooks/common/queryRecovery";
import { useEventContext } from "@/hooks/common/useEventProvider";
import { useWorkspaceContext } from "@/hooks/workspace";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";
import type { Event, MutationPayload } from "@/types";

export const ISSUE_HISTORY_LIMIT = 200;

export interface UseIssueHistoryResult {
  events: Event[];
  error: Error | null;
  isLoading: boolean;
  refetch: () => Promise<void>;
}
interface HistoryOwner {
  workspaceId: string;
  issueId: string | null;
  enabled: boolean;
  revision: number;
  request: ScopedQueryRequest<Event[]>;
}

/** The API validates complete event records. Keep the selected issue identity
 * check here too so a substituted loader cannot publish foreign history. */
function selectedHistory(value: Event[], issueId: string): Event[] {
  if (
    !Array.isArray(value) ||
    value.some(
      (event) =>
        !event ||
        typeof event !== "object" ||
        event.issue_id !== issueId ||
        typeof event.id !== "string" ||
        !event.id,
    )
  )
    throw new Error("Invalid selected issue history");
  return value;
}
function relevantMutation(
  mutation: MutationPayload,
  owner: HistoryOwner,
): boolean {
  if (mutation.workspace_id && mutation.workspace_id !== owner.workspaceId)
    return false;
  if (mutation.type === "refresh") return true;
  if (["dependency", "dep"].includes(mutation.entity_type ?? "")) return true;
  if (mutation.issue_id === owner.issueId) return true;
  if (mutation.entity_type === "issue" && mutation.entity_id === owner.issueId)
    return true;
  // Older dependency/comment/label notifications may omit the affected issue.
  // Their local workspace history must be reread rather than declared unchanged.
  return (
    !mutation.issue_id &&
    ["comment", "label"].includes(mutation.entity_type ?? "")
  );
}

/** Ordinary bounded selected-issue history repair. Revision can be the current
 * detail object; equal timestamps do not imply an unchanged history response. */
export function useIssueHistory(
  issueId: string | null,
  revision?: unknown,
  enabled = true,
): UseIssueHistoryResult {
  const { workspaceId } = useWorkspaceContext();
  const recovery = useContext(QueryRecoveryContext);
  const { subscribe, connectionEpoch } = useEventContext();
  const committed = useRef<HistoryOwner | null>(null);
  const stateOwner = useRef<HistoryOwner | null>(null);
  const [events, setEvents] = useState<Event[]>([]);
  const [error, setError] = useState<Error | null>(null);
  const [isLoading, setLoading] = useState(false);
  const owner = useMemo(() => {
    const current = () => committed.current === candidate;
    const request = new ScopedQueryRequest<Event[]>({
      load: async (signal) => {
        if (!issueId || !enabled)
          throw new Error("Issue history scope disabled");
        return selectedHistory(
          await getIssueEvents(workspaceId, issueId, ISSUE_HISTORY_LIMIT, {
            signal,
          }),
          issueId,
        );
      },
      commit: (rows) => {
        if (!current())
          throw new DOMException("Issue history superseded", "AbortError");
        setEvents(rows);
        setError(null);
      },
      onError: (failure) => {
        if (current()) setError(failure);
      },
      onLoading: (loading) => {
        if (current()) setLoading(loading);
      },
    });
    const candidate: HistoryOwner = {
      workspaceId,
      issueId,
      enabled: enabled && !!issueId,
      revision: 0,
      request,
    };
    return candidate;
  }, [workspaceId, issueId, enabled]);
  const refetch = useCallback(async () => {
    if (committed.current !== owner || !owner.enabled) return;
    owner.revision++;
    await owner.request.run({ fresh: true }).catch(() => {});
  }, [owner]);
  // Layout effects change ownership only after commit; a suspended speculative
  // workspace render cannot abort or redirect the visible panel's requests.
  useLayoutEffect(() => {
    committed.current = owner;
    if (stateOwner.current !== owner) {
      stateOwner.current = owner;
      setEvents([]);
      setError(null);
      setLoading(false);
    }
    const unregister = owner.enabled
      ? recovery?.register(
          `issue-history:${workspaceId}:${issueId}`,
          (signal) => owner.request.run({ signal, fresh: true }),
          () => owner.revision,
        )
      : undefined;
    if (owner.enabled) void owner.request.run().catch(() => {});
    return () => {
      unregister?.();
      if (committed.current === owner) committed.current = null;
      owner.request.cancel();
    };
  }, [owner, recovery, workspaceId, issueId]);
  const previous = useRef<{
    owner: HistoryOwner;
    revision: unknown;
    epoch: number;
  } | null>(null);
  useLayoutEffect(() => {
    const last = previous.current;
    previous.current = { owner, revision, epoch: connectionEpoch };
    if (
      last?.owner === owner &&
      (!Object.is(last.revision, revision) || last.epoch !== connectionEpoch)
    )
      void refetch();
  }, [owner, revision, connectionEpoch, refetch]);
  useLayoutEffect(() => {
    if (!owner.enabled) return;
    return subscribe((mutation) => {
      if (committed.current === owner && relevantMutation(mutation, owner))
        void refetch();
    });
  }, [owner, subscribe, refetch]);
  return { events, error, isLoading, refetch };
}
