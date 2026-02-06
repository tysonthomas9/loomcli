/**
 * AgentDetailPanel component.
 * Slide-out side panel that displays detailed information about a selected agent.
 * Follows the same slide-out pattern as IssueDetailPanel.
 */

import { useEffect, useRef, useCallback, useState } from 'react';

import { getAgentLogStreamUrl } from '@/api';
import { useLogStream } from '@/hooks';
import type { LoomAgentStatus, LoomTaskInfo } from '@/types';
import { parseLoomStatus } from '@/types';

import { LogViewer } from '../LogViewer';
import styles from './AgentDetailPanel.module.css';

/**
 * Props for the AgentDetailPanel component.
 */
export interface AgentDetailPanelProps {
  /** Whether the panel is open */
  isOpen: boolean;
  /** Name of the selected agent (null when closed) */
  agentName: string | null;
  /** All agents from useAgents */
  agents: LoomAgentStatus[];
  /** Agent tasks map from useAgents */
  agentTasks: Record<string, LoomTaskInfo>;
  /** Callback when panel should close */
  onClose: () => void;
  /** Callback when task link is clicked (opens IssueDetailPanel) */
  onTaskClick?: (taskId: string) => void;
}

/**
 * Pastel color palette for agent avatars (matches AgentCard).
 */
const AVATAR_COLORS = [
  '#9DC08B',
  '#F59E87',
  '#B6B2DF',
  '#95CBE9',
  '#F5C28E',
  '#E8A5B3',
  '#A5D4C8',
  '#D4A5D8',
];

function getAvatarColor(name: string): string {
  let hash = 0;
  for (let i = 0; i < name.length; i++) {
    hash = ((hash << 5) - hash + name.charCodeAt(i)) | 0;
  }
  return AVATAR_COLORS[Math.abs(hash) % AVATAR_COLORS.length] ?? '#9DC08B';
}

function shouldUseWhiteText(hex: string): boolean {
  const r = parseInt(hex.slice(1, 3), 16);
  const g = parseInt(hex.slice(3, 5), 16);
  const b = parseInt(hex.slice(5, 7), 16);
  const brightness = (r * 299 + g * 587 + b * 114) / 1000;
  return brightness < 160;
}

