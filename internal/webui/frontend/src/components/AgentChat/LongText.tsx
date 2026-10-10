import { useState } from "react";
import styles from "./AgentChat.module.css";

/** Characters shown before "Show all", so a huge message cannot stall the page. */
export const LONG_TEXT_LIMIT = 8000;

/**
 * Untrusted text as plain text: React escapes it, and the CSS wraps any
 * unbroken run. Text over the limit is cut until the user expands it.
 */
export function LongText({ text }: { text: string }) {
  const [all, setAll] = useState(false);
  const cut = !all && text.length > LONG_TEXT_LIMIT;
  return (
    <div className={styles.text}>
      {cut ? text.slice(0, LONG_TEXT_LIMIT) + "…" : text}
      {cut && (
        <button className={styles.showAll} onClick={() => setAll(true)}>
          Show all ({text.length.toLocaleString()} characters)
        </button>
      )}
    </div>
  );
}
