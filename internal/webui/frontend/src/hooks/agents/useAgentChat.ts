import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AgentEventStream,
  getAgent,
  newRequestId,
  respondToAsk,
  sendMessage,
  withdrawMessage,
} from "@/api/agentsv1";
import type { Agent, AgentEvent, Ask, RespondBody } from "@/api/agentsv1";
import { ApiError } from "@/types/common";
import { REFRESH_KINDS, addDelta, chatItems, settle } from "./agentChatModel";
import type { ChatItem, Streaming } from "./agentChatModel";

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
}

const message = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

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

  const refresh = useCallback(() => {
    getAgent(workspaceId, agentId)
      .then(setAgent)
      .catch((err) => setError(message(err)));
  }, [workspaceId, agentId]);

  useEffect(() => {
    refresh();
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
      onNotice: (n) => setStreaming((s) => addDelta(s, n)),
      onResync: refresh,
    });
    void stream.connect();
    return () => stream.close();
  }, [workspaceId, agentId, refresh]);

  const items = useMemo(
    () => chatItems(events, streaming),
    [events, streaming],
  );

  // Runs one write; on failure shows the error and rethrows it.
  const write = useCallback(
    async (call: () => Promise<unknown>) => {
      setError(null);
      try {
        await call();
      } catch (err) {
        setError(message(err));
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

  // An answered ask hides at once; ask_not_found means it was answered
  // elsewhere or lost, so it hides too. Other errors re-enable the card.
  const respond = useCallback(
    async (askId: string, body: RespondBody) => {
      await write(() =>
        respondToAsk(workspaceId, agentId, askId, body, newRequestId()).catch(
          (err) => {
            if (errorCode(err) !== "ask_not_found") throw err;
          },
        ),
      );
      setAnswered((a) => new Set(a).add(askId));
    },
    [write, workspaceId, agentId],
  );

  const asks = (agent?.open_asks ?? []).filter((a) => !answered.has(a.id));
  return { agent, items, asks, error, send, clear, stop, respond };
}
