/**
 * AgentStatusBadge component.
 * Shows real-time agent status as a pill badge next to the assignee field.
 * Clickable to open the agent's terminal/logs tab.
 */

import { useCallback } from "react";

import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks/common";
import { useGitStatus } from "@/hooks/workspace";
import { parseLoomStatus, resolveAgentByName } from "@/types";
import { getStatusDotColor, getStatusLabel } from "@/utils/agent";

import styles from "./AgentStatusBadge.module.css";

/**
 * Props for the AgentStatusBadge component.
 */
export interface AgentStatusBadgeProps {
  /** The assigned agent's name */
  agentName: string;
  /** Callback when badge is clicked to open terminal/logs */
  onOpenTerminal?: (agentName: string) => void;
}

/** PR polling interval (30 seconds) */
const PR_POLL_INTERVAL = 30_000;

/**
 * AgentStatusBadge renders a pill-shaped badge with the agent's real-time status.
 * Status updates come from agentStore (5s polling).
 * PR link is fetched separately on a 30s interval.
 */
export function AgentStatusBadge({
  agentName,
  onOpenTerminal,
}: AgentStatusBadgeProps): JSX.Element | null {
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  // Look up agent by name (returns new ref each poll, so derive a stable boolean)
  const agent = resolveAgentByName(agents, agentName);
  const agentExists = !!agent;

  // Fetch git status for PR link detection via the shared git-status query.
  // Polling at PR_POLL_INTERVAL (coarser than the Git tab's 5s) keeps request
  // volume down, while sharing agentQueryKeys.agentGitStatus means React Query
  // dedupes this against the Git tab when both are open. The query key is scoped
  // by agentName, so switching agents resets the derived PR state automatically.
  const { status } = useGitStatus({
    agentName,
    enabled: agentExists,
    pollInterval: PR_POLL_INTERVAL,
  });
  const prBranch = status && status.ahead > 0 ? status.branch : null;

  const handleClick = useCallback(() => {
    onOpenTerminal?.(agentName);
  }, [onOpenTerminal, agentName]);

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onOpenTerminal?.(agentName);
      }
    },
    [onOpenTerminal, agentName],
  );

  // Don't render if agent not found (human assignee or not connected)
  if (!agent) return null;

  const parsed = parseLoomStatus(agent.status);
  const dotColor = getStatusDotColor(parsed.type);
  const label = getStatusLabel(parsed);

  return (
    <span
      className={styles.badge}
      data-status={parsed.type}
      data-testid="agent-status-badge"
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      role="button"
      tabIndex={0}
      aria-label={`Agent ${agentName}: ${label}${parsed.duration ? ` (${parsed.duration})` : ""}. Click to view logs.`}
      title={`${agentName}: ${label}`}
    >
      <span
        className={styles.statusDot}
        style={{ backgroundColor: dotColor }}
        aria-hidden="true"
      />
      <span className={styles.statusLabel}>{label}</span>
      {parsed.duration && (
        <span className={styles.duration}>{parsed.duration}</span>
      )}
      {prBranch && (
        <span className={styles.prIcon} aria-label="Has pushed commits">
          <svg
            width="12"
            height="12"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="M8 1v10M4 8l4 4 4-4"
              stroke="currentColor"
              strokeWidth="1.5"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </span>
      )}
    </span>
  );
}
