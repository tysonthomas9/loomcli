// The page layout, message column and scroll-to-end are ported from T3 Code
// apps/web/src/components/ChatView.tsx and
// apps/web/src/components/chat/MessagesTimeline.tsx at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import {
  latestTurnError,
  ownSender,
  senderAgent,
  trayRows,
  trayWaves,
  useAgentChat,
  useRoster,
  useRosterAgent,
} from "@/hooks";
import type { WaitingMessage } from "@/api/agentsv1";
import type { ChatItem } from "@/hooks";
import { AgentTray, chatPath, HarnessIcon } from "./AgentTray";
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
import tray from "./AgentTray.module.css";

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
  // The agent tray: working children, and results waiting for this agent.
  const roster = useRoster();
  const [trayOpen, setTrayOpen] = useState(false);
  const tRows = useMemo(
    () =>
      trayRows(agentId, roster.values(), items, agent?.waiting_messages ?? []),
    [agentId, roster, items, agent?.waiting_messages],
  );
  const tWaves = useMemo(() => trayWaves(tRows, items), [tRows, items]);
  const names = useMemo(
    () =>
      new Map(
        items.flatMap((i) =>
          i.kind === "started"
            ? i.children.map((c) => [c.child, c.name] as const)
            : [],
        ),
      ),
    [items],
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
    setTrayOpen(false); // the open tray hides the latest lines (R2)
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
          {waiting.map((w) =>
            senderAgent(w.sender) ? (
              <AgentWaiting key={`waiting:${w.sender}`} w={w} names={names} />
            ) : (
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
            ),
          )}
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
            <AgentTray
              workspaceId={workspaceId}
              rows={tRows}
              waves={tWaves}
              open={trayOpen}
              onOpenChange={setTrayOpen}
              narrow={compact.narrow}
              tucked={!ask}
            />
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

/**
 * The row's spacing kind. Every work row (work, work-toggle, work-live and
 * thinking) is "work", which gets T3's pb-2 (8px); messages and cards keep
 * pb-4 (16px).
 */
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

/** An agent's name, live from the roster, else the name it was saved with. */
function AgentName({ id, name }: { id: string; name: string }) {
  return <>{useRosterAgent(id)?.name ?? name}</>;
}

/** An agent's name as a link to its chat. */
function NameLink({ ws, id, name }: { ws: string; id: string; name: string }) {
  return (
    <Link to={chatPath(ws, id)}>
      <AgentName id={id} name={name} />
    </Link>
  );
}

/** A clock time such as 2:45 PM, or "" when the stamp does not parse. */
function clockTime(at: string): string {
  const t = Date.parse(at);
  return Number.isNaN(t)
    ? ""
    : new Date(t).toLocaleTimeString([], {
        hour: "numeric",
        minute: "2-digit",
      });
}

/** A message another agent sent on purpose: "from <name>", as markdown. */
function FromAgent({
  id,
  name,
  text,
  waiting,
}: {
  id: string;
  name: string;
  text: string;
  waiting?: boolean;
}) {
  const a = useRosterAgent(id);
  return (
    <div
      className={tray.fromAgent}
      data-testid="from-agent"
      data-waiting={!!waiting}
    >
      <div className={tray.fromTitle}>
        {a && <HarnessIcon harness={a.harness} />}
        {waiting && "Waiting · "}
        <span>
          from <strong>{a?.name ?? name}</strong>
        </span>
      </div>
      <ChatMarkdown text={text} streaming={false} />
    </div>
  );
}

/**
 * A waiting message from an agent: its text without the child records it
 * carries (those show on their completion markers), or nothing when only
 * records wait.
 */
function AgentWaiting({
  w,
  names,
}: {
  w: WaitingMessage;
  names: ReadonlyMap<string, string>;
}) {
  const id = senderAgent(w.sender) ?? w.sender;
  const text = w.completions ? (w.message ?? "") : w.text;
  if (!text.trim()) return null;
  return (
    <li className={page.row} data-kind="waiting">
      <FromAgent id={id} name={names.get(id) ?? id} text={text} waiting />
    </li>
  );
}

/** A child result's one-line marker, with its delivery state and summary. */
function CompletionMarker({
  item,
  workspaceId,
}: {
  item: Extract<ChatItem, { kind: "completion" }>;
  workspaceId: string;
}) {
  const [open, setOpen] = useState(false);
  const r = item.record;
  const ok = r.outcome === "completed";
  const time = clockTime(item.at);
  const where =
    r.branch && `${r.branch}${r.head ? `@${r.head.slice(0, 7)}` : ""}`;
  return (
    <div
      className={tray.marker}
      data-testid="completion-record"
      data-outcome={r.outcome}
      data-attempt={r.attempt}
      data-delivery={item.delivery}
      title={where || undefined}
    >
      <span className={ok ? tray.ok : tray.fail} aria-hidden="true">
        {ok ? "✓" : "✕"}
      </span>
      <span>
        <NameLink ws={workspaceId} id={r.child} name={item.name} />{" "}
        {ok ? "done" : r.outcome}
        {/* Attempts count from 0; people count from 1. */}
        {r.attempt > 0 && ` · attempt ${r.attempt + 1}`}
        {time && ` · ${time}`}
      </span>
      {item.delivery === "waiting" && (
        <span className={tray.tag} data-delivery="waiting">
          waiting for Lead
        </span>
      )}
      {item.delivery === "delivered" && (
        <span className={tray.tag} data-delivery="delivered">
          · Lead read the result
        </span>
      )}
      {r.summary && (
        <button
          type="button"
          className={tray.view}
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          {open ? "hide" : "view"}
        </button>
      )}
      {open && r.summary && (
        <div className={tray.markerBody}>
          {where && <div className={tray.meta}>{where}</div>}
          <LongText text={r.summary} />
        </div>
      )}
    </div>
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
    case "started": {
      const time = clockTime(item.at);
      return (
        <div className={tray.marker} data-testid="started-marker">
          <span aria-hidden="true">↳</span>
          <span>
            Started{" "}
            {item.children.map((c, i) => (
              <Fragment key={c.child}>
                {i > 0 && ", "}
                <NameLink ws={workspaceId} id={c.child} name={c.name} />
              </Fragment>
            ))}
            {time && ` · ${time}`}
          </span>
        </div>
      );
    }
    case "completion":
      return <CompletionMarker item={item} workspaceId={workspaceId} />;
    case "from_agent":
      return <FromAgent id={item.agent} name={item.name} text={item.text} />;
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
