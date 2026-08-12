import { useState } from "react";

import type { ReviewerMessage } from "@/api/workspace/prReview";
import { MarkdownRenderer } from "@/components/IssueDetailPanel";
import { TerminalView } from "@/components/TerminalView";
import { ToolPill } from "@/components/ToolPill";
import { usePRReviewConversation } from "@/hooks/workspace";
import { argPreviewFromJSON } from "@/utils/toolPreview";

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

function isToolMessage(message: ReviewerMessage): boolean {
  return message.kind === "tool_use" || Boolean(message.tool_name);
}

function ReviewerToolPill({
  message,
}: {
  message: ReviewerMessage;
}): JSX.Element {
  const [expanded, setExpanded] = useState(false);
  return (
    <ToolPill
      name={message.tool_name || "tool"}
      arg={argPreviewFromJSON(message.tool_input)}
      input={(message.tool_input ?? "").trim()}
      result={(message.tool_result ?? "").trim()}
      expanded={expanded}
      onToggle={() => setExpanded((v) => !v)}
      className={styles.toolBlock}
      testId="pr-chat-tool"
    />
  );
}

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
  const canSend =
    Boolean(agentName) &&
    text.trim().length > 0 &&
    !sending &&
    !chatUnavailable;

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
          onClick={onClose}
        >
          ×
        </button>
      </header>

      {error && (
        <div className={styles.error} data-testid="pr-discussion-error">
          <span>{error}</span>
          {!agentName && (
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
            messages.map((message) =>
              isToolMessage(message) ? (
                <ReviewerToolPill key={message.item_id} message={message} />
              ) : (
                <div
                  key={message.item_id}
                  className={
                    message.role === "user"
                      ? styles.messageUser
                      : styles.messageAssistant
                  }
                >
                  <MarkdownRenderer
                    content={message.text}
                    className={styles.messageText}
                  />
                </div>
              ),
            )
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
