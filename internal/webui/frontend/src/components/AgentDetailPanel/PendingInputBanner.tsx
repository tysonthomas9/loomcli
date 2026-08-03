/**
 * PendingInputBanner surfaces the interactive prompt an agent is waiting on
 * and lets the operator resolve it: pick one of the typed options, send free
 * text, or decline. Rendered inside AgentDetailPanel's sticky header region,
 * so answering lives behind exactly the same route authz as stop/start.
 */

import { useState } from "react";

import { usePendingInput } from "@/hooks/agents";

import styles from "./PendingInputBanner.module.css";

interface PendingInputBannerProps {
  agentName: string;
}

export function PendingInputBanner({ agentName }: PendingInputBannerProps) {
  const { pending, busy, error, deliver } = usePendingInput(agentName);
  const [text, setText] = useState("");

  const sendText = async () => {
    const trimmed = text.trim();
    if (trimmed === "") return;
    if (await deliver({ text: trimmed })) setText("");
  };

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
            if (e.key === "Enter") {
              void sendText();
            }
          }}
        />
        <button
          type="button"
          className={styles.sendButton}
          disabled={busy || text.trim() === ""}
          onClick={() => void sendText()}
        >
          Send
        </button>
      </div>
    </div>
  );
}
