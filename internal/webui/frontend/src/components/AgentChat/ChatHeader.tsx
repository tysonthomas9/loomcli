// Ported from T3 Code apps/web/src/components/chat/ChatHeader.tsx at commit
// 2daff8c25. Copyright (c) 2026 T3 Tools Inc. MIT License; see
// THIRD_PARTY_NOTICES.md. Kept: the title with its inline rename (double-click
// or the pencil, Enter commits, Escape cancels, blur commits) and
// resolveRenameCommit. Loom adds the harness label and state pill; T3's
// project breadcrumb, thread menu, project scripts, open-in and git actions
// are left out.
import { useRef, useState, type KeyboardEvent } from "react";
import type { Agent } from "@/api/agentsv1";
import styles from "./ChatPage.module.css";

/**
 * Rename commit rule (T3's): trim, reject empty, and skip the request when
 * nothing changed.
 */
export function resolveRenameCommit(input: {
  readonly title: string;
  readonly originalTitle: string;
}):
  | { action: "commit"; title: string }
  | { action: "reject-empty" }
  | { action: "noop" } {
  const trimmed = input.title.trim();
  if (trimmed.length === 0) return { action: "reject-empty" };
  if (trimmed === input.originalTitle) return { action: "noop" };
  return { action: "commit", title: trimmed };
}

/**
 * The chat's header: the agent's name (renamed inline through PATCH name),
 * its harness as a label, its state as a pill, and Unarchive (design v2
 * §4.7). Archive and Delete are in the sidebar row's menu (SB4). The same
 * for every harness.
 */
export function ChatHeader({
  agentId,
  agent,
  expired,
  onRename,
  onUnarchive,
}: {
  agentId: string;
  agent: Agent | null;
  /** The saved history is gone, so Unarchive would fail. */
  expired: boolean;
  /** PATCHes the name; rejects after the chat shows the error. */
  onRename: (name: string) => Promise<void>;
  /** Rejects after the chat shows the error. */
  onUnarchive: () => Promise<void>;
}) {
  const title = agent?.name ?? agentId;
  const [renaming, setRenaming] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const act = (call: () => Promise<void>) => {
    setBusy(true);
    void call()
      .catch(() => {})
      .finally(() => setBusy(false));
  };
  const committed = useRef(false);

  const start = () => {
    if (!agent) return;
    committed.current = false;
    setRenaming(title);
  };
  const commit = (value: string) => {
    setRenaming(null);
    const r = resolveRenameCommit({ title: value, originalTitle: title });
    if (r.action === "commit") void onRename(r.title).catch(() => {});
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === "Enter") {
      committed.current = true;
      commit(e.currentTarget.value);
    } else if (e.key === "Escape") {
      committed.current = true;
      setRenaming(null);
    }
  };

  return (
    <header className={styles.header}>
      <div className={styles.titleWrap}>
        {renaming !== null ? (
          <input
            autoFocus
            aria-label="Agent name"
            className={styles.titleInput}
            defaultValue={renaming}
            onBlur={(e) => {
              if (committed.current) return;
              commit(e.currentTarget.value);
            }}
            onFocus={(e) => e.currentTarget.select()}
            onKeyDown={onKeyDown}
          />
        ) : (
          <>
            <h2
              className={styles.title}
              title={title}
              onDoubleClick={(e) => {
                if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
                start();
              }}
            >
              {title}
            </h2>
            {agent && (
              <button
                type="button"
                className={styles.renameButton}
                aria-label="Rename agent"
                title="Rename agent"
                onClick={start}
              >
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden="true"
                >
                  <path d="M21.17 6.81a1 1 0 0 0-3.98-3.98L3.84 16.17a2 2 0 0 0-.5.83l-1.32 4.35a.5.5 0 0 0 .62.62l4.35-1.32a2 2 0 0 0 .83-.5z" />
                </svg>
              </button>
            )}
          </>
        )}
      </div>
      {agent && (
        <div className={styles.headerMeta}>
          <span className={styles.badge} data-testid="harness-label">
            {agent.harness}
          </span>
          <span
            className={styles.statePill}
            data-state={agent.state}
            data-running={agent.running_turn_id ? "true" : undefined}
          >
            <span className={styles.stateDot} aria-hidden="true" />
            {agent.state}
          </span>
          {agent.history_purge_failed_at && !agent.history_purged_at && (
            <span className={styles.statePill} role="status">
              History expiry incomplete
            </span>
          )}
          {agent.archived_at && !expired && (
            <button
              type="button"
              className={styles.headerAction}
              data-testid="agent-unarchive"
              disabled={busy}
              onClick={() => act(onUnarchive)}
            >
              Unarchive
            </button>
          )}
        </div>
      )}
    </header>
  );
}
