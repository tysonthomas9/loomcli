import { useCallback, useEffect, useRef, useState } from "react";

import {
  listAgentActivity,
  listAgentRuns,
  type AgentActivity,
  type AgentHistorySession,
  type AgentRunsResponse,
} from "@/api/agents";
import type { WorkflowRun } from "@/api/workflows";

export interface UseAgentHistoryResult {
  runs: WorkflowRun[];
  sessions: AgentHistorySession[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const HISTORY_POLL_INTERVAL_MS = 3_000;

function emptyHistory(agentId: string | null): AgentRunsResponse {
  return {
    agent_id: agentId ?? "",
    runs: [],
    sessions: [],
  };
}

interface AgentHistoryState {
  key: string;
  response: AgentRunsResponse;
  isLoading: boolean;
  error: Error | null;
}

function sessionFromActivity(activity: AgentActivity): AgentHistorySession {
  const updatedAt = activity.finished_at ?? activity.started_at;
  return {
    workspace_key: activity.workspace_key,
    session_id: activity.source_id,
    agent_id: activity.agent_id,
    kind: "interactive",
    ...(activity.task_id ? { task_id: activity.task_id } : {}),
    status: activity.status as AgentHistorySession["status"],
    started_at: activity.started_at,
    ...(activity.finished_at ? { finished_at: activity.finished_at } : {}),
    ...(activity.summary ? { summary: activity.summary } : {}),
    created_at: activity.started_at,
    updated_at: updatedAt,
  };
}

function mergeSessions(
  canonical: AgentHistorySession[],
  activity: AgentHistorySession[],
): AgentHistorySession[] {
  const byID = new Map<string, AgentHistorySession>();
  for (const session of canonical) byID.set(session.session_id, session);
  for (const session of activity) byID.set(session.session_id, session);
  return [...byID.values()].sort((left, right) => {
    const leftTime = left.updated_at || left.started_at || left.created_at;
    const rightTime = right.updated_at || right.started_at || right.created_at;
    return rightTime.localeCompare(leftTime);
  });
}

function rejectionError(result: PromiseRejectedResult): Error {
  return result.reason instanceof Error
    ? result.reason
    : new Error(String(result.reason));
}

/** Fetch unified workflow-run or interactive-session history for one agent. */
export function useAgentHistory(
  workspaceId: string,
  agentId: string | null,
  enabled = true,
): UseAgentHistoryResult {
  const requestKey =
    enabled && workspaceId && agentId ? `${workspaceId}\u0000${agentId}` : "";
  const [state, setState] = useState<AgentHistoryState>({
    key: "",
    response: emptyHistory(null),
    isLoading: false,
    error: null,
  });
  const generationRef = useRef(0);
  const inFlightGenerationRef = useRef<number | null>(null);

  const fetchData = useCallback(async () => {
    if (!requestKey || !agentId) return;
    const generation = generationRef.current;
    // A new route generation may start immediately while an old route request
    // drains, but never overlap requests for the same visible agent.
    if (inFlightGenerationRef.current === generation) return;
    inFlightGenerationRef.current = generation;
    setState((current) => ({
      key: requestKey,
      response:
        current.key === requestKey ? current.response : emptyHistory(agentId),
      isLoading: true,
      error: current.key === requestKey ? current.error : null,
    }));
    try {
      const [runsResult, activityResult] = await Promise.allSettled([
        listAgentRuns(workspaceId, agentId, { limit: 25 }),
        listAgentActivity(workspaceId, agentId, { limit: 25 }),
      ]);
      const runs =
        runsResult.status === "fulfilled"
          ? runsResult.value
          : emptyHistory(agentId);
      const activityItems =
        activityResult.status === "fulfilled" &&
        Array.isArray(activityResult.value.activity)
          ? activityResult.value.activity
          : [];
      const activitySessions = activityItems
        .filter((item) => item.kind === "agent_session")
        .map(sessionFromActivity);
      const sessions = mergeSessions(runs.sessions ?? [], activitySessions);

      // `/runs` is the established history surface and `/activity` is an
      // additive Interaction projection. Keep existing DriverRun history
      // usable against an older or temporarily degraded activity route. If
      // `/runs` is the failed source, only suppress its error when Interaction
      // still supplied real session history rather than a misleading empty
      // state.
      if (runsResult.status === "rejected" && sessions.length === 0) {
        throw rejectionError(runsResult);
      }
      const next: AgentRunsResponse = {
        ...runs,
        sessions,
      };
      if (generation !== generationRef.current) {
        return;
      }
      setState({
        key: requestKey,
        response: next,
        isLoading: false,
        error: null,
      });
    } catch (err) {
      if (generation !== generationRef.current) {
        return;
      }
      setState((current) => ({
        key: requestKey,
        response:
          current.key === requestKey ? current.response : emptyHistory(agentId),
        isLoading: false,
        error: err instanceof Error ? err : new Error(String(err)),
      }));
    } finally {
      if (inFlightGenerationRef.current === generation) {
        inFlightGenerationRef.current = null;
      }
    }
  }, [agentId, requestKey, workspaceId]);

  useEffect(() => {
    generationRef.current += 1;
    setState({
      key: requestKey,
      response: emptyHistory(agentId),
      isLoading: Boolean(requestKey),
      error: null,
    });
    if (!requestKey) return;
    // Poll even when history is empty or terminal. Otherwise a session that
    // starts after the first read never becomes visible on an already-open
    // tab. Chain each poll after completion so slow reads cannot overlap.
    let cancelled = false;
    let timer: number | null = null;
    const poll = async () => {
      await fetchData();
      if (cancelled) return;
      timer = window.setTimeout(() => void poll(), HISTORY_POLL_INTERVAL_MS);
    };
    void poll();
    return () => {
      cancelled = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [agentId, fetchData, requestKey]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  // Effects reset state after commit. Mask a previous route's response during
  // the intervening render so it cannot trigger a transcript read for the
  // newly selected agent.
  const visible =
    requestKey && state.key === requestKey
      ? state
      : {
          key: requestKey,
          response: emptyHistory(agentId),
          isLoading: Boolean(requestKey),
          error: null,
        };

  return {
    runs: visible.response.runs,
    sessions: visible.response.sessions,
    isLoading: visible.isLoading,
    error: visible.error,
    refetch,
  };
}
