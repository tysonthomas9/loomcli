import { useState } from "react";
import type { Ask, RespondBody } from "@/api/agentsv1";
import { LongText } from "./LongText";
import styles from "./AgentChat.module.css";

export interface AskCardProps {
  ask: Ask;
  /** Resolves when the server took the answer; rejects to re-enable the card. */
  onRespond: (body: RespondBody) => Promise<void>;
}

/** One card for every harness's approval or question (design v2 §9.3). */
export function AskCard({ ask, onRespond }: AskCardProps) {
  const [answer, setAnswer] = useState("");
  const [pending, setPending] = useState(false);
  const respond = (body: RespondBody) => {
    setPending(true);
    onRespond(body).catch(() => setPending(false));
  };
  const question = ask.type === "question";
  return (
    <div
      className={styles.askCard}
      role="group"
      aria-label={question ? "Question" : "Approval"}
      data-testid="ask-card"
    >
      <div className={styles.askTitle}>
        {question ? "The agent asks" : "The agent asks for approval"}
      </div>
      <LongText text={ask.about} />
      {question ? (
        <form
          className={styles.askActions}
          onSubmit={(e) => {
            e.preventDefault();
            if (answer.trim()) respond({ answer });
          }}
        >
          <textarea
            className={styles.askInput}
            aria-label="Answer"
            value={answer}
            disabled={pending}
            onChange={(e) => setAnswer(e.target.value)}
          />
          <button type="submit" disabled={pending || !answer.trim()}>
            Answer
          </button>
        </form>
      ) : (
        <div className={styles.askActions}>
          <button
            disabled={pending}
            onClick={() => respond({ decision: "allow_once" })}
          >
            Allow once
          </button>
          <button
            disabled={pending}
            onClick={() => respond({ decision: "allow_always" })}
          >
            Always allow
          </button>
          <button
            disabled={pending}
            onClick={() => respond({ decision: "deny" })}
          >
            Deny
          </button>
        </div>
      )}
    </div>
  );
}
