import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AgentEventStream,
  archiveAgent,
  deleteAgent,
  getAgent,
  newRequestId,
  respondToAsk,
  sendMessage,
  unarchiveAgent,
  updateAgent,
  withdrawMessage,
} from "@/api/agentsv1";
import type {
  Agent,
  AgentEvent,
  Ask,
  RespondBody,
  UpdateAgentBody,
} from "@/api/agentsv1";
import { ApiError } from "@/types/common";
import { REFRESH_KINDS, addDelta, chatItems, settle } from "./agentChatModel";
import type { ChatItem, Streaming } from "./agentChatModel";

/** Archives an agent by id: the chat header's Archive call, for the sidebar. */
export function useArchiveAgent(
  workspaceId: string,
): (agentId: string) => Promise<void> {
  return useCallback(
    (agentId: string) => archiveAgent(workspaceId, agentId, newRequestId()),
    [workspaceId],
  );
}

/** A Delete the server refused: its text (with the files unsaved_work
 * names), and unsaved_work's fingerprint. */
export interface DeleteRefusal {
  error: string;
  /** The fingerprint Delete anyway sends; null unless unsaved_work. */
  unsaved: string | null;
}

/** Deletes an agent by id for the sidebar: null once deleted, else the refusal. */
export function useDeleteAgent(
  workspaceId: string,
): (agentId: string, fingerprint?: string) => Promise<DeleteRefusal | null> {
  return useCallback(
    async (agentId: string, fingerprint?: string) => {
      try {
        await deleteAgent(
          workspaceId,
          agentId,
          newRequestId(),
          fingerprint ? { fingerprint } : undefined,
        );
        return null;
      } catch (err) {
        return {
          error: message(err),
          unsaved:
            errorCode(err) === "unsaved_work"
              ? (((err as ApiError).body as { fingerprint?: string })
                  .fingerprint ?? null)
              : null,
        };
      }
    },
    [workspaceId],
  );
}

export interface UseAgentChatReturn {
  agent: Agent | null;
  items: ChatItem[];
  /** Open asks not yet answered from this view. */
  asks: Ask[];
  error: string | null;
  /** Send; rejects (after setting error) so the composer keeps its text. */
  send: (text: string) => Promise<void>;
  clear: () => Promise<void>;
  /** Stops the running turn: an interrupt Send with no message (§9.2). */
  stop: () => Promise<void>;
  respond: (askId: string, body: RespondBody) => Promise<void>;
  /** PATCHes the agent (name, model, effort, options); a model or effort
   * change applies from the next turn. */
  update: (body: UpdateAgentBody) => Promise<void>;
  /** When the running turn started (its saved turn.started), else null. */
  runningSince: string | null;
  archive: () => Promise<void>;
  unarchive: () => Promise<void>;
  /** The saved history is gone (history_purged_at, or a history_expired error). */
  expired: boolean;
  /**
   * No catch-up is replaying history: false from each connect, reconnect or
   * feed.gap until its catch-up loads, so rows that arrive while true are live.
   */
  synced: boolean;
}

/** REST Send's JSON request body limit (the server's 1 MiB guard). */
const TOO_LARGE =
  "Not sent: the message is over the server's 1 MiB request limit. Shorten it or split it into parts.";

const message = (err: unknown) => {
  if (err instanceof ApiError && err.status === 413) return TOO_LARGE;
  const paths = err instanceof ApiError ? errorPaths(err) : [];
  const text = err instanceof Error ? err.message : String(err);
  return paths.length ? `${text}: ${paths.join(", ")}` : text;
};

// The files an unsaved_work refusal names.
const errorPaths = (err: ApiError) => {
  const p = (err.body as { paths?: unknown } | undefined)?.paths;
  return Array.isArray(p) ? p.map(String) : [];
};

const errorCode = (err: unknown) =>
  err instanceof ApiError && err.body && typeof err.body === "object"
    ? (err.body as { code?: string }).code
    : undefined;

/**
 * One agent's chat over the Agent API (design v2 §9.2–§9.5): saved events
 * and live deltas from the event stream, and Get for state, waiting messages
 * and open asks after every change and every resync. Each write is a new
 * user action, so it gets a new Idempotency-Key.
 */
