import { useState } from "react";
import { isUserSender, useAgentChat } from "@/hooks";
import type { ChatItem } from "@/hooks";
import { AskCard } from "./AskCard";
import { LongText } from "./LongText";
import styles from "./AgentChat.module.css";

export interface AgentChatProps {
  workspaceId: string;
  agentId: string;
}

/**
 * One chat view for every harness (design v2 §9.2–§9.3): the transcript, a
 * composer that stays usable while a turn runs, the waiting bubbles with edit
 * and clear, and approval and question cards. The harness shows only as a
 * label.
 */
export function AgentChat({ workspaceId, agentId }: AgentChatProps) {
  const { agent, items, asks, error, send, clear, stop, respond } =
    useAgentChat(workspaceId, agentId);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [sending, setSending] = useState(false);

  // An edit is another Send that replaces the waiting text (§9.2).
  const submit = () => {
    const text = draft.trim();
    if (!text || sending) return;
    setSending(true);
    send(text)
      .then(() => {
        setDraft("");
        setEditing(false);
      })
      .catch(() => {})
      .finally(() => setSending(false));
  };

  const stopEditing = () => {
    setDraft("");
    setEditing(false);
  };

  return (
    <section className={styles.chat} aria-label="Agent chat">
      <header className={styles.header}>
        <span className={styles.name}>{agent?.name ?? agentId}</span>
        {agent && (
          <>
            <span className={styles.label} data-testid="harness-label">
              {agent.harness}
            </span>
            <span className={styles.state}>{agent.state}</span>
          </>
        )}
      </header>

      <ol className={styles.transcript} data-testid="chat-transcript">
        {items.map((item) => (
          <li key={item.key} className={styles[item.kind]}>
            <Item item={item} />
          </li>
        ))}
        {agent?.waiting_messages.map((w) => (
          <li key={`waiting:${w.sender}`} className={styles.waiting}>
            <div className={styles.waitingTitle}>
              Waiting
              {!isUserSender(w.sender) && ` · from ${w.sender}`}
            </div>
            <LongText text={w.text} />
            {isUserSender(w.sender) && (
              <div className={styles.waitingActions}>
                <button
                  onClick={() => {
                    setDraft(w.text);
                    setEditing(true);
                  }}
                >
                  Edit
                </button>
                <button
                  onClick={() =>
                    clear()
                      .then(() => editing && stopEditing())
                      .catch(() => {})
                  }
                >
                  Clear
                </button>
              </div>
            )}
          </li>
        ))}
      </ol>

      {asks.map((ask) => (
        <AskCard
          key={ask.id}
          ask={ask}
          onRespond={(body) => respond(ask.id, body)}
        />
      ))}

      {error && (
        <div className={styles.error} role="alert">
          {error}
        </div>
      )}

      <form
        className={styles.composer}
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
      >
        <textarea
          aria-label="Message"
          className={styles.input}
          value={draft}
          placeholder={editing ? "Edit your waiting message" : "Message"}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing
            ) {
              e.preventDefault();
              submit();
            }
          }}
        />
        {editing && (
          <button type="button" onClick={stopEditing}>
            Cancel
          </button>
        )}
        {agent?.running_turn_id && (
          <button type="button" onClick={() => void stop().catch(() => {})}>
            Stop
          </button>
        )}
        <button type="submit" disabled={sending || !draft.trim()}>
          {editing ? "Save" : "Send"}
        </button>
      </form>
    </section>
  );
}

function Item({ item }: { item: ChatItem }) {
  switch (item.kind) {
    case "turn_end":
      return <div className={styles.note}>Turn {item.reason}</div>;
    case "tool":
    case "reasoning":
      return (
        <details>
          <summary>{item.kind === "tool" ? "Tool call" : "Reasoning"}</summary>
          <LongText text={item.text} />
        </details>
      );
    default:
      return <LongText text={item.text} />;
  }
}
