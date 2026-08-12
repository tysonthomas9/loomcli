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

/** The `code` an ApiError carries for a given status, if it is that status. */
function apiErrorCode(error: unknown, status: number): unknown {
  if (!(error instanceof ApiError) || error.status !== status) return undefined;
  if (!error.body || typeof error.body !== "object") return undefined;
  return (error.body as { code?: unknown }).code;
}

function isStaleSubjectError(error: unknown): boolean {
  return apiErrorCode(error, 409) === "stale_subject";
}

function isReviewerNotStartedError(error: unknown): boolean {
  return apiErrorCode(error, 404) === "reviewer_not_started";
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
  /** True after conversation returned OK (or ensure finished) so polling can continue. */
  const [conversationReady, setConversationReady] = useState(false);

  const retry = useCallback(() => {
    setError(null);
    setRetryNonce((n) => n + 1);
  }, []);

  const requestSeqRef = useRef(0);
  const ensureKeyRef = useRef<string | null>(null);
  const pollInFlightRef = useRef(false);
  const mountedRef = useRef(false);
  const onStaleSubjectRef = useRef(onStaleSubject);
  // Mirrors agentName for the poll's error path, which must read it WITHOUT
  // taking a dependency on it (that would re-arm the poll interval on every
  // agent change). Always written through setAgentNameAndRef.
  const agentNameRef = useRef<string | null>(null);
  const setAgentNameAndRef = useCallback((name: string | null) => {
    agentNameRef.current = name;
    setAgentName(name);
  }, []);
  const key = `${workspaceId}|${owner}|${repo}|${number}`;

  const invalidateRequests = useCallback(() => {
    requestSeqRef.current += 1;
    pollInFlightRef.current = false;
  }, []);

  const applyConversation = useCallback(
    (conversation: {
      messages: ReviewerMessage[];
      state: string;
      detail?: string | null;
    }) => {
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
      setConversationReady(true);
    },
    [],
  );

  const refetchConversation = useCallback(
    async (opts?: { quietNotStarted?: boolean }) => {
      if (!enabled) return;
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
          applyConversation(conversation);
        }
      } catch (err) {
        if (!(mountedRef.current && seq === requestSeqRef.current)) return;
        if (isReviewerNotStartedError(err)) {
          // Reviewer not stood up yet — stay on "starting" until ensure finishes.
          // Do not surface an error while ensure is still in flight.
          if (!opts?.quietNotStarted && agentNameRef.current) {
            setError(errorMessage(err));
          }
          return;
        }
        setError(errorMessage(err));
      } finally {
        pollInFlightRef.current = false;
      }
    },
    [applyConversation, enabled, number, owner, repo, workspaceId],
  );

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
    setAgentNameAndRef(null);
    setMessages([]);
    setState("starting");
    setDetail(null);
    setError(null);
    setSending(false);
    setConversationReady(false);
    ensureKeyRef.current = null;
    requestSeqRef.current++;
    pollInFlightRef.current = false;
  }, [key, setAgentNameAndRef]);

  // Load conversation immediately — do not wait for ensure. Existing reviewers
  // (e.g. kanban sidebar click) can show chat while checkout refreshes.
  useEffect(() => {
    if (!enabled) return;
    void refetchConversation({ quietNotStarted: true });
  }, [enabled, key, refetchConversation, retryNonce]);

  useEffect(() => {
    if (!enabled) return;
    if (ensureKeyRef.current === key) return;

    let ignore = false;
    let completed = false;
    ensureKeyRef.current = key;
    setError(null);

    void (async () => {
      try {
        const result = await ensureReviewer(workspaceId, owner, repo, number);
        if (!ignore && mountedRef.current) {
          setAgentNameAndRef(result.agent_name);
          setError(null);
          setConversationReady(true);
          // Refresh after ensure so checkout/agent identity stays in sync.
          pollInFlightRef.current = false;
          await refetchConversation({ quietNotStarted: false });
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
  }, [
    enabled,
    key,
    number,
    owner,
    refetchConversation,
    repo,
    retryNonce,
    setAgentNameAndRef,
    workspaceId,
  ]);

  useEffect(() => {
    if (!enabled || !conversationReady) return;

    const intervalId = setInterval(() => {
      void refetchConversation({ quietNotStarted: false });
    }, POLL_INTERVAL);
    return () => {
      clearInterval(intervalId);
      invalidateRequests();
    };
  }, [conversationReady, enabled, invalidateRequests, refetchConversation]);

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
        await refetchConversation({ quietNotStarted: false });
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