function getStatusDotColor(type: string): string {
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

function getStatusLabel(type: string): string {
  switch (type) {
    case 'working':
      return 'Working';
    case 'planning':
      return 'Planning';
    case 'done':
      return 'Done';
    case 'review':
      return 'Awaiting Review';
    case 'idle':
      return 'Idle';
    case 'error':
      return 'Error';
    case 'dirty':
      return 'Uncommitted Changes';
    case 'changes':
      return 'Has Changes';
    case 'ready':
      return 'Ready';
    default:
      return 'Unknown';
  }
}

function getPriorityLabel(priority: number): string {
  switch (priority) {
    case 0:
      return 'P0 Critical';
    case 1:
      return 'P1 High';
    case 2:
      return 'P2 Medium';
    case 3:
      return 'P3 Low';
    case 4:
      return 'P4 Backlog';
    default:
      return `P${priority}`;
  }
}

/**
 * AgentDetailPanel displays detailed agent information in a slide-out panel.
 */
type TabType = 'info' | 'logs';

export function AgentDetailPanel({
  isOpen,
  agentName,
  agents,
  agentTasks,
  onClose,
  onTaskClick,
}: AgentDetailPanelProps): JSX.Element {
  const panelRef = useRef<HTMLElement>(null);
  const [activeTab, setActiveTab] = useState<TabType>('info');

  // Log streaming - only connect when logs tab is active and panel is open
  const shouldConnectLogs = isOpen && activeTab === 'logs' && agentName !== null;
  const logStreamUrl = agentName ? getAgentLogStreamUrl(agentName) : '';

  const {
    lines: logLines,
    state: logConnectionState,
    lastError: logError,
    connect: connectLogs,
    disconnect: disconnectLogs,
  } = useLogStream({
    url: logStreamUrl,
    autoConnect: false,
  });

  // Connect/disconnect logs based on tab and panel state
  useEffect(() => {
    if (shouldConnectLogs) {
      connectLogs();
    } else {
      disconnectLogs();
    }
  }, [shouldConnectLogs, connectLogs, disconnectLogs]);

  // Reset to info tab when agent changes
  useEffect(() => {
    setActiveTab('info');
  }, [agentName]);

  // Handle Escape key
  useEffect(() => {
    if (!isOpen) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        onClose();
      }
    };

    document.addEventListener('keydown', handleKeyDown);
    return () => document.removeEventListener('keydown', handleKeyDown);
  }, [isOpen, onClose]);

  // Lock body scroll when open
  useEffect(() => {
    if (isOpen) {
      const previousOverflow = document.body.style.overflow;
      document.body.style.overflow = 'hidden';
      return () => {
        document.body.style.overflow = previousOverflow;
      };
    }
  }, [isOpen]);

  // Focus management
  useEffect(() => {
    if (isOpen && panelRef.current) {
      const previouslyFocused = document.activeElement as HTMLElement | null;
      panelRef.current.focus();
      return () => {
        if (previouslyFocused && document.contains(previouslyFocused) && previouslyFocused.focus) {
          previouslyFocused.focus();
        }
      };
    }
  }, [isOpen]);

  const handleTaskClick = useCallback(
    (taskId: string) => {
      onTaskClick?.(taskId);
    },
    [onTaskClick]
  );

  // Find the agent from the array
  const agent = agentName ? agents.find((a) => a.name === agentName) : undefined;
  const parsed = agent ? parseLoomStatus(agent.status) : undefined;
  const task = agentName ? agentTasks[agentName] : undefined;
  const currentTaskId = parsed?.taskId;
  const isActive = parsed?.type === 'working' || parsed?.type === 'planning';

  const rootClassName = [styles.overlay, isOpen && styles.open].filter(Boolean).join(' ');

  return (
    <div
      className={rootClassName}
      onClick={onClose}
      data-testid="agent-detail-overlay"
      aria-hidden={!isOpen}
    >
      <aside
        ref={panelRef}
        className={styles.panel}
        onClick={(e) => e.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={agent ? `Details for agent ${agent.name}` : 'Agent details'}
        tabIndex={-1}
        data-testid="agent-detail-panel"
        data-state={isOpen ? 'open' : 'closed'}
      >
        {agent && parsed ? (
          <>
            {/* Sticky Header */}
            <div className={styles.stickyHeaderWrapper}>
              <div
                className={styles.agentAvatar}
                style={{
                  backgroundColor: getAvatarColor(agent.name),
                  color: shouldUseWhiteText(getAvatarColor(agent.name)) ? '#fff' : '#1f2937',
                }}
              >
                {agent.name.charAt(0).toUpperCase()}
              </div>
              <div className={styles.headerInfo}>
                <h2 className={styles.agentName}>{agent.name}</h2>
                <div className={styles.statusRow}>
                  <span
                    className={styles.statusDot}
                    style={{ backgroundColor: getStatusDotColor(parsed.type) }}
                    data-active={isActive}
                    aria-hidden="true"
                  />
                  <span className={styles.statusLabel}>
                    {getStatusLabel(parsed.type)}
                    {parsed.duration && ` (${parsed.duration})`}
                  </span>
                </div>
              </div>
              <button
                type="button"
                className={styles.closeButton}
                onClick={onClose}
                aria-label="Close panel"
              >
                <svg width="20" height="20" viewBox="0 0 16 16" fill="none">
                  <path
                    d="M4 4l8 8M12 4l-8 8"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                  />
                </svg>
              </button>
            </div>

            {/* Metadata Bar */}
            <div className={styles.metadataBar}>
              <span className={styles.metadataItem}>
                <span className={styles.branchName}>{agent.branch}</span>
              </span>
            </div>

            {/* Tab Bar */}
            <div className={styles.tabBar}>
              <button
                type="button"
                className={`${styles.tab} ${activeTab === 'info' ? styles.activeTab : ''}`}
                onClick={() => setActiveTab('info')}
                aria-selected={activeTab === 'info'}
                role="tab"
              >
                Info
              </button>
              <button
                type="button"
                className={`${styles.tab} ${activeTab === 'logs' ? styles.activeTab : ''}`}
                onClick={() => setActiveTab('logs')}
                aria-selected={activeTab === 'logs'}
                role="tab"
              >
                Logs
              </button>
            </div>

            {/* Tab Content */}
            {activeTab === 'info' ? (
              /* Info Tab - Scrollable Content */
              <div className={styles.scrollableContent}>
                {/* Current Task Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Current Task</h3>
                  {task && currentTaskId ? (
                    <button
                      type="button"
                      className={styles.taskLink}
                      onClick={() => handleTaskClick(currentTaskId)}
                    >
                      <span className={styles.taskId}>{task.id}</span>
                      <div className={styles.taskInfo}>
                        <p className={styles.taskTitle}>{task.title}</p>
                        <div className={styles.taskMeta}>
                          <span className={styles.priorityBadge} data-priority={task.priority}>
                            {getPriorityLabel(task.priority)}
                          </span>
                        </div>
                      </div>
                    </button>
                  ) : (
                    <span className={styles.emptyState}>No active task</span>
                  )}
                </div>

                {/* Commit Status Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Commit Status</h3>
                  <div className={styles.commitRow}>
                    {agent.ahead > 0 ? (
                      <span className={styles.commitBadge} data-type="ahead">
                        +{agent.ahead} ahead
                      </span>
                    ) : null}
                    {agent.behind > 0 ? (
                      <span className={styles.commitBadge} data-type="behind">
                        -{agent.behind} behind
                      </span>
                    ) : null}
                    {agent.ahead === 0 && agent.behind === 0 && (
                      <span className={styles.commitBadge} data-type="synced">
                        In sync
                      </span>
                    )}
                  </div>
                </div>

                {/* Agent Info Section */}
                <div className={styles.section}>
                  <h3 className={styles.sectionTitle}>Agent Info</h3>
                  <dl className={styles.infoGrid}>
                    <dt>Branch</dt>
                    <dd>{agent.branch}</dd>
                    <dt>Status</dt>
                    <dd>{agent.status}</dd>
                    {parsed.taskId && (
                      <>
                        <dt>Task ID</dt>
                        <dd>{parsed.taskId}</dd>
                      </>
                    )}
                    {parsed.duration && (
                      <>
                        <dt>Duration</dt>
                        <dd>{parsed.duration}</dd>
                      </>
                    )}
                  </dl>
                </div>
              </div>
            ) : (
              /* Logs Tab */
              <div className={styles.logsContainer}>
                <LogViewer
                  lines={logLines}
                  connectionState={logConnectionState}
                  error={logError}
                  height="100%"
                />
              </div>
            )}
          </>
        ) : agentName ? (
          /* Agent not found state */
          <div className={styles.notFound}>
            <span className={styles.notFoundIcon}>?</span>
            <span>Agent disconnected or not found</span>
          </div>
        ) : null}
      </aside>
    </div>
  );
}
