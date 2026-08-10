/**
 * usePendingInput polls the daemon's pending-input registry for one agent and
 * delivers the operator's answer with the request id attached. Owning the api
 * calls here keeps PendingInputBanner presentational and inside the
 * components→hooks boundary (components must not import @/api directly).
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  answerAgentInput,
  fetchAgentPendingInput,
  type PendingInput,
} from "@/api/agents/pendingInputs";
import { useWorkspaceContext } from "@/hooks/workspace";

const POLL_MS = 5000;

export interface PendingAnswerBody {
  option_id?: string;
  text?: string;
  decline?: boolean;
}

export interface UsePendingInputReturn {
  pending: PendingInput | null;
  busy: boolean;
  error: string | null;
  /** Resolves true when the answer was delivered (the caller may clear its input). */
  deliver: (body: PendingAnswerBody) => Promise<boolean>;
}

export function usePendingInput(agentName: string): UsePendingInputReturn {
  const { workspaceId } = useWorkspaceContext();
  const [pending, setPending] = useState<PendingInput | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const mounted = useRef(true);

  const refresh = useCallback(async () => {
    if (!workspaceId || !agentName) return;
    try {
      const inputs = await fetchAgentPendingInput(workspaceId, agentName);
      if (mounted.current) {
        setPending(inputs.length > 0 ? (inputs[0] ?? null) : null);
      }
    } catch {
      // A daemon without the input routes (older build, remote mode) is not
      // an error state for the panel — there is just nothing to show.
      if (mounted.current) setPending(null);
    }
  }, [workspaceId, agentName]);

  useEffect(() => {
    mounted.current = true;
    void refresh();
    const timer = setInterval(() => void refresh(), POLL_MS);
    return () => {
      mounted.current = false;
      clearInterval(timer);
    };
  }, [refresh]);

  const deliver = useCallback(
    async (body: PendingAnswerBody): Promise<boolean> => {
      if (!workspaceId || !pending) return false;
      setBusy(true);
      setError(null);
      try {
        await answerAgentInput(workspaceId, agentName, {
          request_id: pending.request_id,
          ...body,
        });
        setPending(null);
        return true;
      } catch (e) {
        setError(e instanceof Error ? e.message : "answer failed");
        return false;
      } finally {
        if (mounted.current) setBusy(false);
      }
    },
    [workspaceId, agentName, pending],
  );

  return { pending, busy, error, deliver };
}

export type { PendingInput };
