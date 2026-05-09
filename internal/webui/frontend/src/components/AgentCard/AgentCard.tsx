/**
 * AgentCard component displays a single agent's status.
 * Compact single-row layout with circular avatar, status dot, and line diff stats.
 */

import type { LoomAgentStatus, ParsedLoomStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { RepoBadge } from "@/components/RepoBadge";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { useAgentDiffStat } from "@/hooks";

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
 * Format a heartbeat timestamp as a short "Xs ago" / "Xm ago" string. Returns
 * undefined when the timestamp is empty, invalid, or zero. The supervisor
 * heartbeats every ttl/4 (default 30 s); a fresh value is < 30 s old, while a
 * stale value > 30 s indicates the heartbeat goroutine is no longer ticking.
 */
export function formatHeartbeatAge(
  timestamp: string | undefined,
  now: Date = new Date(),
): string | undefined {
  if (!timestamp) return undefined;
  // Backend writes Go's zero time as "0001-01-01T00:00:00Z" because
  // json.Marshal does not omit zero time.Time even with `omitempty`. Treat
  // any pre-1970 timestamp as "no heartbeat yet" rather than displaying a
  // misleading "57 years ago".
  if (timestamp.startsWith("0001-")) return undefined;
  const ts = new Date(timestamp);
  if (Number.isNaN(ts.getTime()) || ts.getFullYear() < 1970) return undefined;
  const ageMs = now.getTime() - ts.getTime();
  if (ageMs < 0) return "0s ago";
  const seconds = Math.floor(ageMs / 1000);
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  return `${hours}h ago`;
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
  showRepoBadge = true,
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
  const heartbeatAge = formatHeartbeatAge(agent.agent_lease_last_heartbeat);
  // Heartbeats tick every 30s; > 60s is stale and warrants a visible warning.
  // Only meaningful when heartbeatAge resolved to a real value (filters out
  // Go's zero-time "0001-..." and missing values).
  const heartbeatStale =
    heartbeatAge !== undefined &&
    Date.now() - new Date(agent.agent_lease_last_heartbeat ?? 0).getTime() >
      60_000;
  const { data: diffStat } = useAgentDiffStat({
    agentName: agent.name,
    pollInterval: 60000,
  });

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
        {diffStat && (diffStat.added > 0 || diffStat.removed > 0) && (
          <div
            className={styles.diffStats}
            title={`${diffStat.added} lines added, ${diffStat.removed} lines removed`}
          >
            {diffStat.added > 0 && (
              <span className={styles.linesAdded}>+{diffStat.added}</span>
            )}
            {diffStat.removed > 0 && (
              <span className={styles.linesRemoved}>-{diffStat.removed}</span>
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
        {heartbeatAge && (
          <span
            className={styles.heartbeat}
            data-stale={heartbeatStale || undefined}
            title={`Supervisor agent-lease last heartbeat: ${agent.agent_lease_last_heartbeat}${heartbeatStale ? " (stale — heartbeat goroutine may have stopped)" : ""}`}
            aria-label={`Heartbeat ${heartbeatAge}`}
          >
            ♥ {heartbeatAge}
          </span>
        )}
      </div>
    </div>
  );
}
