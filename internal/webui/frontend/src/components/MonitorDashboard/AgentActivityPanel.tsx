/**
 * AgentActivityPanel displays real-time agent status in a compact panel.
 * Shows agent cards with status colors, summary counts, and sync warnings.
 */

import { useMemo } from 'react';
import { AgentCard } from '../AgentCard';
import type { LoomAgentStatus, LoomSyncInfo, LoomTaskInfo, LoomConnectionState, ParsedLoomStatus } from '@/types';
import { parseLoomStatus } from '@/types';
import styles from './AgentActivityPanel.module.css';

/**
 * Props for the AgentActivityPanel component.
 */
export interface AgentActivityPanelProps {
  /** Agent status data from useAgents */
  agents: LoomAgentStatus[];
  /** Task info keyed by agent name (for titles) */
  agentTasks: Record<string, LoomTaskInfo>;
  /** Sync status for git warnings */
  sync: LoomSyncInfo;
  /** Whether data is loading */
  isLoading: boolean;
  /** Whether connected to loom server */
  isConnected: boolean;
  /** Connection state for detailed UI feedback */
  connectionState: LoomConnectionState;
  /** Seconds until next auto-retry */
  retryCountdown: number;
  /** Last update timestamp */
  lastUpdated: Date | null;
  /** Additional CSS class name */
  className?: string;
  /** Handler when an agent card is clicked */
  onAgentClick?: (agentName: string) => void;
  /** Handler for retry button */
  onRetry?: () => void;
}

/**
 * Summary statistics computed from agent list.
 */
interface AgentSummary {
  active: number;    // working or planning
  idle: number;      // ready, idle, done
  error: number;     // error state
  needsPush: number; // agents with commits ahead
}

/**
 * Compute summary stats from agents array.
 */
function computeSummary(agents: LoomAgentStatus[]): AgentSummary {
  const summary: AgentSummary = { active: 0, idle: 0, error: 0, needsPush: 0 };

  for (const agent of agents) {
    const parsed: ParsedLoomStatus = parseLoomStatus(agent.status);

    if (parsed.type === 'working' || parsed.type === 'planning') {
      summary.active++;
    } else if (parsed.type === 'error') {
      summary.error++;
    } else {
      summary.idle++;
    }

    if (agent.ahead > 0) {
      summary.needsPush++;
    }
  }

  return summary;
}

/**
 * AgentActivityPanel shows agent status cards with summary statistics.
 */
export function AgentActivityPanel({
  agents,
  agentTasks,
  sync: _sync,
  isLoading,
  isConnected,
  connectionState,
  retryCountdown,
  lastUpdated: _lastUpdated,
  className,
  onAgentClick,
  onRetry,
}: AgentActivityPanelProps): JSX.Element {
  // Note: _sync and _lastUpdated are passed for future use (e.g., displaying sync warnings)
  const summary = useMemo(() => computeSummary(agents), [agents]);

  const rootClassName = [styles.panel, className].filter(Boolean).join(' ');

  // Scenario 1: Never connected - show "Start loom serve" message
  if (connectionState === 'never_connected' && !isLoading) {
    return (
      <div className={rootClassName} data-testid="agent-activity-panel">
        <div className={styles.empty} role="status" aria-live="polite">
          <span className={styles.emptyIcon}>🔌</span>
          <span className={styles.emptyText}>Loom server not running</span>
          <span className={styles.emptyHint}>
            Start with: <code className={styles.code}>loom serve --port 9000</code>
          </span>
          {onRetry && (
            <button className={styles.retryButton} onClick={onRetry}>
              Check Connection
            </button>
          )}
        </div>
      </div>
    );
  }

  // Loading state (no agents yet, but was connected before)
  if (isLoading && agents.length === 0) {
    return (
      <div className={rootClassName} data-testid="agent-activity-panel">
        <div className={styles.loading} role="status" aria-live="polite">Loading agents...</div>
      </div>
    );
  }

  // Scenario 3: Reconnecting - show spinner overlay with countdown
  if (connectionState === 'reconnecting' && agents.length === 0) {
    return (
      <div className={rootClassName} data-testid="agent-activity-panel">
        <div className={styles.reconnecting} role="status" aria-live="polite">
          <span className={styles.spinner} aria-hidden="true" />
          <span className={styles.emptyText}>Reconnecting to loom server...</span>
          {retryCountdown > 0 && (
            <span className={styles.countdown}>Retry in {retryCountdown}s</span>
          )}
          {onRetry && (
            <button className={styles.retryButton} onClick={onRetry}>
              Retry Now
            </button>
          )}
        </div>
      </div>
    );
  }

  // Scenario 2: Disconnected (no cache) - show empty state with retry
  if (!isConnected && agents.length === 0) {
    return (
      <div className={rootClassName} data-testid="agent-activity-panel">
        <div className={styles.empty} role="status" aria-live="polite">
          <span className={styles.emptyIcon}>⚠️</span>
          <span className={styles.emptyText}>Loom server not available</span>
          {onRetry && (
            <button className={styles.retryButton} onClick={onRetry}>
              Retry Connection
            </button>
          )}
        </div>
      </div>
    );
  }

  // No agents state (connected but no agents found)
  if (isConnected && agents.length === 0) {
    return (
      <div className={rootClassName} data-testid="agent-activity-panel">
        <div className={styles.empty} role="status" aria-live="polite">
          <span className={styles.emptyText}>No agents found</span>
        </div>
      </div>
    );
  }

  return (
    <div className={rootClassName} data-testid="agent-activity-panel">
      {/* Summary bar */}
      <div className={styles.summary} role="status" aria-label="Agent activity summary">
        <span className={styles.summaryItem} data-type="active">
          <span className={styles.summaryCount}>{summary.active}</span>
          <span className={styles.summaryLabel}>active</span>
        </span>
        <span className={styles.summarySeparator}>·</span>
        <span className={styles.summaryItem} data-type="idle">
          <span className={styles.summaryCount}>{summary.idle}</span>
          <span className={styles.summaryLabel}>idle</span>
        </span>
        {summary.error > 0 && (
          <>
            <span className={styles.summarySeparator}>·</span>
            <span className={styles.summaryItem} data-type="error">
              <span className={styles.summaryCount}>{summary.error}</span>
              <span className={styles.summaryLabel}>error</span>
            </span>
          </>
        )}
        {summary.needsPush > 0 && (
          <>
            <span className={styles.summarySeparator}>·</span>
            <span className={styles.summaryItem} data-type="sync">
              <span className={styles.summaryCount}>{summary.needsPush}</span>
              <span className={styles.summaryLabel}>need push</span>
            </span>
          </>
        )}
      </div>

      {/* Agent grid */}
      <div className={styles.agentGrid}>
        {agents.map((agent) => {
          const handleClick = onAgentClick
            ? () => onAgentClick(agent.name)
            : undefined;
          return (
            <AgentCard
              key={agent.name}
              agent={agent}
              taskTitle={agentTasks[agent.name]?.title}
              {...(handleClick && { onClick: handleClick })}
            />
          );
        })}
      </div>
    </div>
  );
}
