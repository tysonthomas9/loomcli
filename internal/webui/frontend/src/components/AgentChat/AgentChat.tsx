import { useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import { ownSender, useAgentChat, useRosterAgent } from "@/hooks";
import type { ChatItem } from "@/hooks";
import { AskCard } from "./AskCard";
import { ChatMarkdown } from "./ChatMarkdown";
import {
  COMPOSER_FOOTER_COMPACT_BREAKPOINT_PX,
  ComposerModelControls,
} from "./ComposerModelControls";
import { LONG_TEXT_LIMIT, LongText } from "./LongText";
import { MessageCopyButton } from "./MessageCopyButton";
import { deriveTimelineRows, type TimelineRow } from "./timelineRows";
import {
  LiveWorkEntryRow,
  ThinkingActivityRow,
  WorkEntryRow,
  WorkGroupToggleRow,
} from "./WorkRows";
import styles from "./AgentChat.module.css";

export interface AgentChatProps {
  workspaceId: string;
  agentId: string;
}

/**
 * One chat view for every harness (design v2 §9.2–§9.3): the transcript, a
 * composer that stays usable while a turn runs, the waiting bubbles with edit
 * and clear, and approval and question cards. A lead's chat shows a card per
 * child and every completion record (§9.4). The harness shows only as a
 * label.
 */
export function AgentChat({ workspaceId, agentId }: AgentChatProps) {
  const { agent, items, asks, error, send, clear, stop, respond, update } =
    useAgentChat(workspaceId, agentId);
  const compact = useNarrow(COMPOSER_FOOTER_COMPACT_BREAKPOINT_PX);
  const own = ownSender(useAuth().user?.id);
  const [draft, setDraft] = useState("");
  const [editing, setEditing] = useState(false);
  const [sending, setSending] = useState(false);
  const [expandedGroups, setExpandedGroups] = useState<ReadonlySet<string>>(
    new Set(),
  );
  const rows = useMemo(
    () => deriveTimelineRows(items, expandedGroups),
    [items, expandedGroups],
  );
  const toggleGroup = (id: string) =>
    setExpandedGroups((g) => {
      const next = new Set(g);
      if (!next.delete(id)) next.add(id);
      return next;
    });

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
            {agent.history_purge_failed_at && !agent.history_purged_at && (
              <span className={styles.state} role="status">
                History expiry incomplete
              </span>
            )}
          </>
        )}
      </header>

      <ol className={styles.transcript} data-testid="chat-transcript">
        {rows.map((row) => (
          <li
            key={row.id}
            className={
              row.kind === "item" ? styles[row.item.kind] : styles.work
            }
          >
            <Row row={row} workspaceId={workspaceId} onToggle={toggleGroup} />
          </li>
        ))}
        {agent?.waiting_messages.map((w) => (
          <li key={`waiting:${w.sender}`} className={styles.waiting}>
            <div className={styles.waitingTitle}>
              Waiting
              {w.sender !== own && ` · from ${w.sender}`}
            </div>
            <LongText text={w.text} />
            {w.sender === own && (
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
        ref={compact.ref}
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
        <div className={styles.composerFooter}>
          <ComposerModelControls
            workspaceId={workspaceId}
            agent={agent}
            compact={compact.narrow}
            update={update}
          />
          <div className={styles.composerActions}>
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
          </div>
        </div>
      </form>
    </section>
  );
}

/** Whether the observed element is narrower than px (false until measured). */
function useNarrow(px: number) {
  const ref = useRef<HTMLFormElement>(null);
  const [narrow, setNarrow] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(([e]) => {
      if (e) setNarrow(e.contentRect.width < px);
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [px]);
  return { ref, narrow };
}

/** A child's chat link, with its live state from the sidebar's roster. */
function ChildLink({ ws, id, name }: { ws: string; id: string; name: string }) {
  const a = useRosterAgent(id);
  return (
    <Link
      className={styles.childLink}
      to={`/ws/${encodeURIComponent(ws)}/chat/${encodeURIComponent(id)}`}
    >
      <span className={styles.name}>{a?.name ?? name}</span>
      {a && <span className={styles.label}>{a.harness}</span>}
      {a && <span className={styles.state}>{a.state}</span>}
    </Link>
  );
}

function Row({
  row,
  workspaceId,
  onToggle,
}: {
  row: TimelineRow;
  workspaceId: string;
  onToggle: (groupId: string) => void;
}) {
  switch (row.kind) {
    case "item":
      return <Item item={row.item} workspaceId={workspaceId} />;
    case "work":
      return <WorkEntryRow entry={row.entry} inGroup={row.inGroup} />;
    case "work-toggle":
      return (
        <WorkGroupToggleRow row={row} onToggle={() => onToggle(row.groupId)} />
      );
    case "work-live":
      return (
        <LiveWorkEntryRow row={row} onToggle={() => onToggle(row.groupId)} />
      );
    case "thinking":
      return <ThinkingActivityRow />;
  }
}

/**
 * An agent message as markdown, cut like LongText until the user expands
 * it, with a copy button once it is complete.
 */
function AgentMessage({
  text,
  streaming,
}: {
  text: string;
  streaming: boolean;
}) {
  const [all, setAll] = useState(false);
  const cut = !all && text.length > LONG_TEXT_LIMIT;
  return (
    <div className={styles.agentMessage}>
      <ChatMarkdown
        text={cut ? text.slice(0, LONG_TEXT_LIMIT) + "…" : text}
        streaming={streaming}
      />
      {cut && (
        <button className={styles.showAll} onClick={() => setAll(true)}>
          Show all ({text.length.toLocaleString()} characters)
        </button>
      )}
      {!streaming && text.trim() && (
        <div className={styles.messageMeta}>
          <MessageCopyButton text={text} />
        </div>
      )}
    </div>
  );
}

function Item({ item, workspaceId }: { item: ChatItem; workspaceId: string }) {
  switch (item.kind) {
    case "child":
      return (
        <div data-testid="child-card">
          <div className={styles.note}>Child agent</div>
          <ChildLink ws={workspaceId} id={item.child} name={item.name} />
        </div>
      );
    case "completion": {
      const r = item.record;
      return (
        <div data-testid="completion-record">
          <div className={styles.note}>
            {/* Attempts count from 0; people count from 1. */}
            Attempt {r.attempt + 1} {r.outcome}
            {r.branch && ` · ${r.branch}`}
            {r.head && ` @ ${r.head.slice(0, 8)}`}
          </div>
          <ChildLink ws={workspaceId} id={r.child} name={r.child} />
          {r.summary && <LongText text={r.summary} />}
        </div>
      );
    }
    case "turn_end":
      return <div className={styles.note}>Turn {item.reason}</div>;
    case "agent":
      return <AgentMessage text={item.text} streaming={!!item.streaming} />;
    case "user":
      return <LongText text={item.text} />;
    // Tool calls and reasoning are work rows (see deriveTimelineRows).
    case "tool":
    case "reasoning":
      return null;
  }
}
