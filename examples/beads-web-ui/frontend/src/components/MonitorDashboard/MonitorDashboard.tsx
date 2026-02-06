/**
 * MonitorDashboard container component for multi-agent monitoring.
 *
 * Renders a single-column vertical stack containing:
 * - Project Health Panel (top)
 * - Agent Activity Panel (bottom)
 */

import type { ViewMode } from '@/components/ViewSwitcher';
import { useAgents, useBlockedIssues } from '@/hooks';
import type { Issue } from '@/types';

import { AgentActivityPanel } from './AgentActivityPanel';
import { ConnectionBanner } from './ConnectionBanner';
import styles from './MonitorDashboard.module.css';
import { ProjectHealthPanel } from './ProjectHealthPanel';

/**
 * Props for the MonitorDashboard component.
 */
export interface MonitorDashboardProps {
  /** Additional CSS class name */
  className?: string;
  /** Callback to change the active view (used for expand to graph) */
  onViewChange?: (view: ViewMode) => void;
  /** Callback when an issue is clicked (bottleneck item or graph node) */
  onIssueClick?: (issue: Issue) => void;
  /** Callback when an agent card is clicked */
  onAgentClick?: (agentName: string) => void;
}

/**
 * MonitorDashboard renders a single-column vertical stack for multi-agent monitoring.
 */
export function MonitorDashboard({
  className,
  onIssueClick,
  onAgentClick,
}: MonitorDashboardProps): JSX.Element {
  // Fetch agent status and stats
  const {
    agents,
    agentTasks,
    sync,
    stats,
    isLoading,
    isConnected,
    connectionState,
    retryCountdown,
    lastUpdated,
    retryNow,
  } = useAgents({ pollInterval: 5000 });

  // Show stale data warning when disconnected but have cached data
  const showStaleBanner = !isConnected && agents.length > 0;

  // Fetch blocked issues for bottleneck detection
  const { data: blockedIssues, loading: isLoadingBlocked } = useBlockedIssues({
    pollInterval: 30000,
  });

  // Handler for bottleneck clicks - opens issue detail panel
  const handleBottleneckClick = (issue: Pick<Issue, 'id' | 'title'>) => {
    onIssueClick?.({ ...issue } as Issue);
  };

  // Handler for agent clicks - delegates to parent
  const handleAgentClick = (agentName: string) => {
    onAgentClick?.(agentName);
  };

  const rootClassName = className ? `${styles.dashboard} ${className}` : styles.dashboard;

  return (
    <div className={rootClassName} data-testid="monitor-dashboard">
      {/* Connection banner for stale data warning */}
      {showStaleBanner && lastUpdated && (
        <ConnectionBanner
          className={styles.connectionBanner ?? ''}
          lastUpdated={lastUpdated}
          retryCountdown={retryCountdown}
          isReconnecting={connectionState === 'reconnecting'}
          onRetry={retryNow}
        />
      )}

      {/* Top: Project Health */}
      <section
        className={`${styles.panel} ${styles.projectHealth}`}
        aria-labelledby="project-health-heading"
      >
        <header className={styles.panelHeader}>
          <h2 id="project-health-heading" className={styles.panelTitle}>
            Project Health
          </h2>
          <span className={styles.refreshIndicator}>↻ 30s</span>
        </header>
        <div className={styles.panelContent}>
          <ProjectHealthPanel
            stats={stats}
            blockedIssues={blockedIssues}
            isLoading={isLoadingBlocked}
            onBottleneckClick={handleBottleneckClick}
          />
        </div>
      </section>

      {/* Middle: Agent Activity */}
      <section
        className={`${styles.panel} ${styles.agentActivity}`}
        aria-labelledby="agent-activity-heading"
      >
        <header className={styles.panelHeader}>
          <h2 id="agent-activity-heading" className={styles.panelTitle}>
            Agent Activity
          </h2>
          <span className={styles.refreshIndicator}>↻ 5s</span>
          {/* TODO: Wire up agent configuration when available */}
          <button className={styles.settingsButton} aria-label="Agent activity settings">
            ⚙️
          </button>
        </header>
        <div className={styles.panelContent}>
          <AgentActivityPanel
            agents={agents}
            agentTasks={agentTasks}
            sync={sync}
            isLoading={isLoading}
            isConnected={isConnected}
            connectionState={connectionState}
            retryCountdown={retryCountdown}
            lastUpdated={lastUpdated}
            onAgentClick={handleAgentClick}
            onRetry={retryNow}
          />
        </div>
      </section>
    </div>
  );
}
