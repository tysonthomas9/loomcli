// The page layout, message column and scroll-to-end are ported from T3 Code
// apps/web/src/components/ChatView.tsx and
// apps/web/src/components/chat/MessagesTimeline.tsx at commit 2daff8c25.
// Copyright (c) 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md.
import {
  type CSSProperties,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "@/contexts/AuthContext";
import {
  agentColor,
  agentColorIndex,
  latestTurnError,
  ownSender,
  senderAgent,
  trayRows,
  trayWaves,
  useAgentChat,
  useRoster,
  useRosterActivity,
  useRosterAgent,
} from "@/hooks";
import type { WaitingMessage } from "@/api/agentsv1";
import type { ChatItem } from "@/hooks";
import { AgentBadge } from "./AgentBadge";
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
import {
  clockTime,
  MessageActions,
  useLinger,
  UserMessage,
  WorkingRow,
} from "./MessageRows";
import { useSmoothText } from "./useSmoothText";
import {
  dismissThreadErrorBannerForSession,
  getThreadErrorBannerKey,
  isThreadErrorBannerDismissedForSession,
  ThreadErrorBanner,
} from "./ThreadErrorBanner";
import {
  deriveTimelineRows,
  firstLine,
  toolHeading,
  type TimelineRow,
} from "./timelineRows";
import {
  BridgeCallRow,
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
    archive,
    unarchive,
    remove,
    unsaved,
    expired,
    synced,
  } = useAgentChat(workspaceId, agentId);
  const navigate = useNavigate();
  const del = (fingerprint?: string) =>
    remove(fingerprint).then(() =>
      navigate(`/ws/${encodeURIComponent(workspaceId)}/home`),
    );
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
  const entering = useEnteringRows(rows, synced);
  // The agent tray: working children, and results waiting for this agent.
  const roster = useRoster();
  const activity = useRosterActivity();
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
  const working = useLinger(running);
  // The fading working row keeps the time it showed.
  const [since, setSince] = useState(runningSince);
  if (running && runningSince !== since) setSince(runningSince);
  const step = useMemo(() => runningStep(items), [items]);
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
        expired={expired}
        onRename={(name) => update({ name })}
        onArchive={archive}
        onUnarchive={unarchive}
        onDelete={() => del()}
      />

      <div className={page.scroller}>
        <ol
          ref={scroll.ref}
          className={page.transcript}
          data-testid="chat-transcript"
          onScroll={scroll.onScroll}
        >
          {rows.map((row) => (
            <li
              key={row.id}
              className={page.row}
              data-kind={rowKind(row)}
              data-enter={entering.has(row.id) || undefined}
            >
              <Row row={row} workspaceId={workspaceId} onToggle={toggleGroup} />
            </li>
          ))}
          {working.mounted && (
            <li
              className={page.row}
              data-kind="working"
              data-leaving={working.leaving || undefined}
            >
              <WorkingRow
                startedAt={running ? runningSince : since}
                step={running ? step : null}
              />
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

          {agent?.attention_reason && (
            <div
              className={page.attention}
              role="status"
              data-testid="agent-attention-banner"
            >
              Needs attention: {attentionText(agent.attention_reason)}
            </div>
          )}

          {error && (
            <div className={page.error} role="alert">
              {error}
              {unsaved && (
                <>
                  {" "}
                  Delete anyway loses these changes.{" "}
                  <button
                    type="button"
                    className={page.headerAction}
                    data-danger="true"
                    data-testid="agent-delete-anyway"
                    onClick={() => void del(unsaved).catch(() => {})}
                  >
                    Delete anyway
                  </button>
                </>
              )}
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
              activity={activity}
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
            {expired ? (
              <div className={page.notice} data-testid="agent-history-expired">
                History expired: the saved transcript was removed after the
                30-day retention, so it cannot be reopened.
              </div>
            ) : agent?.archived_at ? (
              <div className={page.notice} data-testid="agent-archived-notice">
                Archived: read-only. {daysLeftText(agent.archived_at)} Unarchive
                to send messages again.
              </div>
            ) : (
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
            )}
          </div>
        </div>
      </div>
    </section>
  );
}

/** How long history outlives Archive (R29). */
const RETENTION_DAYS = 30;

/** The days left before an archived agent's history expires, as a sentence. */
export function daysLeftText(archivedAt: string, now = Date.now()): string {
  const t = Date.parse(archivedAt);
  if (Number.isNaN(t)) return "";
  const left = Math.max(
    0,
    Math.ceil((t + RETENTION_DAYS * 86_400_000 - now) / 86_400_000),
  );
  return left === 1
    ? "History expires in 1 day."
    : `History expires in ${left} days.`;
}

/** Plain words for the server's attention reasons; others show as sent. */
const ATTENTION: Record<string, string> = {
  harness_unavailable: "the harness is unavailable.",
  delivery_unknown: "a message may not have been delivered.",
  history_too_large: "the history is too large to load in full.",
  session_missing: "the native session is missing.",
  create_incomplete: "creating the agent did not finish.",
  delete_incomplete: "deleting the agent did not finish.",
};
const attentionText = (reason: string) => ATTENTION[reason] ?? reason;

/**
 * The row's spacing kind. Every work row (work, work-toggle, work-live and
 * thinking) is "work", which gets T3's pb-2 (8px); messages and cards keep
 * pb-4 (16px).
 */
function rowKind(row: TimelineRow): string {
  if (row.kind === "started") return "started";
  return row.kind === "item" ? row.item.kind : "work";
}

/** The running tool's name, for the working row's "· step". */
function runningStep(items: readonly ChatItem[]): string | null {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i]!;
    if (it.kind === "tool" && it.status === "running") return toolHeading(it);
  }
  return null;
}

const isMessageRow = (r: TimelineRow) =>
  r.kind === "item" && (r.item.kind === "user" || r.item.kind === "agent");

/**
 * The ids of user and agent rows that arrived live, which fade in. Rows
 * that a catch-up replays (`synced` false: the first load, a reconnect or a
 * feed.gap) and a completed message replacing its streamed copy do not
 * animate. An id is kept while its row shows, so a later render does not
 * cut its fade short.
 */
function useEnteringRows(
  rows: readonly TimelineRow[],
  synced: boolean,
): ReadonlySet<string> {
  const prev = useRef<ReadonlyMap<string, boolean> | null>(null);
  const entering = useRef(new Set<string>()).current;
  useMemo(() => {
    const now = new Map(rows.map((r) => [r.id, isMessageRow(r)]));
    const before = prev.current;
    prev.current = synced ? now : null;
    entering.forEach((id) => now.has(id) || entering.delete(id));
    if (!before) return;
    const added = rows.filter((r) => !before.has(r.id));
    const replaced = [...before].some(([id, msg]) => msg && !now.has(id));
    if (replaced) return;
    added.filter(isMessageRow).forEach((r) => entering.add(r.id));
  }, [rows, synced, entering]);
  return entering;
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

/** A started child as its colour avatar and name, linking to its chat. */
function ChildChip({ ws, id, name }: { ws: string; id: string; name: string }) {
  const shown = useRosterAgent(id)?.name ?? name;
  return (
    <Link className={tray.childChip} to={chatPath(ws, id)}>
      <AgentBadge id={id} name={shown} size={16} />
      <AgentName id={id} name={name} />
    </Link>
  );
}

/**
 * Children started back to back as one quiet line (CL1): "↳ Started [U1]
 * ui-test-agent-1 [U2] ui-test-agent-2 · 2 tool calls ›", the Lead's
 * agent_create calls folded into the toggle.
 */
function StartedMarker({
  row,
  workspaceId,
  onToggle,
}: {
  row: Extract<TimelineRow, { kind: "started" }>;
  workspaceId: string;
  onToggle: () => void;
}) {
  const time = clockTime(row.item.at);
  const n = row.calls.length;
  return (
    <div
      className={tray.marker}
      data-testid="started-marker"
      title={time ? `Started at ${time}` : undefined}
    >
      <span aria-hidden="true">↳</span>
      <span>Started</span>
      {row.item.children.map((c) => (
        <ChildChip key={c.child} ws={workspaceId} id={c.child} name={c.name} />
      ))}
      {n > 0 && (
        <button
          type="button"
          className={tray.calls}
          aria-expanded={row.expanded}
          onClick={onToggle}
        >
          {n} tool {n === 1 ? "call" : "calls"}{" "}
          <span className={tray.callsChevron} aria-hidden="true">
            ›
          </span>
        </button>
      )}
    </div>
  );
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

/**
 * A child's result as one E1 card (CL1): its colour avatar, name and
 * outcome, then the result's first line. The whole card links to the
 * child's chat (click, Enter or Space); the time and › show only on hover
 * or focus.
 */
function CompletionCard({
  item,
  workspaceId,
}: {
  item: Extract<ChatItem, { kind: "completion" }>;
  workspaceId: string;
}) {
  const navigate = useNavigate();
  const [reveal, setReveal] = useState({ hover: false, focus: false });
  const r = item.record;
  const ok = r.outcome === "completed";
  const name = useRosterAgent(r.child)?.name ?? item.name;
  const time = clockTime(item.at);
  const where =
    r.branch && `${r.branch}${r.head ? `@${r.head.slice(0, 7)}` : ""}`;
  const result = r.summary ? firstLine(r.summary, 400) : "";
  const shown = reveal.hover || reveal.focus;
  const open = () => navigate(chatPath(workspaceId, r.child));
  return (
    <div
      className={tray.card}
      role="link"
      tabIndex={0}
      style={{ "--agent-color": agentColor(r.child) } as CSSProperties}
      data-agent-color={agentColorIndex(r.child)}
      data-testid="completion-record"
      data-outcome={r.outcome}
      data-attempt={r.attempt}
      data-delivery={item.delivery}
      title={where || undefined}
      onClick={open}
      onKeyDown={(e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          open();
        }
      }}
      onMouseEnter={() => setReveal((v) => ({ ...v, hover: true }))}
      onMouseLeave={() => setReveal((v) => ({ ...v, hover: false }))}
      onFocus={() => setReveal((v) => ({ ...v, focus: true }))}
      onBlur={() => setReveal((v) => ({ ...v, focus: false }))}
    >
      <AgentBadge id={r.child} name={name} size={18} />
      <span className={tray.cardName}>{name}</span>
      <span className={tray.cardState}>
        <span className={ok ? tray.cardOk : tray.cardFail}>
          {ok ? "✓ done" : `✕ ${r.outcome}`}
        </span>
        {/* Attempts count from 0; people count from 1. */}
        {r.attempt > 0 && ` · attempt ${r.attempt + 1}`}
        {item.delivery === "waiting" && (
          <span className={tray.tag} data-delivery="waiting">
            waiting for Lead
          </span>
        )}
      </span>
      {shown && time && (
        <span className={tray.cardTime} data-testid="card-time">
          {time}
        </span>
      )}
      {shown && (
        <span className={tray.cardGo} aria-hidden="true">
          ›
        </span>
      )}
      {result && (
        <span className={tray.cardResult} data-failed={!ok}>
          {result}
        </span>
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
      return (
        <WorkEntryRow
          entry={row.entry}
          inGroup={row.inGroup}
          label={row.label}
        />
      );
    case "started":
      return (
        <StartedMarker
          row={row}
          workspaceId={workspaceId}
          onToggle={() => onToggle(row.groupId)}
        />
      );
    case "bridge":
      return <BridgeCallRow row={row} />;
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
  at,
}: {
  text: string;
  streaming: boolean;
  at?: string | undefined;
}) {
  const [all, setAll] = useState(false);
  const smooth = useSmoothText(text, streaming);
  const cut = !all && smooth.text.length > LONG_TEXT_LIMIT;
  return (
    <div className={page.agentMessage}>
      <ChatMarkdown
        text={cut ? smooth.text.slice(0, LONG_TEXT_LIMIT) + "…" : smooth.text}
        streaming={streaming}
        fresh={smooth.fresh}
      />
      {cut && (
        <button className={styles.showAll} onClick={() => setAll(true)}>
          Show all ({text.length.toLocaleString()} characters)
        </button>
      )}
      {!streaming && text.trim() && <MessageActions text={text} at={at} />}
    </div>
  );
}

function Item({ item, workspaceId }: { item: ChatItem; workspaceId: string }) {
  switch (item.kind) {
    // Started markers are their own rows (see deriveTimelineRows).
    case "started":
      return null;
    case "completion":
      return <CompletionCard item={item} workspaceId={workspaceId} />;
    case "from_agent":
      return <FromAgent id={item.agent} name={item.name} text={item.text} />;
    // A fresh native context; the transcript above stays readable.
    case "harness_changed":
      return (
        <div
          className={page.contextDivider}
          data-testid="harness-context-divider"
        >
          New harness context
          {item.to && `: ${item.from ? `${item.from} → ` : ""}${item.to}`}
        </div>
      );
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
      return (
        <AgentMessage
          text={item.text}
          streaming={!!item.streaming}
          at={item.at}
        />
      );
    case "user":
      return <UserMessage text={item.text} at={item.at} />;
    // Tool calls and reasoning are work rows (see deriveTimelineRows).
    case "tool":
    case "reasoning":
      return null;
  }
}
