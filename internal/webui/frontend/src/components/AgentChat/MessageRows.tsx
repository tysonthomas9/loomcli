// Ported from T3 Code apps/web/src/components/chat/MessagesTimeline.tsx
// (UserTimelineRow, CollapsibleUserMessageBody, AssistantTimelineRow's meta
// row, WorkingTimelineRow and WorkingTimer) at commit 2daff8c25. Copyright (c)
// 2026 T3 Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md. Loom has no
// attachments, terminal contexts, timestamps or revert on a user message.
import { useEffect, useRef, useState, type ReactNode } from "react";
import { LongText } from "./LongText";
import { MessageCopyButton } from "./MessageCopyButton";
import styles from "./ChatPage.module.css";

const MAX_COLLAPSED_USER_MESSAGE_LINES = 8;
const MAX_COLLAPSED_USER_MESSAGE_LENGTH = 600;

/** Whether a user message is long enough to fold behind "Show full message". */
export function shouldCollapseUserMessage(text: string): boolean {
  if (text.trim().length === 0) return false;
  return (
    text.length > MAX_COLLAPSED_USER_MESSAGE_LENGTH ||
    text.split("\n").length > MAX_COLLAPSED_USER_MESSAGE_LINES
  );
}

/**
 * T3's user bubble: right-aligned, at most 80% wide, a long message folded
 * with a fade until "Show full message", and a copy button under it on hover.
 */
export function UserMessage({
  text,
  footer,
}: {
  text: string;
  footer?: ReactNode;
}) {
  const [expanded, setExpanded] = useState(false);
  const canCollapse = shouldCollapseUserMessage(text);
  const collapsed = canCollapse && !expanded;
  return (
    <div className={styles.userRow}>
      <div className={styles.userBubble}>
        <div
          className={styles.userBody}
          data-user-message-collapsed={collapsed ? "true" : "false"}
        >
          <LongText text={text} />
        </div>
        {(canCollapse || footer) && (
          <div className={styles.userFooter}>
            {canCollapse && (
              <button
                type="button"
                className={styles.collapseToggle}
                aria-expanded={expanded}
                onClick={() => setExpanded((v) => !v)}
              >
                {expanded ? "Show less" : "Show full message"}
              </button>
            )}
            {footer && <div className={styles.userFooterEnd}>{footer}</div>}
          </div>
        )}
      </div>
      {text.trim() && (
        <div className={styles.userMeta}>
          <MessageCopyButton text={text} label="Copy your message" />
        </div>
      )}
    </div>
  );
}

/** "Working for Ns" while a turn runs (T3's WorkingTimelineRow). */
export function WorkingRow({ startedAt }: { startedAt: string | null }) {
  return (
    <div className={styles.working} data-testid="working-row">
      {startedAt ? (
        <>
          Working for <WorkingTimer startedAt={startedAt} />
        </>
      ) : (
        "Working..."
      )}
    </div>
  );
}

/** The elapsed time, ticking every second without re-rendering the chat. */
function WorkingTimer({ startedAt }: { startedAt: string }) {
  const ref = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    const tick = () => {
      if (ref.current) ref.current.textContent = formatElapsed(startedAt);
    };
    tick();
    const id = setInterval(tick, 1000);
    return () => clearInterval(id);
  }, [startedAt]);
  return (
    <span ref={ref} className={styles.tabular}>
      {formatElapsed(startedAt)}
    </span>
  );
}

/** T3's formatWorkingTimer: 5s, 1m, 1m 5s, 1h, 1h 2m. */
export function formatElapsed(startIso: string, now = Date.now()): string {
  const start = Date.parse(startIso);
  if (!Number.isFinite(start)) return "0s";
  const elapsed = Math.max(0, Math.floor((now - start) / 1000));
  if (elapsed < 60) return `${elapsed}s`;
  const hours = Math.floor(elapsed / 3600);
  const minutes = Math.floor((elapsed % 3600) / 60);
  const seconds = elapsed % 60;
  if (hours > 0) return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`;
  return seconds > 0 ? `${minutes}m ${seconds}s` : `${minutes}m`;
}
