/**
 * PendingInputBanner surfaces the interactive prompt an agent is waiting on
 * and lets the operator resolve it: pick one of the typed options, send free
 * text, or decline. Rendered inside AgentDetailPanel's sticky header region,
 * so answering lives behind exactly the same route authz as stop/start.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import {
  answerAgentInput,
  fetchAgentPendingInput,
  type PendingInput,
} from "@/api/agents/pendingInputs";
import { useWorkspaceContext } from "@/hooks/workspace";

import styles from "./PendingInputBanner.module.css";

const POLL_MS = 5000;

interface PendingInputBannerProps {
  agentName: string;
}

export function PendingInputBanner({ agentName }: PendingInputBannerProps) {
  const { workspaceId } = useWorkspaceContext();
  const [pending, setPending] = useState<PendingInput | null>(null);
  const [text, setText] = useState("");
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
    async (body: { option_id?: string; text?: string; decline?: boolean }) => {
      if (!workspaceId || !pending) return;
      setBusy(true);
      setError(null);
      try {
        await answerAgentInput(workspaceId, agentName, {
          request_id: pending.request_id,
          ...body,
        });
        setPending(null);
        setText("");
      } catch (e) {
        setError(e instanceof Error ? e.message : "answer failed");
      } finally {
        if (mounted.current) setBusy(false);
      }
    },
    [workspaceId, agentName, pending],
  );

  if (!pending) return null;

  return (
    <div
      className={styles.banner}
      role="region"
      aria-label="Agent is waiting on input"
      data-testid="pending-input-banner"
    >
      <div className={styles.header}>
        <span className={styles.badge}>⏳ waiting on input</span>
        <span className={styles.kind}>{pending.kind}</span>
      </div>
      <div className={styles.prompt}>{pending.prompt}</div>
      {error && <div className={styles.error}>{error}</div>}
      <div className={styles.actions}>
        {(pending.options ?? []).map((opt) => (
          <button
            key={opt.id}
            type="button"
            className={styles.optionButton}
            disabled={busy}
            onClick={() => void deliver({ option_id: opt.id })}
          >
            {opt.label && opt.label !== opt.id
              ? `${opt.label}`
              : `Option ${opt.id}`}
          </button>
        ))}
        <button
          type="button"
          className={styles.declineButton}
          disabled={busy}
          onClick={() => void deliver({ decline: true })}
        >
          Decline
        </button>
      </div>
      <div className={styles.textRow}>
        <input
          type="text"
          className={styles.textInput}
          placeholder="Answer with text…"
          value={text}
          disabled={busy}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && text.trim() !== "") {
              void deliver({ text: text.trim() });
            }
          }}
        />
        <button
          type="button"
          className={styles.sendButton}
          disabled={busy || text.trim() === ""}
          onClick={() => void deliver({ text: text.trim() })}
        >
          Send
        </button>
      </div>
    </div>
  );
}
