/**
 * useAgentBrowsers — durable browser inventory for one interactive agent.
 *
 * The inventory is always the server's durable view: it is refetched (never
 * patched locally) after SSE `browser` mutations for this agent, on SSE
 * reconnect, window focus/visibility, surface activation, and after select.
 *
 * State safety:
 * - state is keyed by workspace+agent; a key change renders an empty loading
 *   inventory in the same render (no stale IDs under a new agent);
 * - every request carries a generation number, and older generations are
 *   ignored, so out-of-order A/B responses resolve to B;
 * - a failure (including 401 / operator-session loss) clears the entries.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  BrowserApiError,
  listAgentBrowsers,
  onBrowserOperatorSessionLost,
  selectAgentBrowser,
} from "@/api/agents/browsers";
import { useEventContext, useEventSubscription } from "@/hooks/common";
import type { AgentBrowser, MutationPayload } from "@/types";

export interface AgentBrowsersError {
  code: string;
  message: string;
  isAuthFailure: boolean;
}

export type AgentBrowsersPhase = "idle" | "loading" | "ready" | "error";

export interface UseAgentBrowsersOptions {
  workspaceId: string | undefined;
  agentName: string | undefined;
  /** False for worker agents: no requests, empty inventory. */
  enabled: boolean;
  /** The surface hosting the tabs is visible; activation triggers a refetch. */
  active?: boolean;
}

export interface UseAgentBrowsersReturn {
  phase: AgentBrowsersPhase;
  browsers: AgentBrowser[];
  error: AgentBrowsersError | null;
  refetch: () => void;
  select: (browserId: string) => Promise<void>;
}

interface InventoryState {
  key: string;
  phase: AgentBrowsersPhase;
  browsers: AgentBrowser[];
  error: AgentBrowsersError | null;
}

const EMPTY: AgentBrowser[] = [];

function toError(err: unknown): AgentBrowsersError {
  if (err instanceof BrowserApiError) {
    return {
      code: err.code,
      message: err.message,
      isAuthFailure: err.isAuthFailure,
    };
  }
  return {
    code: "browser_unavailable",
    message: err instanceof Error ? err.message : "Browser request failed",
    isAuthFailure: false,
  };
}

function isAbort(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === "AbortError") ||
    (err instanceof Error && err.name === "AbortError")
  );
}

export function useAgentBrowsers({
  workspaceId,
  agentName,
  enabled,
  active = true,
}: UseAgentBrowsersOptions): UseAgentBrowsersReturn {
  const key =
    enabled && workspaceId && agentName
      ? `${workspaceId}\u0000${agentName}`
      : "";

  const [state, setState] = useState<InventoryState>(() => ({
    key,
    phase: key ? "loading" : "idle",
    browsers: EMPTY,
    error: null,
  }));

  const generationRef = useRef(0);
  const abortRef = useRef<AbortController | null>(null);
  const inFlightRef = useRef(false);

  const refetch = useCallback(() => {
    if (!key || !workspaceId || !agentName) return;
    const generation = ++generationRef.current;
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;
    inFlightRef.current = true;
    listAgentBrowsers(workspaceId, agentName, { signal: controller.signal })
      .then((browsers) => {
        if (generation !== generationRef.current) return;
        setState({ key, phase: "ready", browsers, error: null });
      })
      .catch((err: unknown) => {
        if (generation !== generationRef.current || isAbort(err)) return;
        setState({ key, phase: "error", browsers: EMPTY, error: toError(err) });
      })
      .finally(() => {
        if (generation === generationRef.current) inFlightRef.current = false;
      });
  }, [key, workspaceId, agentName]);

  // Key change (agent/workspace switch, enable/disable): clear immediately,
  // invalidate older generations, then load the new inventory.
  useEffect(() => {
    generationRef.current += 1;
    abortRef.current?.abort();
    abortRef.current = null;
    inFlightRef.current = false;
    setState({
      key,
      phase: key ? "loading" : "idle",
      browsers: EMPTY,
      error: null,
    });
    refetch();
    return () => {
      generationRef.current += 1;
      abortRef.current?.abort();
      abortRef.current = null;
    };
  }, [key, refetch]);

  // Operator bearer dropped (401, expiry, revocation): clear now. A request
  // already in flight is retrying with a re-issued bearer and will settle the
  // state; otherwise reload.
  useEffect(() => {
    if (!key) return;
    return onBrowserOperatorSessionLost((lostWorkspace) => {
      if (lostWorkspace !== workspaceId) return;
      setState({ key, phase: "loading", browsers: EMPTY, error: null });
      if (!inFlightRef.current) refetch();
    });
  }, [key, workspaceId, refetch]);

  // Durable commit broadcast for this agent's browsers.
  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (!key || mutation.entity_type !== "browser") return;
      if (mutation.entity_id !== agentName) return;
      if (mutation.workspace_id && mutation.workspace_id !== workspaceId)
        return;
      refetch();
    },
    [key, agentName, workspaceId, refetch],
  );
  useEventSubscription(handleMutation, { entityTypes: ["browser"] });

  // SSE reconnect: mutations may have been missed while disconnected.
  const { state: sseState } = useEventContext();
  const prevSseStateRef = useRef(sseState);
  useEffect(() => {
    const prev = prevSseStateRef.current;
    prevSseStateRef.current = sseState;
    if (sseState === "connected" && prev === "reconnecting") refetch();
  }, [sseState, refetch]);

  // Surface activation.
  const prevActiveRef = useRef(active);
  useEffect(() => {
    const wasActive = prevActiveRef.current;
    prevActiveRef.current = active;
    if (active && !wasActive) refetch();
  }, [active, refetch]);

  // Window focus / visibility.
  useEffect(() => {
    if (!key) return;
    const onFocus = () => refetch();
    const onVisibility = () => {
      if (document.visibilityState === "visible") refetch();
    };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [key, refetch]);

  const select = useCallback(
    async (browserId: string) => {
      if (!key || !workspaceId || !agentName) return;
      const generation = generationRef.current;
      try {
        await selectAgentBrowser(workspaceId, agentName, browserId);
      } catch (err) {
        if (generation !== generationRef.current || isAbort(err)) return;
        const error = toError(err);
        if (error.isAuthFailure) {
          setState({ key, phase: "error", browsers: EMPTY, error });
          return;
        }
      }
      if (generation === generationRef.current) refetch();
    },
    [key, workspaceId, agentName, refetch],
  );

  // A stale key renders as an empty loading inventory in this very render.
  if (state.key !== key) {
    return {
      phase: key ? "loading" : "idle",
      browsers: EMPTY,
      error: null,
      refetch,
      select,
    };
  }
  return {
    phase: state.phase,
    browsers: state.browsers,
    error: state.error,
    refetch,
    select,
  };
}
