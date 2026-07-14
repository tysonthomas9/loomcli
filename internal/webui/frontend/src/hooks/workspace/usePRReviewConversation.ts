import { useCallback, useEffect, useRef, useState } from "react";

import {
  ensureReviewer,
  getReviewerConversation,
  sendReviewerMessage,
  type ReviewerMessage,
} from "@/api/workspace/prReview";
import { ApiError } from "@/types/common/errors";

const POLL_INTERVAL = 1_500;

export interface UsePRReviewConversationParams {
  workspaceId: string;
  owner: string;
  repo: string;
  number: number;
  enabled: boolean;
  onStaleSubject?: () => void | Promise<void>;
}

export interface UsePRReviewConversationResult {
  agentName: string | null;
  messages: ReviewerMessage[];
  state: string;
  /** Human-readable context for failed/unsupported states. */
  detail: string | null;
  sending: boolean;
  /** Resolves true when the message was accepted; false on failure/no-op. */
  send: (text: string) => Promise<boolean>;
  /** Re-attempt standing up the reviewer after an ensure failure. */
  retry: () => void;
  error: string | null;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isStaleSubjectError(error: unknown): boolean {
  if (!(error instanceof ApiError) || error.status !== 409) return false;
  if (!error.body || typeof error.body !== "object") return false;
  return (error.body as { code?: unknown }).code === "stale_subject";
}

export function usePRReviewConversation({
  workspaceId,
  owner,
  repo,
  number,
  enabled,
  onStaleSubject,
}: UsePRReviewConversationParams): UsePRReviewConversationResult {
  const [agentName, setAgentName] = useState<string | null>(null);
  const [messages, setMessages] = useState<ReviewerMessage[]>([]);
  const [state, setState] = useState("starting");
  const [detail, setDetail] = useState<string | null>(null);
  const [sending, setSending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [retryNonce, setRetryNonce] = useState(0);

  const retry = useCallback(() => {
    setError(null);
    setRetryNonce((n) => n + 1);
  }, []);

  const requestSeqRef = useRef(0);
  const ensureKeyRef = useRef<string | null>(null);
  const pollInFlightRef = useRef(false);
  const mountedRef = useRef(false);
  const onStaleSubjectRef = useRef(onStaleSubject);
  const key = `${workspaceId}|${owner}|${repo}|${number}`;

  const invalidateRequests = useCallback(() => {
    requestSeqRef.current += 1;
    pollInFlightRef.current = false;
  }, []);

  const refetchConversation = useCallback(async () => {
    if (!enabled || !agentName) return;
    if (pollInFlightRef.current) return;
    pollInFlightRef.current = true;
    const seq = ++requestSeqRef.current;

    try {
      const conversation = await getReviewerConversation(
        workspaceId,
        owner,
        repo,
        number,
      );
      if (mountedRef.current && seq === requestSeqRef.current) {
        // A reconnecting snapshot has no messages by construction (the read
        // failed transiently, e.g. a torn transcript append) — keep showing
        // the last good conversation instead of blanking the chat.
        if (
          conversation.state !== "reconnecting" ||
          conversation.messages.length > 0
        ) {
          setMessages(conversation.messages);
        }
        setState(conversation.state);
        setDetail(conversation.detail ?? null);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current && seq === requestSeqRef.current) {
        setError(errorMessage(err));
      }
    } finally {
      pollInFlightRef.current = false;
    }
  }, [agentName, enabled, number, owner, repo, workspaceId]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      invalidateRequests();
    };
  }, [invalidateRequests]);

  useEffect(() => {
    onStaleSubjectRef.current = onStaleSubject;
  }, [onStaleSubject]);

  useEffect(() => {
    setAgentName(null);
    setMessages([]);
    setState("starting");
    setDetail(null);
    setError(null);
    setSending(false);
    ensureKeyRef.current = null;
    requestSeqRef.current++;
    pollInFlightRef.current = false;
  }, [key]);

  useEffect(() => {
    if (!enabled) return;
    if (ensureKeyRef.current === key) return;

    let ignore = false;
    let completed = false;
    ensureKeyRef.current = key;
    setState("starting");
    setError(null);

    void (async () => {
      try {
        const result = await ensureReviewer(workspaceId, owner, repo, number);
        if (!ignore && mountedRef.current) {
          setAgentName(result.agent_name);
          setError(null);
        }
      } catch (err) {
        if (!ignore && mountedRef.current) {
          ensureKeyRef.current = null;
          if (isStaleSubjectError(err)) {
            void onStaleSubjectRef.current?.();
          }
          setError(errorMessage(err));
        }
      } finally {
        completed = true;
      }
    })();

    return () => {
      ignore = true;
      if (!completed && ensureKeyRef.current === key) {
        ensureKeyRef.current = null;
      }
    };
    // retryNonce lets retry() re-run ensure after a failure (the catch above
    // clears ensureKeyRef, so this effect proceeds instead of short-circuiting).
  }, [enabled, key, number, owner, repo, retryNonce, workspaceId]);

  useEffect(() => {
    if (!enabled || !agentName) return;

    void refetchConversation();
    const intervalId = setInterval(refetchConversation, POLL_INTERVAL);
    return () => {
      clearInterval(intervalId);
      invalidateRequests();
    };
  }, [agentName, enabled, invalidateRequests, refetchConversation]);

  const send = useCallback(
    async (text: string): Promise<boolean> => {
      const trimmed = text.trim();
      if (!trimmed || !agentName) return false;

      setSending(true);
      setError(null);
      try {
        await sendReviewerMessage(workspaceId, owner, repo, number, trimmed);
        // Clear any in-flight poll gate so the optimistic refetch isn't dropped
        // and the user's message shows immediately.
        pollInFlightRef.current = false;
        await refetchConversation();
        return true;
      } catch (err) {
        if (mountedRef.current) {
          setError(errorMessage(err));
        }
        return false;
      } finally {
        if (mountedRef.current) {
          setSending(false);
        }
      }
    },
    [agentName, number, owner, refetchConversation, repo, workspaceId],
  );

  return { agentName, messages, state, detail, sending, send, retry, error };
}
