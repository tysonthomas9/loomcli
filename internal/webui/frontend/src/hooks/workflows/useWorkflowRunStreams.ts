/**
 * useWorkflowRunStreams — live status updates for active workflow runs.
 *
 * Subscribes to the per-run SSE endpoint
 * `GET /api/workspaces/{ws}/runs/{runId}/stream`, which emits `event: event`
 * frames containing run lifecycle events (PlatformEvent objects). The
 * fleet-db store appends `driver_run.create|claim|heartbeat|finish|recover`
 * events whose `after` field is a JSON-encoded map that includes the run's
 * `status`, so status transitions (including terminal ones) arrive over the
 * stream without client-side polling.
 *
 * Behavior per active (non-terminal) run, capped at MAX_RUN_STREAMS:
 * - On a status-transition event: one `getWorkflowRun` fetch for the
 *   canonical run object, delivered via `onRunUpdate`.
 * - On a heartbeat event (status unchanged): the run's `last_heartbeat` is
 *   patched locally — no network call.
 * - On a server `event: error` frame or a transport error: the stream is
 *   closed, a single `getWorkflowRun` refresh keeps the UI fresh, and the
 *   stream is re-established after a delay while the run is still active.
 *
 * Fallback to slow polling (FALLBACK_POLL_MS) when:
 * - `EventSource` is unavailable (older runtimes, jsdom tests);
 * - an auth token is set — the webui's auth middleware accepts Bearer
 *   headers only, which `EventSource` cannot send; or
 * - the run is beyond the MAX_RUN_STREAMS cap.
 *
 * Streams are torn down on unmount and whenever a run leaves the active set
 * (terminal status or removal).
 */

import { useEffect, useMemo, useRef } from "react";

import {
  getWorkflowRun,
  isTerminalWorkflowRunStatus,
  type WorkflowRun,
  type WorkflowRunStatus,
} from "@/api";
import { getApiOrigin, getAuthToken, wsUrl } from "@/api/common";

/** Maximum number of concurrent EventSource connections. */
const MAX_RUN_STREAMS = 8;
/** Delay before re-establishing a stream after an error. */
const STREAM_RETRY_MS = 5_000;
/** Poll cadence for runs that cannot use SSE. */
const FALLBACK_POLL_MS = 5_000;

/** Separators for the active-run identity key (never appear in IDs). */
const PAIR_SEP = "\u0001";
const FIELD_SEP = "\u0000";

/** Shape of the run lifecycle events the stream delivers (PlatformEvent). */
interface RunStreamEventFrame {
  action?: string;
  entity_type?: string;
  entity_id?: string;
  /** JSON-encoded map of run state; includes `status`, `last_heartbeat`. */
  after?: string;
}

export interface UseWorkflowRunStreamsOptions {
  workspaceId: string;
  /** Map of epicId -> run. Only non-terminal runs with a run_id stream. */
  runs: Record<string, WorkflowRun>;
  /** Called with the refreshed run whenever its state changes. */
  onRunUpdate: (epicId: string, run: WorkflowRun) => void;
}

export function workflowRunStreamUrl(
  workspaceId: string,
  runId: string,
): string {
  return `${getApiOrigin()}${wsUrl(
    workspaceId,
    `/runs/${encodeURIComponent(runId)}/stream`,
  )}`;
}

