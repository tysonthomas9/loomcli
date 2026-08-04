import styles from "./SessionBadge.module.css";

interface SessionBadgeProps {
  count: number;
}

export function SessionBadge({ count }: SessionBadgeProps) {
  if (count <= 0) return null;

  return (
    <span className={styles.badge} aria-label={`${count} active sessions`}>
      {count}
    </span>
  );
}
