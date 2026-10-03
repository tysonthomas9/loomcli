// The page layout, message column and scroll-to-end are ported from T3 Code
// apps/web/src/components/ChatView.tsx and
// apps/web/src/components/chat/MessagesTimeline.tsx at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import {
  latestTurnError,
  ownSender,
  useAgentChat,
  useRosterAgent,
} from "@/hooks";
import type { ChatItem } from "@/hooks";
import { AskCard } from "./AskCard";
import { ChatComposer } from "./ChatComposer";
import { ChatHeader } from "./ChatHeader";
import { ChatMarkdown } from "./ChatMarkdown";
import {
  COMPOSER_FOOTER_COMPACT_BREAKPOINT_PX,
  ComposerModelControls,
} from "./ComposerModelControls";
import { LONG_TEXT_LIMIT, LongText } from "./LongText";
import { MessageCopyButton } from "./MessageCopyButton";
import { UserMessage, WorkingRow } from "./MessageRows";
import {
  dismissThreadErrorBannerForSession,
  getThreadErrorBannerKey,
  isThreadErrorBannerDismissedForSession,
  ThreadErrorBanner,
} from "./ThreadErrorBanner";
import { deriveTimelineRows, type TimelineRow } from "./timelineRows";
import {
  LiveWorkEntryRow,
  ThinkingActivityRow,
  WorkEntryRow,
  WorkGroupToggleRow,
} from "./WorkRows";
import styles from "./AgentChat.module.css";
import page from "./ChatPage.module.css";

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
  const {
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
  } = useAgentChat(workspaceId, agentId);
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
  // The latest turn's failure reason (T3's thread error), until dismissed.
  const turnError = useMemo(() => latestTurnError(items), [items]);
  const bannerKey = getThreadErrorBannerKey(agentId, turnError);
  const [, setDismissed] = useState(0);
  const ask = asks[0]; // T3 shows the first open ask, with "1/N"
  const running = !!agent?.running_turn_id;
  const waiting = agent?.waiting_messages ?? [];
  const scroll = useStickToEnd();
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
    scroll.follow();
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

  const empty = rows.length === 0 && waiting.length === 0 && !running;

  return (
    <section className={page.chat} aria-label="Agent chat">
      <ChatHeader
        agentId={agentId}
        agent={agent}
        onRename={(name) => update({ name })}
      />

      <div className={page.scroller}>
        <ol
          ref={scroll.ref}
          className={page.transcript}
          data-testid="chat-transcript"
          onScroll={scroll.onScroll}
        >
          {rows.map((row) => (
            <li key={row.id} className={page.row} data-kind={rowKind(row)}>
              <Row row={row} workspaceId={workspaceId} onToggle={toggleGroup} />
            </li>
          ))}
          {running && (
            <li className={page.row} data-kind="working">
              <WorkingRow startedAt={runningSince} />
            </li>
          )}
          {waiting.map((w) => (
            <li
              key={`waiting:${w.sender}`}
              className={`${page.row} ${page.waiting}`}
              data-kind="waiting"
            >
              <div className={page.userBubble}>
                <div className={page.waitingTitle}>
                  Waiting
                  {w.sender !== own && ` · from ${w.sender}`}
                </div>
                <LongText text={w.text} />
                {w.sender === own && (
                  <div className={page.waitingActions}>
                    <button
                      type="button"
                      onClick={() => {
                        setDraft(w.text);
                        setEditing(true);
                      }}
                    >
                      Edit
                    </button>
                    <button
                      type="button"
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
              </div>
            </li>
          ))}
        </ol>
        {empty && agent && (
          <div className={page.emptyOverlay}>
            Send a message to start the conversation.
          </div>
        )}
        {!scroll.atEnd && (
          <button
            type="button"
            className={page.scrollToEnd}
            onClick={() => scroll.toEnd(true)}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              aria-hidden="true"
            >
              <path d="m6 9 6 6 6-6" />
            </svg>
            Scroll to end
          </button>
        )}
      </div>

      <div className={page.dock}>
        <div className={page.dockColumn}>
          <ThreadErrorBanner
            error={
              isThreadErrorBannerDismissedForSession(bannerKey)
                ? null
                : turnError
            }
            onDismiss={() => {
              dismissThreadErrorBannerForSession(bannerKey);
              setDismissed((n) => n + 1);
            }}
          />

          {error && (
            <div className={page.error} role="alert">
              {error}
            </div>
          )}

          <div>
            {ask && (
              <div className={page.askDrawer}>
                <AskCard
                  key={ask.id}
                  ask={ask}
                  pendingCount={asks.length}
                  onRespond={(body) => respond(ask.id, body)}
                  onStop={stop}
                />
              </div>
            )}
            <ChatComposer
              formRef={compact.ref}
              draft={draft}
              onDraftChange={setDraft}
              editing={editing}
              sending={sending}
              running={running}
              onSubmit={submit}
              onCancelEdit={stopEditing}
              onStop={() => void stop().catch(() => {})}
              controls={
                <ComposerModelControls
                  workspaceId={workspaceId}
                  agent={agent}
                  compact={compact.narrow}
                  update={update}
                />
              }
            />
          </div>
        </div>
      </div>
    </section>
  );
}

/** The row's spacing kind: T3 keeps work rows closer than messages. */
function rowKind(row: TimelineRow): string {
  return row.kind === "item" ? row.item.kind : "work";
}

/** Distance from the end, in px, that still counts as at the end. */
const AT_END_SLOP_PX = 64;

/**
 * T3's live follow: the transcript opens at its end and stays there while
 * new content arrives, unless the user scrolled up; then "Scroll to end"
 * shows.
 */
function useStickToEnd() {
  const ref = useRef<HTMLOListElement>(null);
  const atEndRef = useRef(true);
  const [atEnd, setAtEnd] = useState(true);
  const toEnd = useCallback((smooth = false) => {
    const el = ref.current;
    if (!el) return;
    atEndRef.current = true;
    setAtEnd(true);
    if (smooth && typeof el.scrollTo === "function")
      el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    else el.scrollTop = el.scrollHeight;
  }, []);
  const onScroll = useCallback(() => {
    const el = ref.current;
    if (!el) return;
    const end =
      el.scrollHeight - el.scrollTop - el.clientHeight <= AT_END_SLOP_PX;
    if (end === atEndRef.current) return;
    atEndRef.current = end;
    setAtEnd(end);
  }, []);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof MutationObserver === "undefined") return;
    toEnd();
    const mo = new MutationObserver(() => {
      if (atEndRef.current) el.scrollTop = el.scrollHeight;
    });
    mo.observe(el, { childList: true, subtree: true, characterData: true });
    return () => mo.disconnect();
  }, [toEnd]);
  return { ref, atEnd, onScroll, toEnd, follow: () => toEnd() };
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
      className={page.childLink}
      to={`/ws/${encodeURIComponent(ws)}/chat/${encodeURIComponent(id)}`}
    >
      <span className={page.childName}>{a?.name ?? name}</span>
      {a && <span className={page.badge}>{a.harness}</span>}
      {a && (
        <span className={page.statePill} data-state={a.state}>
          <span className={page.stateDot} aria-hidden="true" />
          {a.state}
        </span>
      )}
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
    <div className={page.agentMessage}>
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
        <div className={page.messageMeta}>
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
        <div className={page.card} data-testid="child-card">
          <div className={page.cardNote}>Child agent</div>
          <ChildLink ws={workspaceId} id={item.child} name={item.name} />
        </div>
      );
    case "completion": {
      const r = item.record;
      return (
        <div className={page.card} data-testid="completion-record">
          <div className={page.cardNote}>
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
      return (
        <div className={page.turnEnd}>
          Turn {item.reason}
          {item.error && (
            <div className={styles.text} data-testid="turn-end-error">
              {item.error}
            </div>
          )}
        </div>
      );
    case "agent":
      return <AgentMessage text={item.text} streaming={!!item.streaming} />;
    case "user":
      return <UserMessage text={item.text} />;
    // Tool calls and reasoning are work rows (see deriveTimelineRows).
    case "tool":
    case "reasoning":
      return null;
  }
}
