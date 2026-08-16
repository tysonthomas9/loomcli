import { useState } from "react";

import { TerminalView } from "@/components/TerminalView/TerminalView";

import { archiveReviewer } from "../api/prReview";
import { usePRReviewConversation } from "../usePRReviewConversation";
import styles from "./PRDiscussionPanel.module.css";

interface PRDiscussionPanelProps {
  workspaceId: string;
  owner: string;
  repo: string;
  number: number;
  onClose: () => void;
  onStaleSubject?: () => void | Promise<void>;
}

type Tab = "chat" | "terminal";

export function PRDiscussionPanel({
  workspaceId,
  owner,
  repo,
  number,
  onClose,
  onStaleSubject,
}: PRDiscussionPanelProps): JSX.Element {
  const [activeTab, setActiveTab] = useState<Tab>("chat");
  const [text, setText] = useState("");
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState<string | null>(null);
  const { agentName, messages, state, detail, sending, send, retry, error } =
    usePRReviewConversation({
      workspaceId,
      owner,
      repo,
      number,
      enabled: true,
      ...(onStaleSubject ? { onStaleSubject } : {}),
    });
  // unsupported: this backend has no readable chat (terminal still works).
  // failed: the reviewer runtime died — a sent message would only queue
  // invisibly, so hold the composer shut in both states.
  const chatUnavailable = state === "unsupported" || state === "failed";
  const preparingReviewer = !agentName && !error;
  const canSend =
    Boolean(agentName) &&
    text.trim().length > 0 &&
    !sending &&
    !chatUnavailable &&
    !closing;
  const panelError = closeError ?? error;

  const close = async (): Promise<void> => {
    if (closing) return;
    if (!agentName) {
      onClose();
      return;
    }
    setClosing(true);
    setCloseError(null);
    try {
      await archiveReviewer(workspaceId, owner, repo, number);
      onClose();
    } catch (err) {
      setCloseError(
        err instanceof Error ? err.message : "Failed to end PR review",
      );
    } finally {
      setClosing(false);
    }
  };

  const submit = async (): Promise<void> => {
    if (!canSend) return;
    // Clear the composer only after a successful send so a failed send doesn't
    // lose the user's typed message.
    const ok = await send(text);
    if (ok) setText("");
  };

  return (
    <aside className={styles.panel} data-testid="pr-discussion-panel">
      <header className={styles.header}>
        <div className={styles.titleBlock}>
          <h2 className={styles.title}>Discuss PR</h2>
          <span className={styles.status}>{state}</span>
        </div>
        <div className={styles.tabs} role="tablist" aria-label="PR discussion">
          <button
            type="button"
            className={activeTab === "chat" ? styles.tabActive : styles.tab}
            data-testid="pr-discussion-tab-chat"
            aria-selected={activeTab === "chat"}
            onClick={() => setActiveTab("chat")}
          >
            Chat
          </button>
          <button
            type="button"
            className={activeTab === "terminal" ? styles.tabActive : styles.tab}
            data-testid="pr-discussion-tab-terminal"
            aria-selected={activeTab === "terminal"}
            onClick={() => setActiveTab("terminal")}
          >
            Terminal
          </button>
        </div>
        <button
          type="button"
          className={styles.closeButton}
          aria-label="Close discussion"
          disabled={closing || preparingReviewer}
          title={
            preparingReviewer
              ? "Preparing the checkout reviewer"
              : "End this review and archive its checkout-specific Agent"
          }
          onClick={() => void close()}
        >
          {closing || preparingReviewer ? "…" : "×"}
        </button>
      </header>

      {panelError && (
        <div className={styles.error} data-testid="pr-discussion-error">
          <span>{panelError}</span>
          {!agentName && !closeError && (
            <button
              type="button"
              className={styles.retryButton}
              data-testid="pr-discussion-retry"
              onClick={retry}
            >
              Retry
            </button>
          )}
        </div>
      )}

      <div
        className={styles.chatTab}
        data-testid="pr-chat"
        style={{ display: activeTab === "chat" ? "flex" : "none" }}
      >
        <div className={styles.messages} data-testid="pr-chat-messages">
          {chatUnavailable ? (
            <div className={styles.empty} data-testid="pr-chat-unavailable">
              <p>
                {detail ??
                  (state === "failed"
                    ? "The review agent stopped unexpectedly. Close and reopen the reviewer to restart it."
                    : "The chat view is not available for this reviewer backend.")}
              </p>
              {state === "unsupported" && (
                <button
                  type="button"
                  className={styles.retryButton}
                  data-testid="pr-chat-open-terminal"
                  onClick={() => setActiveTab("terminal")}
                >
                  Open Terminal
                </button>
              )}
            </div>
          ) : messages.length === 0 ? (
            <div className={styles.empty}>
              Starting the review agent — it will read the PR head and respond
              here.
            </div>
          ) : (
            messages.map((message) => (
              <div
                key={message.item_id}
                className={
                  message.role === "user"
                    ? styles.messageUser
                    : styles.messageAssistant
                }
              >
                <div className={styles.messageText}>{message.text}</div>
              </div>
            ))
          )}
        </div>
        <div className={styles.composer}>
          <textarea
            className={styles.textarea}
            data-testid="pr-chat-composer"
            disabled={chatUnavailable}
            value={text}
            onChange={(event) => setText(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey) {
                event.preventDefault();
                void submit();
              }
            }}
            placeholder="Ask the reviewer…"
            rows={3}
          />
          <button
            type="button"
            className={styles.sendButton}
            data-testid="pr-chat-send"
            disabled={!canSend}
            onClick={() => void submit()}
          >
            {sending ? "Sending" : "Send"}
          </button>
        </div>
      </div>

      {agentName && (
        <div
          className={styles.terminalTab}
          style={{ display: activeTab === "terminal" ? "block" : "none" }}
        >
          <TerminalView isActive={true} pendingAgentName={agentName} hideTabs />
        </div>
      )}
    </aside>
  );
}