export function useAgentChat(
  workspaceId: string,
  agentId: string,
): UseAgentChatReturn {
  const [agent, setAgent] = useState<Agent | null>(null);
  const [events, setEvents] = useState<readonly AgentEvent[]>([]);
  const [streaming, setStreaming] = useState<Streaming>(new Map());
  const [answered, setAnswered] = useState<ReadonlySet<string>>(new Set());
  const [error, setError] = useState<string | null>(null);
  const [expiredErr, setExpiredErr] = useState(false);
  const [synced, setSynced] = useState(false);

  const refresh = useCallback(() => {
    getAgent(workspaceId, agentId)
      .then(setAgent)
      .catch((err) => setError(message(err)));
  }, [workspaceId, agentId]);

  useEffect(() => {
    refresh();
    setSynced(false);
    const stream: AgentEventStream = new AgentEventStream(workspaceId, {
      agents: [agentId],
      deltas: true,
      onEvents: (added) => {
        setEvents([...stream.history.events(agentId)]);
        setStreaming((s) =>
          added.some((e) => e.kind === "agent.turn_completed")
            ? new Map()
            : settle(s, added),
        );
        if (added.some((e) => REFRESH_KINDS.has(e.kind))) refresh();
      },
      onNotice: (n) => {
        if (n.kind === "feed.gap") setSynced(false);
        // Capture each native notice before React can combine state updates.
        const observedAt = performance.now();
        setStreaming((s) => addDelta(s, n, observedAt));
      },
      onStateChange: (state) => {
        if (state === "connecting" || state === "reconnecting")
          setSynced(false);
      },
      onResync: () => {
        setSynced(true);
        refresh();
      },
    });
    void stream.connect();
    return () => stream.close();
  }, [workspaceId, agentId, refresh]);

  const waiting = agent?.waiting_messages;
  const items = useMemo(
    () => chatItems(events, streaming, waiting),
    [events, streaming, waiting],
  );

  // Runs one write; on failure shows the error and rethrows it.
  const write = useCallback(
    async (call: () => Promise<unknown>) => {
      setError(null);
      try {
        await call();
      } catch (err) {
        setError(message(err));
        if (errorCode(err) === "history_expired") setExpiredErr(true);
        throw err;
      }
      refresh();
    },
    [refresh],
  );

  const send = useCallback(
    (text: string) =>
      write(() => sendMessage(workspaceId, agentId, text, newRequestId())),
    [write, workspaceId, agentId],
  );

  const clear = useCallback(
    () => write(() => withdrawMessage(workspaceId, agentId, newRequestId())),
    [write, workspaceId, agentId],
  );

  const stop = useCallback(
    () =>
      write(() =>
        sendMessage(workspaceId, agentId, "", newRequestId(), "interrupt"),
      ),
    [write, workspaceId, agentId],
  );

  // An answered ask hides at once; ask_not_found or already_answered means
  // it was answered elsewhere or lost, so it hides too. Other errors
  // re-enable the card.
  const respond = useCallback(
    async (askId: string, body: RespondBody) => {
      await write(() =>
        respondToAsk(workspaceId, agentId, askId, body, newRequestId()).catch(
          (err) => {
            const code = errorCode(err);
            if (code !== "ask_not_found" && code !== "already_answered")
              throw err;
          },
        ),
      );
      setAnswered((a) => new Set(a).add(askId));
    },
    [write, workspaceId, agentId],
  );

  const update = useCallback(
    (body: UpdateAgentBody) =>
      write(() => updateAgent(workspaceId, agentId, body, newRequestId())),
    [write, workspaceId, agentId],
  );

  const archive = useCallback(
    () => write(() => archiveAgent(workspaceId, agentId, newRequestId())),
    [write, workspaceId, agentId],
  );

  const unarchive = useCallback(
    () => write(() => unarchiveAgent(workspaceId, agentId, newRequestId())),
    [write, workspaceId, agentId],
  );

  const runningTurn = agent?.running_turn_id ?? null;
  const runningSince = useMemo(
    () => turnStartedAt(events, runningTurn),
    [events, runningTurn],
  );

  const asks = (agent?.open_asks ?? []).filter((a) => !answered.has(a.id));
  return {
    agent,
    items,
    asks,
    error,
    send,
    clear,
    stop,
    respond,
    update,
    runningSince,
    archive,
    unarchive,
    expired: expiredErr || !!agent?.history_purged_at,
    synced,
  };
}

/** The saved start time of turn turnId, or null when it is not in events. */
export function turnStartedAt(
  events: readonly AgentEvent[],
  turnId: string | null,
): string | null {
  if (!turnId) return null;
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i];
    if (e?.kind === "turn.started" && e.turn_id === turnId) return e.created_at;
  }
  return null;
}
