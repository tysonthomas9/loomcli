import { useCallback, useEffect, useRef, useState } from "react";

import {
  getAgentServiceJournal,
  getDriverRunLog,
  getTaskRunLog,
  getTaskRunTranscript,
  listAgentServiceRunTasks,
  listAgentServiceRuns,
  listAgentServices,
  listRunEvents,
  type AgentServiceDTO,
  type AgentServiceJournalDTO,
  type AgentServiceList,
  type DriverRunDTO,
  type PersistedLogDTO,
  type RunEventDTO,
  type TaskRunDTO,
} from "@/api/agentServices";
import type { TranscriptEntry } from "@/types/agent";
import { ApiError } from "@/types/common";

const DEFAULT_POLL_INTERVAL = 30_000;
const DEFAULT_RUN_LIMIT = 20;

interface PolledListState<T> {
  key: string;
  items: T[];
  total: number;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
}

interface UsePolledListOptions {
  enabled: boolean;
  pollInterval: number;
}

interface UsePolledListResult<T> {
  items: T[];
  total: number;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  notFound: boolean;
  refresh: () => Promise<void>;
}

function emptyListState<T>(key: string): PolledListState<T> {
  return {
    key,
    items: [],
    total: 0,
    loading: false,
    initialized: false,
    error: null,
  };
}

