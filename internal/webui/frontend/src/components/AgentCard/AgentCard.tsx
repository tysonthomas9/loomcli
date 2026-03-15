/**
 * AgentCard component displays a single agent's status.
 * Compact single-row layout with circular avatar, status dot, and commit count.
 */

import type { LoomAgentStatus, ParsedLoomStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { RepoBadge } from "@/components/RepoBadge";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./AgentCard.module.css";

// Re-export for backward compatibility (consumed by IssueCard, etc.)
export { getAvatarColor } from "@/utils/colorUtils";

/**
 * Props for the AgentCard component.
 */
export interface AgentCardProps {
  /** Agent status data */
  agent: LoomAgentStatus;
  /** Optional task title to display (when working/planning) */
  taskTitle?: string | undefined;
  /** Additional CSS class name */
  className?: string;
  /** Click handler */
  onClick?: () => void;
}

/**
 * Get status dot color based on parsed status type.
 */
export function getStatusDotColor(type: ParsedLoomStatus["type"]): string {
  switch (type) {
    case "working":
    case "planning":
    case "dirty":
    case "changes":
      return "var(--color-status-working, #facc15)";
    case "error":
      return "var(--color-status-error, #ef4444)";
    case "done":
      return "var(--color-status-done, #22c55e)";
    case "review":
      return "var(--color-status-review, #3b82f6)";
    case "idle":
    case "ready":
    default:
      return "var(--color-status-idle, #9ca3af)";
  }
}

/**
 * Build the status label text for the right-hand meta column.
 */
export function getStatusLabel(parsed: ParsedLoomStatus): string {
  switch (parsed.type) {
    case "working":
      return "Working";
    case "planning":
      return "Planning";
    case "done":
      return "Done";
    case "review":
      return "Review";
    case "idle":
      return "Idle";
    case "error":
      return "Error";
    case "dirty":
      return "Uncommitted changes";
    case "changes":
      return `${parsed.changeCount ?? 0} change${parsed.changeCount === 1 ? "" : "s"}`;
    case "ready":
    default:
      return "Ready";
  }
}

/**
 * AgentCard displays a single agent's status in a compact row with circular avatar.
 */
export function AgentCard({
  agent,
  taskTitle,
  className,
  onClick,
}: AgentCardProps): JSX.Element {
  const parsed = parseLoomStatus(agent.status);
  const avatarColor = getAvatarColor(agent.name);
  const dotColor = getStatusDotColor(parsed.type);
  const statusLabel = getStatusLabel(parsed);
  const isError = parsed.type === "error";
  const initial = agent.name.charAt(0) || "?";
  const textColor = shouldUseWhiteText(avatarColor) ? "#fff" : "#1f2937";
  const roleLabel = agent.role
    ? agent.role.charAt(0).toUpperCase() + agent.role.slice(1)
    : "Agent";

  const rootClassName = [styles.card, className].filter(Boolean).join(" ");

  return (
    <div
      className={rootClassName}
      data-status={parsed.type}
      onClick={onClick}
      role={onClick ? "button" : undefined}
      tabIndex={onClick ? 0 : undefined}
      aria-label={onClick ? `Agent: ${agent.name}` : undefined}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === "Enter" || e.key === " ") {
                e.preventDefault();
                onClick();
              }
            }
          : undefined
      }
    >
      <div className={styles.avatarContainer}>
        <div
          className={styles.avatar}
          style={{ backgroundColor: avatarColor, color: textColor }}
          aria-label={`${agent.name} avatar`}
        >
          {initial}
        </div>
        <span
          className={styles.statusDot}
          style={{ backgroundColor: dotColor }}
          aria-hidden="true"
        />
      </div>

      <div className={styles.info}>
        <span className={styles.name}>{agent.name}</span>
        <span className={styles.role}>{roleLabel}</span>
        {agent.repo && (
          <span className={styles.repoLine}>
            <RepoBadge repoName={agent.repo} />
            {agent.cross_repo && (
              <span
                className={styles.crossRepoIndicator}
                aria-label="Works across multiple repositories"
                title="Cross-repo"
              >
                ↔
              </span>
            )}
          </span>
        )}
      </div>

      <div className={styles.meta}>
        {(agent.ahead > 0 || agent.behind > 0) && (
          <div
            className={styles.commitCounts}
            title={
              agent.ahead > 0 && agent.behind > 0
                ? `${agent.ahead} commits ahead and ${agent.behind} commits behind`
                : agent.ahead > 0
                  ? `${agent.ahead} commits ahead`
                  : `${agent.behind} commits behind`
            }
          >
            {agent.ahead > 0 && (
              <span className={styles.commitCount}>+{agent.ahead}</span>
            )}
            {agent.behind > 0 && (
              <span className={styles.behindCount}>-{agent.behind}</span>
            )}
          </div>
        )}
        <span
          className={styles.statusLine}
          data-error={isError || undefined}
          title={taskTitle || statusLabel}
        >
          {statusLabel}
        </span>
      </div>
    </div>
  );
}
