/**
 * AgentRow - Compact agent info row for IssueCard.
 * Shows mini avatar with status dot, agent name, and activity text.
 */

import type { ParsedLoomStatus } from '@/types';

import styles from './AgentRow.module.css';

/**
 * Props for the AgentRow component.
 */
export interface AgentRowProps {
  /** Agent display name */
  agentName: string;
  /** Parsed loom status (null if agent not found in loom) */
  status: ParsedLoomStatus | null;
  /** Avatar background color */
  avatarColor: string;
  /** Status dot color (only shown when status is available) */
  dotColor?: string | undefined;
  /** Activity text (e.g., "Working: bd-123") */
  activity?: string | undefined;
}

/**
 * AgentRow displays a compact agent info row on an IssueCard.
 */
export function AgentRow({
  agentName,
  status,
  avatarColor,
  dotColor,
  activity,
}: AgentRowProps): JSX.Element {
  // Strip [H] prefix for human assignees
  const displayName = agentName.replace(/^\[H\]\s*/, '');
  const initial = displayName.charAt(0).toUpperCase() || '?';

  return (
    <div className={styles.agentRow}>
      <div className={styles.avatarContainer}>
        <div className={styles.avatar} style={{ backgroundColor: avatarColor }}>
          {initial}
        </div>
        {status && dotColor && (
          <span
            className={styles.statusDot}
            style={{ backgroundColor: dotColor }}
            aria-hidden="true"
          />
        )}
      </div>
      <span className={styles.name}>{displayName}</span>
      {activity && (
        <span className={styles.activity} title={activity}>
          {activity}
        </span>
      )}
    </div>
  );
}