export function useWorkflowRunStreams({
  workspaceId,
  runs,
  onRunUpdate,
}: UseWorkflowRunStreamsOptions): void {
  // Latest-value refs so the subscription effect does not tear down and
  // rebuild streams on every runs-map or callback identity change.
  const runsRef = useRef(runs);
  runsRef.current = runs;
  const onRunUpdateRef = useRef(onRunUpdate);
  onRunUpdateRef.current = onRunUpdate;

  // Identity of the active run set. Streams are (re)built only when an
  // active run appears or disappears, not on status/heartbeat updates of
  // runs that stay active.
  const activeRunsKey = useMemo(
    () =>
      Object.entries(runs)
        .filter(
          ([, run]) => run.run_id && !isTerminalWorkflowRunStatus(run.status),
        )
        .map(([epicId, run]) => `${epicId}${FIELD_SEP}${run.run_id}`)
        .sort()
        .join(PAIR_SEP),
    [runs],
  );

  useEffect(() => {
    if (!workspaceId || activeRunsKey === "") return;

    const entries = activeRunsKey.split(PAIR_SEP).map((pair) => {
      const sep = pair.indexOf(FIELD_SEP);
      return { epicId: pair.slice(0, sep), runId: pair.slice(sep + 1) };
    });

    let cancelled = false;
    const sources = new Set<EventSource>();
    const pollIntervals = new Set<number>();
    const retryTimers = new Set<number>();
    /** Run IDs with a getWorkflowRun call currently in flight. */
    const inFlight = new Set<string>();
    /** Last status delivered to the parent, per run ID. */
    const lastStatus = new Map<string, WorkflowRunStatus | undefined>();
    for (const { epicId, runId } of entries) {
      lastStatus.set(runId, runsRef.current[epicId]?.status);
    }

    const isRunActive = (epicId: string): boolean => {
      const run = runsRef.current[epicId];
      return Boolean(run?.run_id) && !isTerminalWorkflowRunStatus(run?.status);
    };

    /** Single canonical refresh; skipped while a previous one is in flight. */
    const refresh = async (epicId: string, runId: string): Promise<void> => {
      if (cancelled || inFlight.has(runId)) return;
      inFlight.add(runId);
      try {
        const run = await getWorkflowRun(workspaceId, runId);
        if (cancelled) return;
        lastStatus.set(runId, run.status);
        onRunUpdateRef.current(epicId, run);
      } catch {
        // Transient failure: retry shortly so a missed terminal transition
        // (e.g. a finish event whose refresh failed) cannot strand the run.
        if (cancelled) return;
        const timer = window.setTimeout(() => {
          retryTimers.delete(timer);
          if (!cancelled && isRunActive(epicId)) void refresh(epicId, runId);
        }, STREAM_RETRY_MS);
        retryTimers.add(timer);
      } finally {
        inFlight.delete(runId);
      }
    };

    const startPolling = (epicId: string, runId: string): void => {
      const interval = window.setInterval(() => {
        if (!isRunActive(epicId)) return;
        void refresh(epicId, runId);
      }, FALLBACK_POLL_MS);
      pollIntervals.add(interval);
    };

    const handleFrame = (epicId: string, runId: string, raw: string): void => {
      let frame: RunStreamEventFrame;
      try {
        frame = JSON.parse(raw) as RunStreamEventFrame;
      } catch {
        return;
      }
      if (frame.entity_type !== "driver_run" || frame.entity_id !== runId) {
        return;
      }
      let after: Record<string, string> = {};
      if (frame.after) {
        try {
          after = JSON.parse(frame.after) as Record<string, string>;
        } catch {
          // Fall through: refresh below still covers status transitions.
        }
      }
      const status = after["status"] as WorkflowRunStatus | undefined;
      if (status === undefined || status !== lastStatus.get(runId)) {
        // Status transition (or unparseable event): fetch the canonical
        // run object once instead of reconstructing it from event fields.
        void refresh(epicId, runId);
        return;
      }
      // Heartbeat with unchanged status: patch locally, no network call.
      const heartbeat = after["last_heartbeat"];
      const current = runsRef.current[epicId];
      if (
        heartbeat &&
        current &&
        current.run_id === runId &&
        current.last_heartbeat !== heartbeat
      ) {
        onRunUpdateRef.current(epicId, {
          ...current,
          last_heartbeat: heartbeat,
        });
      }
    };

    const connect = (epicId: string, runId: string): void => {
      if (cancelled) return;
      let source: EventSource;
      try {
        source = new EventSource(workflowRunStreamUrl(workspaceId, runId));
      } catch {
        startPolling(epicId, runId);
        return;
      }
      sources.add(source);
      let recovered = false;
      // Covers both server-sent `event: error` frames and transport-level
      // errors (EventSource dispatches connection failures as "error" too).
      const recover = (): void => {
        if (recovered) return;
        recovered = true;
        source.close();
        sources.delete(source);
        // Single refresh so the UI does not go stale while the stream is
        // down, then re-establish the stream if the run is still active.
        void refresh(epicId, runId);
        if (cancelled) return;
        const timer = window.setTimeout(() => {
          retryTimers.delete(timer);
          if (!cancelled && isRunActive(epicId)) connect(epicId, runId);
        }, STREAM_RETRY_MS);
        retryTimers.add(timer);
      };
      source.addEventListener("event", (e) => {
        handleFrame(epicId, runId, (e as MessageEvent<string>).data);
      });
      source.addEventListener("error", recover);
    };

    // EventSource cannot send Authorization headers, and the webui auth
    // middleware is Bearer-header-only; with a token set, poll instead.
    const canStream =
      typeof EventSource !== "undefined" && getAuthToken() === null;

    entries.forEach(({ epicId, runId }, index) => {
      if (canStream && index < MAX_RUN_STREAMS) {
        connect(epicId, runId);
      } else {
        startPolling(epicId, runId);
      }
    });

    return () => {
      cancelled = true;
      for (const source of sources) source.close();
      sources.clear();
      for (const interval of pollIntervals) window.clearInterval(interval);
      pollIntervals.clear();
      for (const timer of retryTimers) window.clearTimeout(timer);
      retryTimers.clear();
    };
  }, [workspaceId, activeRunsKey]);
}
