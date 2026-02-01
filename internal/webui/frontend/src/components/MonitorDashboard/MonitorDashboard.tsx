/**
 * MonitorDashboard container component for multi-agent monitoring.
 *
 * Renders a single-column vertical stack containing:
 * - Project Health Panel (top)
 * - Agent Activity Panel (middle)
 * - Blocking Dependencies / Mini Dependency Graph (bottom)
 */

import { useAgents, useBlockedIssues, useIssues } from '@/hooks';
import type { ViewMode } from '@/components/ViewSwitcher';
import type { Issue } from '@/types';
import { AgentActivityPanel } from './AgentActivityPanel';
import { ConnectionBanner } from './ConnectionBanner';
import { ProjectHealthPanel } from './ProjectHealthPanel';
import { MiniDependencyGraph } from './MiniDependencyGraph';
import styles from './MonitorDashboard.module.css';

/**
 * Props for the MonitorDashboard component.
 */
export interface MonitorDashboardProps {
  /** Additional CSS class name */
  className?: string;
  /** Callback to change the active view (used for expand to graph) */
  onViewChange?: (view: ViewMode) => void;
}

/**
 * MonitorDashboard renders a single-column vertical stack for multi-agent monitoring.
 */
export function MonitorDashboard({ className, onViewChange }: MonitorDashboardProps): JSX.Element {
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

  // Fetch all issues with dependency data for the graph
  const { issues: graphIssues } = useIssues({ mode: 'graph' });

  // Handler for bottleneck clicks - placeholder for navigation
  const handleBottleneckClick = (issue: Pick<Issue, 'id' | 'title'>) => {
    // TODO: Integrate with IssueDetailPanel when available
    console.log('Bottleneck clicked:', issue.id);
  };

  // Handler for agent clicks - placeholder for agent details
  const handleAgentClick = (agentName: string) => {
    // TODO: Open agent detail drawer/modal when available
    console.log('Agent clicked:', agentName);
  };

  // Handler for node clicks in MiniDependencyGraph
  const handleGraphNodeClick = (issue: Issue) => {
    // TODO: Integrate with IssueDetailPanel when available
    console.log('Graph node clicked:', issue.id);
  };

  // Handler for expand button - navigate to full graph view
  const handleExpandGraph = () => {
    onViewChange?.('graph');
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

      {/* Bottom: Blocking Dependencies */}
      <section
        className={`${styles.panel} ${styles.miniGraph}`}
        aria-labelledby="mini-graph-heading"
      >
        <header className={styles.panelHeader}>
          <h2 id="mini-graph-heading" className={styles.panelTitle}>
            Blocking Dependencies
          </h2>
        </header>
        <div className={styles.panelContent}>
          <MiniDependencyGraph
            issues={graphIssues}
            onNodeClick={handleGraphNodeClick}
            onExpandClick={handleExpandGraph}
          />
        </div>
      </section>
    </div>
  );
}
