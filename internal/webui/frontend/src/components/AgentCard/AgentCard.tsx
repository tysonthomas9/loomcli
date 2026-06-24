/**
 * AgentCard component displays a single agent's status.
 * Compact single-row layout with circular avatar, status dot, and line diff stats.
 */

import type { LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, parseLoomStatus } from "@/types";
import { RepoBadge } from "@/components/RepoBadge";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { getStatusDotColor, getStatusLabel } from "@/utils/agent";

import styles from "./AgentCard.module.css";

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
  /** Whether to show the repo badge (default: true). Set false when already inside a repo group. */
  showRepoBadge?: boolean;
  /** Smaller typography and avatar for sidebar lists. */
  compact?: boolean;
}

/**
 * AgentCard displays a single agent's status in a compact row with circular avatar.
 */
export function AgentCard({
  agent,
  taskTitle,
  className,
  onClick,
  showRepoBadge = true,
  compact = false,
}: AgentCardProps): JSX.Element {
  const parsed = parseLoomStatus(effectiveAgentStatus(agent));
  const avatarColor = getAvatarColor(agent.name);
  const dotColor = getStatusDotColor(parsed.type);
  const statusLabel = getStatusLabel(parsed);
  // When the badge reflects fleet-db's live_status (serve-only deployments,
  // where no task title is loaded), surface the active task/phase on hover.
  const liveDetail =
    agent.live_status === "working"
      ? [agent.active_task_id, agent.active_phase].filter(Boolean).join(" · ")
      : "";
  const isError = parsed.type === "error";
  const initial = agent.name.charAt(0) || "?";
  const textColor = shouldUseWhiteText(avatarColor) ? "#fff" : "#1f2937";
  const roleLabel = agent.role
    ? agent.role.charAt(0).toUpperCase() + agent.role.slice(1)
    : "Agent";

  const rootClassName = [styles.card, compact && styles.compact, className]
    .filter(Boolean)
    .join(" ");

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
        {agent.repo && showRepoBadge && (
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
        <span
          className={styles.statusLine}
          data-error={isError || undefined}
          title={taskTitle || liveDetail || statusLabel}
        >
          {statusLabel}
        </span>
      </div>
    </div>
  );
}
