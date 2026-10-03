// Ported from T3 Code apps/web/src/components/chat/ChatComposer.tsx (the
// composer card, its prompt area and bottom toolbar),
// apps/web/src/components/chat/ComposerPrimaryActions.tsx (the round send and
// stop buttons) and apps/web/src/components/ComposerPromptEditor.tsx (the
// prompt's min and max height) at commit 2daff8c25. Copyright (c) 2026 T3
// Tools Inc. MIT License; see THIRD_PARTY_NOTICES.md. Loom keeps a plain
// textarea (no Lexical), sends while a turn runs (the message waits), and
// leaves out T3's attachments, slash menu, plan/build and runtime modes.
import { useLayoutEffect, useRef, type ReactNode } from "react";
import styles from "./ChatPage.module.css";

/** T3's prompt editor height bounds (min-h-17.5, max-h-50). */
export const PROMPT_MIN_HEIGHT_PX = 70;
export const PROMPT_MAX_HEIGHT_PX = 200;

/** Why Send is disabled, shown as its tooltip; null when it is enabled. */
export function sendDisabledReason(input: {
  draft: string;
  sending: boolean;
}): string | null {
  if (input.sending) return "Sending…";
  if (!input.draft.trim()) return "Type a message to send";
  return null;
}

export interface ChatComposerProps {
  draft: string;
  onDraftChange: (text: string) => void;
  /** Editing the waiting message: Send saves it, and Cancel shows. */
  editing: boolean;
  sending: boolean;
  running: boolean;
  onSubmit: () => void;
  onCancelEdit: () => void;
  onStop: () => void;
  /** The model and effort pickers (UI2), on the toolbar's left. */
  controls: ReactNode;
  formRef?: React.Ref<HTMLFormElement>;
}

/**
 * T3's composer card: a rounded card with an auto-growing prompt and a
 * toolbar along its bottom edge, the pickers on the left and a round send
 * button on the right that shows Stop while a turn runs. Enter sends,
 * Shift+Enter adds a line.
 */
export function ChatComposer({
  draft,
  onDraftChange,
  editing,
  sending,
  running,
  onSubmit,
  onCancelEdit,
  onStop,
  controls,
  formRef,
}: ChatComposerProps) {
  const inputRef = useRef<HTMLTextAreaElement>(null);
  useAutoGrow(inputRef, draft);
  const disabledReason = sendDisabledReason({ draft, sending });
  const sendLabel = editing ? "Save" : "Send";
  // T3 swaps Send for Stop while a turn runs; Loom also sends then (the
  // message waits), so Send comes back as soon as there is text.
  const showSend = !running || !!draft.trim() || editing;

  return (
    <form
      ref={formRef}
      className={styles.composer}
      data-chat-composer-form="true"
      onSubmit={(e) => {
        e.preventDefault();
        onSubmit();
      }}
    >
      <div
        className={styles.composerSurface}
        data-editing={editing || undefined}
      >
        <div className={styles.promptArea}>
          <textarea
            ref={inputRef}
            aria-label="Message"
            className={styles.prompt}
            rows={1}
            value={draft}
            placeholder={
              editing ? "Edit your waiting message" : "Ask anything..."
            }
            onChange={(e) => onDraftChange(e.target.value)}
            onKeyDown={(e) => {
              if (
                e.key === "Enter" &&
                !e.shiftKey &&
                !e.nativeEvent.isComposing
              ) {
                e.preventDefault();
                onSubmit();
              }
            }}
          />
        </div>
        <div className={styles.composerFooter} data-chat-composer-footer="true">
          <div className={styles.composerControls}>{controls}</div>
          <div
            className={styles.composerActions}
            data-chat-composer-actions="right"
          >
            {editing && (
              <button
                type="button"
                className={styles.cancelButton}
                onClick={onCancelEdit}
              >
                Cancel
              </button>
            )}
            {running && (
              <button
                type="button"
                className={styles.stopButton}
                title="Stop the running turn"
                onClick={onStop}
              >
                <svg
                  width="12"
                  height="12"
                  viewBox="0 0 12 12"
                  fill="currentColor"
                  aria-hidden="true"
                >
                  <rect x="2" y="2" width="8" height="8" rx="1.5" />
                </svg>
                <span className={styles.srOnly}>Stop</span>
              </button>
            )}
            {showSend && (
              <button
                type="submit"
                className={styles.sendButton}
                disabled={disabledReason !== null}
                aria-busy={sending || undefined}
                title={disabledReason ?? (editing ? "Save" : "Send message")}
              >
                {sending ? (
                  <span className={styles.spinner} aria-hidden="true" />
                ) : editing ? (
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 14 14"
                    fill="none"
                    aria-hidden="true"
                  >
                    <path
                      d="M2.5 7.5L5.5 10.5L11.5 3.5"
                      stroke="currentColor"
                      strokeWidth="1.8"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                ) : (
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 14 14"
                    fill="none"
                    aria-hidden="true"
                  >
                    <path
                      d="M7 11.5V2.5M7 2.5L3 6.5M7 2.5L11 6.5"
                      stroke="currentColor"
                      strokeWidth="1.8"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                )}
                <span className={styles.srOnly}>{sendLabel}</span>
              </button>
            )}
          </div>
        </div>
      </div>
    </form>
  );
}

/** Grows the textarea with its text between T3's min and max height. */
function useAutoGrow(ref: React.RefObject<HTMLTextAreaElement>, value: string) {
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    const h = Math.min(
      Math.max(el.scrollHeight, PROMPT_MIN_HEIGHT_PX),
      PROMPT_MAX_HEIGHT_PX,
    );
    el.style.height = `${h}px`;
    el.style.overflowY =
      el.scrollHeight > PROMPT_MAX_HEIGHT_PX ? "auto" : "hidden";
  }, [ref, value]);
}