function usePolledList<T>(
  key: string,
  load: () => Promise<AgentServiceList<T>>,
  { enabled, pollInterval }: UsePolledListOptions,
): UsePolledListResult<T> {
  const [state, setState] = useState<PolledListState<T>>(() =>
    emptyListState(key),
  );
  const activeKeyRef = useRef(key);
  const requestSequenceRef = useRef(0);
  const inFlightKeyRef = useRef<string | null>(null);
  activeKeyRef.current = key;

  const refresh = useCallback(async () => {
    if (!enabled || !key || inFlightKeyRef.current === key) return;
    const requestKey = key;
    const requestSequence = ++requestSequenceRef.current;
    inFlightKeyRef.current = requestKey;
    setState((current) => ({
      ...(current.key === requestKey ? current : emptyListState<T>(requestKey)),
      loading: true,
    }));

    try {
      const result = await load();
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState({
        key: requestKey,
        items: result.data,
        total: result.total,
        loading: false,
        initialized: true,
        error: null,
      });
    } catch (error) {
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState((current) => ({
        ...(current.key === requestKey
          ? current
          : emptyListState<T>(requestKey)),
        loading: false,
        initialized: true,
        error: error instanceof Error ? error : new Error(String(error)),
      }));
    } finally {
      if (inFlightKeyRef.current === requestKey) {
        inFlightKeyRef.current = null;
      }
    }
  }, [enabled, key, load]);

  useEffect(() => {
    setState(emptyListState(key));
  }, [key]);

  useEffect(() => {
    if (!enabled || !key) return;

    const requestSequence = requestSequenceRef;
    const inFlightKey = inFlightKeyRef;
    let stopped = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;
    const clearPoll = (): void => {
      if (timeoutId !== null) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    };
    const schedulePoll = (): void => {
      clearPoll();
      if (stopped || document.hidden || pollInterval <= 0) return;
      timeoutId = setTimeout(() => {
        void runPoll();
      }, pollInterval);
    };
    const runPoll = async (): Promise<void> => {
      await refresh();
      schedulePoll();
    };
    const handleVisibilityChange = (): void => {
      clearPoll();
      if (!document.hidden) void runPoll();
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    if (!document.hidden) void runPoll();
    return () => {
      stopped = true;
      clearPoll();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      requestSequence.current++;
      inFlightKey.current = null;
    };
  }, [enabled, key, pollInterval, refresh]);

  const scopedState = state.key === key ? state : emptyListState<T>(key);
  return {
    items: scopedState.items,
    total: scopedState.total,
    loading: scopedState.loading,
    initialized: scopedState.initialized,
    error: scopedState.error,
    notFound:
      scopedState.error instanceof ApiError && scopedState.error.status === 404,
    refresh,
  };
}

export interface UseAgentServicesOptions {
  enabled?: boolean;
  pollInterval?: number;
}

export interface UseAgentServicesReturn {
  services: AgentServiceDTO[];
  total: number;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

export function useAgentServices(
  workspaceId: string,
  options: UseAgentServicesOptions = {},
): UseAgentServicesReturn {
  const enabled = options.enabled ?? Boolean(workspaceId);
  const pollInterval = options.pollInterval ?? DEFAULT_POLL_INTERVAL;
  const load = useCallback(() => listAgentServices(workspaceId), [workspaceId]);
  const result = usePolledList(workspaceId, load, { enabled, pollInterval });
  return {
    services: result.items,
    total: result.total,
    loading: result.loading,
    initialized: result.initialized,
    error: result.error,
    refresh: result.refresh,
  };
}

export interface UseAgentServiceRunsOptions {
  enabled?: boolean;
  limit?: number;
  pollInterval?: number;
}

export interface UseAgentServiceRunsReturn {
  runs: DriverRunDTO[];
  total: number;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  notFound: boolean;
  refresh: () => Promise<void>;
}

export function useAgentServiceRuns(
  workspaceId: string,
  agentServiceId: string,
  options: UseAgentServiceRunsOptions = {},
): UseAgentServiceRunsReturn {
  const limit = options.limit ?? DEFAULT_RUN_LIMIT;
  const enabled =
    options.enabled ?? Boolean(workspaceId && agentServiceId && limit > 0);
  const pollInterval = options.pollInterval ?? DEFAULT_POLL_INTERVAL;
  const key = `${workspaceId}\0${agentServiceId}\0${limit}`;
  const load = useCallback(
    () => listAgentServiceRuns(workspaceId, agentServiceId, limit),
    [agentServiceId, limit, workspaceId],
  );
  const result = usePolledList(key, load, { enabled, pollInterval });
  return {
    runs: result.items,
    total: result.total,
    loading: result.loading,
    initialized: result.initialized,
    error: result.error,
    notFound: result.notFound,
    refresh: result.refresh,
  };
}

export interface UseAgentServiceRunEventsReturn {
  events: RunEventDTO[];
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

export function useAgentServiceRunEvents(
  workspaceId: string,
  runId: string | null,
): UseAgentServiceRunEventsReturn {
  const enabled = Boolean(workspaceId && runId);
  const key = `${workspaceId}\0${runId ?? ""}`;
  const load = useCallback(async (): Promise<AgentServiceList<RunEventDTO>> => {
    if (!runId) return { data: [], total: 0 };
    const page = await listRunEvents(workspaceId, runId);
    return { data: page.events, total: page.events.length };
  }, [runId, workspaceId]);
  const result = usePolledList(key, load, { enabled, pollInterval: 0 });
  return {
    events: result.items,
    loading: result.loading,
    initialized: result.initialized,
    error: result.error,
    refresh: result.refresh,
  };
}

export interface UseAgentServiceRunTasksReturn {
  tasks: TaskRunDTO[];
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

export function useAgentServiceRunTasks(
  workspaceId: string,
  agentServiceId: string,
  runId: string | null,
): UseAgentServiceRunTasksReturn {
  const enabled = Boolean(workspaceId && agentServiceId && runId);
  const key = workspaceId + "\0" + agentServiceId + "\0" + (runId ?? "");
  const load = useCallback(async (): Promise<AgentServiceList<TaskRunDTO>> => {
    if (!runId) return { data: [], total: 0 };
    return listAgentServiceRunTasks(workspaceId, agentServiceId, runId);
  }, [agentServiceId, runId, workspaceId]);
  const result = usePolledList(key, load, { enabled, pollInterval: 0 });
  return {
    tasks: result.items,
    loading: result.loading,
    initialized: result.initialized,
    error: result.error,
    refresh: result.refresh,
  };
}

interface LazyResourceState<T> {
  key: string;
  data: T | null;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
}

export interface UsePersistedLogReturn {
  log: PersistedLogDTO | null;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

interface UseLazyResourceReturn<T> {
  data: T | null;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

function emptyLazyResourceState<T>(key: string): LazyResourceState<T> {
  return { key, data: null, loading: false, initialized: false, error: null };
}

function useLazyResource<T>(
  key: string,
  enabled: boolean,
  load: () => Promise<T>,
): UseLazyResourceReturn<T> {
  const activeKeyRef = useRef(key);
  const requestSequenceRef = useRef(0);
  const inFlightKeyRef = useRef<string | null>(null);
  const [state, setState] = useState<LazyResourceState<T>>(() =>
    emptyLazyResourceState<T>(key),
  );
  activeKeyRef.current = key;

  const refresh = useCallback(async (): Promise<void> => {
    if (!enabled || !key || inFlightKeyRef.current === key) return;
    const requestKey = key;
    const requestSequence = ++requestSequenceRef.current;
    inFlightKeyRef.current = requestKey;
    setState((current) => ({
      ...(current.key === requestKey
        ? current
        : {
            key: requestKey,
            data: null,
            initialized: false,
            error: null,
          }),
      loading: true,
    }));
    try {
      const data = await load();
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState({
        key: requestKey,
        data,
        loading: false,
        initialized: true,
        error: null,
      });
    } catch (error) {
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState({
        key: requestKey,
        data: null,
        loading: false,
        initialized: true,
        error: error instanceof Error ? error : new Error(String(error)),
      });
    } finally {
      if (inFlightKeyRef.current === requestKey) {
        inFlightKeyRef.current = null;
      }
    }
  }, [enabled, key, load]);

  useEffect(() => {
    const requestSequence = requestSequenceRef;
    const inFlightKey = inFlightKeyRef;
    setState(emptyLazyResourceState<T>(key));
    return () => {
      requestSequence.current++;
      inFlightKey.current = null;
    };
  }, [key]);

  useEffect(() => {
    if (enabled) void refresh();
  }, [enabled, refresh]);

  const scoped =
    state.key === key
      ? state
      : emptyLazyResourceState<T>(key);
  return {
    data: scoped.data,
    loading: scoped.loading,
    initialized: scoped.initialized,
    error: scoped.error,
    refresh,
  };
}

export function useTaskRunLog(
  workspaceId: string,
  taskRunId: string,
  options: { enabled?: boolean } = {},
): UsePersistedLogReturn {
  const enabled = options.enabled ?? Boolean(workspaceId && taskRunId);
  const key = workspaceId + "\0task\0" + taskRunId;
  const load = useCallback(
    () => getTaskRunLog(workspaceId, taskRunId),
    [taskRunId, workspaceId],
  );
  const resource = useLazyResource(key, enabled, load);
  return { ...resource, log: resource.data };
}

export interface UseTaskRunTranscriptReturn {
  entries: TranscriptEntry[];
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

export function useTaskRunTranscript(
  workspaceId: string,
  taskRunId: string,
  options: { enabled?: boolean } = {},
): UseTaskRunTranscriptReturn {
  const enabled = options.enabled ?? Boolean(workspaceId && taskRunId);
  const key = workspaceId + "\0transcript\0" + taskRunId;
  const load = useCallback(
    () => getTaskRunTranscript(workspaceId, taskRunId),
    [taskRunId, workspaceId],
  );
  const resource = useLazyResource(key, enabled, load);
  return { ...resource, entries: resource.data ?? [] };
}

export function useDriverRunLog(
  workspaceId: string,
  runId: string,
  options: { enabled?: boolean } = {},
): UsePersistedLogReturn {
  const enabled = options.enabled ?? Boolean(workspaceId && runId);
  const key = workspaceId + "\0run\0" + runId;
  const load = useCallback(
    () => getDriverRunLog(workspaceId, runId),
    [runId, workspaceId],
  );
  const resource = useLazyResource(key, enabled, load);
  return { ...resource, log: resource.data };
}

interface AgentServiceJournalState {
  key: string;
  journal: AgentServiceJournalDTO | null;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
}

export interface UseAgentServiceJournalReturn {
  journal: AgentServiceJournalDTO | null;
  loading: boolean;
  initialized: boolean;
  error: Error | null;
  refresh: () => Promise<void>;
}

export function useAgentServiceJournal(
  workspaceId: string,
  agentServiceId: string,
  options: { enabled?: boolean } = {},
): UseAgentServiceJournalReturn {
  const enabled = options.enabled ?? true;
  const key = `${workspaceId}\0${agentServiceId}`;
  const activeKeyRef = useRef(key);
  const requestSequenceRef = useRef(0);
  const [state, setState] = useState<AgentServiceJournalState>(() => ({
    key,
    journal: null,
    loading: false,
    initialized: false,
    error: null,
  }));
  activeKeyRef.current = key;

  const refresh = useCallback(async (): Promise<void> => {
    if (!workspaceId || !agentServiceId) return;
    const requestKey = key;
    const requestSequence = ++requestSequenceRef.current;
    setState((current) => ({
      ...(current.key === requestKey
        ? current
        : {
            key: requestKey,
            journal: null,
            initialized: false,
            error: null,
          }),
      loading: true,
    }));
    try {
      const journal = await getAgentServiceJournal(workspaceId, agentServiceId);
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState({
        key: requestKey,
        journal,
        loading: false,
        initialized: true,
        error: null,
      });
    } catch (error) {
      if (
        activeKeyRef.current !== requestKey ||
        requestSequence !== requestSequenceRef.current
      ) {
        return;
      }
      setState({
        key: requestKey,
        journal: null,
        loading: false,
        initialized: true,
        error: error instanceof Error ? error : new Error(String(error)),
      });
    }
  }, [agentServiceId, key, workspaceId]);

  useEffect(() => {
    const requestSequence = requestSequenceRef;
    setState({
      key,
      journal: null,
      loading: false,
      initialized: false,
      error: null,
    });
    return () => {
      requestSequence.current++;
    };
  }, [key]);

  useEffect(() => {
    if (!enabled) return;
    void refresh();
  }, [enabled, refresh]);

  const scopedState =
    state.key === key
      ? state
      : {
          key,
          journal: null,
          loading: false,
          initialized: false,
          error: null,
        };
  return {
    journal: scopedState.journal,
    loading: scopedState.loading,
    initialized: scopedState.initialized,
    error: scopedState.error,
    refresh,
  };
}
