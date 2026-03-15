/**
 * RepoBadge — small colored pill displaying a repository name.
 */

import { getAvatarColor } from "@/utils/colorUtils";

import styles from "./RepoBadge.module.css";

export interface RepoBadgeProps {
  /** Repository name to display */
  repoName: string;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Renders a colored pill badge with a repository name.
 * Background color is derived deterministically from the repo name.
 * Always uses dark text for WCAG AA compliance at small font sizes.
 */
export function RepoBadge({
  repoName,
  className,
}: RepoBadgeProps): JSX.Element | null {
  if (!repoName) return null;

  const bgColor = getAvatarColor(repoName);
  const rootClassName = [styles.repoBadge, className].filter(Boolean).join(" ");

  return (
    <span
      className={rootClassName}
      style={{ backgroundColor: bgColor, color: "var(--color-text-primary)" }}
      aria-label={`Repository: ${repoName}`}
      title={repoName}
    >
      {repoName}
    </span>
  );
}
