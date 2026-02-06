/**
 * AgentCard component displays a single agent's status.
 * Compact single-row layout with circular avatar, status dot, and commit count.
 */

import type { LoomAgentStatus, ParsedLoomStatus } from '@/types';
import { parseLoomStatus } from '@/types';

import styles from './AgentCard.module.css';

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
 * Pastel color palette for agent avatars.
 */
const AVATAR_COLORS = [
  '#9DC08B', // sage green
  '#F59E87', // peach
  '#B6B2DF', // lavender
  '#95CBE9', // sky blue
  '#F5C28E', // apricot
  '#E8A5B3', // rose
  '#A5D4C8', // mint
  '#D4A5D8', // orchid
];

/**
 * Get a deterministic avatar background color from agent name.
 */
export function getAvatarColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length] ?? '#9DC08B';
}

/**
 * Determine if white text has sufficient contrast on the given background.
 * Uses relative luminance approximation; returns true if bg is dark enough for white text.
 */
function shouldUseWhiteText(hex: string): boolean {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  // Perceived brightness (ITU-R BT.601)
  const brightness = (r * 299 + g * 587 + b * 114) / 1000;
  return brightness < 160;
}

/**
 * Get status dot color based on parsed status type.
 */
export function getStatusDotColor(type: ParsedLoomStatus['type']): string {
  switch (type) {
    case 'working':
    case 'planning':
    case 'dirty':
    case 'changes':
      return 'var(--color-status-working, #facc15)';
    case 'error':
      return 'var(--color-status-error, #ef4444)';
    case 'done':
      return 'var(--color-status-done, #22c55e)';
    case 'review':
      return 'var(--color-status-review, #3b82f6)';
    case 'idle':
    case 'ready':
    default:
      return 'var(--color-status-idle, #9ca3af)';
  }
}

const ROLE_MAP: Record<string, string> = {
  cobalt: 'Developer',
  dev1: 'Developer',
  ember: 'QA',
  falcon: 'Developer',
  nova: 'Architecture',
  zephyr: 'Developer',
};

function getRoleLabel(name: string): string {
  const key = name.toLowerCase();
  return ROLE_MAP[key] ?? 'Developer';
}

/**
 * Build the status label text for the right-hand meta column.
 */
export function getStatusLabel(parsed: ParsedLoomStatus): string {
  switch (parsed.type) {
    case 'working':
      return 'Working';
    case 'planning':
      return 'Planning';
    case 'done':
      return 'Done';
    case 'review':
      return 'Review';
    case 'idle':
      return 'Idle';
    case 'error':
      return 'Error';
    case 'dirty':
      return 'Uncommitted changes';
    case 'changes':
      return `${parsed.changeCount ?? 0} change${parsed.changeCount === 1 ? '' : 's'}`;
    case 'ready':
    default:
      return 'Ready';
  }
}

/**
 * AgentCard displays a single agent's status in a compact row with circular avatar.
 */
export function AgentCard({ agent, taskTitle, className, onClick }: AgentCardProps): JSX.Element {
  const parsed = parseLoomStatus(agent.status);
  const avatarColor = getAvatarColor(agent.name);
  const dotColor = getStatusDotColor(parsed.type);
  const statusLabel = getStatusLabel(parsed);
  const isError = parsed.type === 'error';
  const initial = agent.name.charAt(0) || '?';
  const textColor = shouldUseWhiteText(avatarColor) ? '#fff' : '#1f2937';
  const roleLabel = getRoleLabel(agent.name);

  const rootClassName = [styles.card, className].filter(Boolean).join(' ');

  return (
    <div
      className={rootClassName}
      data-status={parsed.type}
      onClick={onClick}
      role={onClick ? 'button' : undefined}
      tabIndex={onClick ? 0 : undefined}
      aria-label={onClick ? `Agent: ${agent.name}` : undefined}
      onKeyDown={
        onClick
          ? (e) => {
              if (e.key === 'Enter' || e.key === ' ') {
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
      </div>

      <div className={styles.meta}>
        {agent.ahead > 0 && (
          <span className={styles.commitCount} title={`${agent.ahead} commits ahead`}>
            +{agent.ahead}
          </span>
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
