/**
 * Tracks which terminal tabs look like they are parked on a prompt.
 *
 * Sibling of useUnreadTracking, and deliberately shaped like it: per-tab
 * booleans published as a Map that the tab strip renders from. The two say
 * different things — unread means *output arrived*, waiting means *output
 * stopped and it is your turn* — so they can be true at the same time.
 *
 * Activity is recorded in refs, never state: noteOutput fires on every 16 ms
 * renderer flush and must not cause a React render. A single 1 s interval
 * re-evaluates the predicate for every tracked tab and only calls setState when
 * the result actually changed, so the steady state costs zero renders.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";

import { isWaitingForInput, WAITING_QUIET_MS } from "./waitingState";

/** How often the predicate is re-evaluated for every tracked tab. */
const WAITING_TICK_MS = 1000;

interface ActivityRecord {
  lastOutputAt: number;
  lastInputAt: number;
  hasEverOutput: boolean;
}

interface UseWaitingTrackingOptions {
  instanceRefs: React.MutableRefObject<Map<string, TerminalInstanceHandle>>;
  /** Current connection state of a tab, or undefined if it is gone. */
  getConnectionState: (tabId: string) => ConnectionState | undefined;
  /** Quiet threshold override; exists for tests. */
  quietMs?: number | undefined;
}

interface UseWaitingTrackingReturn {
  /** Tabs currently believed to be waiting. Only true entries are present. */
  tabWaiting: Map<string, boolean>;
  /** PTY output arrived for this tab. Cheap: touches refs only. */
  noteOutput: (tabId: string) => void;
  /** User input was actually delivered to this tab's PTY. */
  noteInput: (tabId: string) => void;
  /** Tab closed, disconnected, or otherwise gone. */
  clearTab: (tabId: string) => void;
}

function sameKeys(a: Map<string, boolean>, b: Map<string, boolean>): boolean {
  if (a.size !== b.size) return false;
  for (const key of a.keys()) {
    if (!b.has(key)) return false;
  }
  return true;
}

export function useWaitingTracking({
  instanceRefs,
  getConnectionState,
  quietMs = WAITING_QUIET_MS,
}: UseWaitingTrackingOptions): UseWaitingTrackingReturn {
  const [tabWaiting, setTabWaiting] = useState<Map<string, boolean>>(
    () => new Map(),
  );
  const recordsRef = useRef<Map<string, ActivityRecord>>(new Map());

  // Held in a ref so a re-created callback never restarts the interval.
  const getConnectionStateRef = useRef(getConnectionState);
  getConnectionStateRef.current = getConnectionState;
  const quietMsRef = useRef(quietMs);
  quietMsRef.current = quietMs;

  const noteOutput = useCallback((tabId: string) => {
    const records = recordsRef.current;
    const existing = records.get(tabId);
    if (existing) {
      existing.lastOutputAt = Date.now();
      existing.hasEverOutput = true;
      return;
    }
    records.set(tabId, {
      lastOutputAt: Date.now(),
      lastInputAt: 0,
      hasEverOutput: true,
    });
  }, []);

  const noteInput = useCallback((tabId: string) => {
    const records = recordsRef.current;
    const existing = records.get(tabId);
    if (existing) {
      existing.lastInputAt = Date.now();
    } else {
      records.set(tabId, {
        lastOutputAt: 0,
        lastInputAt: Date.now(),
        hasEverOutput: false,
      });
    }
    // Clear synchronously: answering a prompt must retire the badge on the
    // first keystroke, not up to a tick later.
    setTabWaiting((prev) => {
      if (!prev.has(tabId)) return prev;
      const next = new Map(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  const clearTab = useCallback((tabId: string) => {
    recordsRef.current.delete(tabId);
    setTabWaiting((prev) => {
      if (!prev.has(tabId)) return prev;
      const next = new Map(prev);
      next.delete(tabId);
      return next;
    });
  }, []);

  useEffect(() => {
    const tick = () => {
      const now = Date.now();
      const next = new Map<string, boolean>();
      for (const [tabId, record] of recordsRef.current) {
        const waiting = isWaitingForInput(
          {
            connected: getConnectionStateRef.current(tabId) === "connected",
            hasEverOutput: record.hasEverOutput,
            lastOutputAt: record.lastOutputAt,
            lastInputAt: record.lastInputAt,
            probe: instanceRefs.current.get(tabId)?.probeActivity() ?? null,
            now,
          },
          quietMsRef.current,
        );
        if (waiting) next.set(tabId, true);
      }
      setTabWaiting((prev) => (sameKeys(prev, next) ? prev : next));
    };

    const interval = setInterval(tick, WAITING_TICK_MS);
    return () => clearInterval(interval);
  }, [instanceRefs]);

  return { tabWaiting, noteOutput, noteInput, clearTab };
}
