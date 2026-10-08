import styles from "./AgentChat.module.css";

/** Untrusted plain text: React escapes it, and CSS wraps unbroken runs. */
export function LongText({ text }: { text: string }) {
  return <div className={styles.text}>{text}</div>;
}
