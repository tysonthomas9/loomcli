/**
 * useTaskSessions - React hook for fetching session records for a given task.
 * Polls at 10s normally, 3s when any session is active (running).
 * Also subscribes to SSE session_change events for immediate refetch.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { getTaskSessions } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import type { SessionRecord } from "@/types/agent";
import { useEventSubscription } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";

/** Return type for the useTaskSessions hook. */
export interface UseTaskSessionsResult {
  /** Session records for the task, newest first. */
  sessions: SessionRecord[];
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from the last fetch, null if successful. */
  error: Error | null;
  /** Manually trigger a refetch. */
  refetch: () => void;
}

/** Normal polling interval (ms). */
const POLL_INTERVAL_NORMAL = 10_000;
/** Fast polling interval when any session is active (ms). */
const POLL_INTERVAL_ACTIVE = 3_000;

export function useTaskSessions(taskId: string | null): UseTaskSessionsResult {
  const { workspaceId } = useWorkspaceContext();
  const [sessions, setSessions] = useState<SessionRecord[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions; // always keep ref in sync with latest state

  // The hook instance survives a taskId change (IssueDetailPanel renders with no
  // `key`), so refs are reused across the switch and `mountedRef` alone fences
  // nothing: React runs the old cleanup and the new effect in the same commit,
  // so it is back to true before any in-flight promise resolves. `generationRef`
  // is bumped on every (workspaceId, taskId) change; a response is only allowed
  // to write state when it still belongs to the current generation. Without it,
  // task A's late response lands under task B.
  const generationRef = useRef(0);
  // In-flight guard is per generation, not per hook: a fetch for the previous
  // task must never suppress the new task's fetch.
  const fetchInProgressRef = useRef(-1);
  // Identity the currently displayed sessions belong to, so a switch can drop
  // them without also clearing on the initial mount.
  const lastKeyRef = useRef<string | null>(null);

  const fetchData = useCallback(async () => {
    if (!taskId) return;
    const generation = generationRef.current;
    if (fetchInProgressRef.current === generation) return;
    fetchInProgressRef.current = generation;
    setIsLoading(true);

    const isCurrent = () =>
      mountedRef.current && generationRef.current === generation;

    try {
      const result = await getTaskSessions(workspaceId, taskId);
      if (isCurrent()) {
        setSessions(result);
        setError(null);
      }
    } catch (err) {
      if (isCurrent()) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (isCurrent()) {
        setIsLoading(false);
      }
      if (fetchInProgressRef.current === generation) {
        fetchInProgressRef.current = -1;
      }
    }
  }, [workspaceId, taskId]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  // Subscribe to SSE session_change events for immediate refetch.
  // Keeps polling as a fallback in case the SSE connection drops.
  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (mutation.type !== "session_change") return;
      // Only refetch when the event is for our task (or no task filter)
      if (taskId && mutation.issue_id && mutation.issue_id !== taskId) return;
      void fetchData();
    },
    [taskId, fetchData],
  );

  useEventSubscription(handleMutation, {
    types: ["session_change"],
  });

  useEffect(() => {
    mountedRef.current = true;
    // Open a new generation: every response issued for a previous (workspace,
    // task) pair is now stale and must not write state.
    generationRef.current += 1;
    const generation = generationRef.current;

    // Drop the previous task's rows straight away. Consumers (SessionsTab,
    // IssueDetailPanel's failed-run banner, TaskSessionDiffPane) render
    // `sessions` without re-filtering on task_id, so leaving them in place
    // shows task A's runs, cost summary and failure banner under task B for the
    // whole duration of task B's fetch.
    const key = `${workspaceId}\u0000${taskId ?? ""}`;
    if (lastKeyRef.current !== null && lastKeyRef.current !== key) {
      setSessions([]);
      setError(null);
    }
    lastKeyRef.current = key;

    if (!taskId) {
      setSessions([]);
      setError(null);
      return;
    }

    void fetchData();

    const hasActive = () => sessionsRef.current.some((s) => s.is_active);

    const getPollInterval = () =>
      hasActive() ? POLL_INTERVAL_ACTIVE : POLL_INTERVAL_NORMAL;

    // Use setTimeout chain so interval adapts to active state changes
    let timer: ReturnType<typeof setTimeout> | null = null;

    const scheduleNext = () => {
      // Generation-checked as well as mount-checked: an in-flight poll from the
      // previous task resolves after this effect re-ran, and would otherwise
      // schedule a timer that this effect's cleanup never clears.
      if (!mountedRef.current || generationRef.current !== generation) return;
      timer = setTimeout(() => {
        void fetchData().then(scheduleNext);
      }, getPollInterval());
    };

    scheduleNext();

    return () => {
      mountedRef.current = false;
      if (timer) clearTimeout(timer);
    };
  }, [workspaceId, taskId, fetchData]);

  return { sessions, isLoading, error, refetch };
}
