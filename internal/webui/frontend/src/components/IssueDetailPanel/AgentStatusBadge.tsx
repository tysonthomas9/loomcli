/**
 * AgentStatusBadge component.
 * Shows real-time agent status as a pill badge next to the assignee field.
 * Clickable to open the agent's terminal/logs tab.
 */

import { useState, useEffect, useRef, useCallback } from "react";

import { fetchGitStatus } from "@/api/git";
import { useAgentContext } from "@/hooks/useAgentContext";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import { parseLoomStatus } from "@/types";
import { getStatusDotColor, getStatusLabel } from "@/components/AgentCard";

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
 * Status updates come from useAgentContext (5s polling).
 * PR link is fetched separately on a 30s interval.
 */
export function AgentStatusBadge({
  agentName,
  onOpenTerminal,
}: AgentStatusBadgeProps): JSX.Element | null {
  const { getAgentByName } = useAgentContext();
  const { workspaceId } = useWorkspaceContext();
  const [prBranch, setPrBranch] = useState<string | null>(null);
  const mountedRef = useRef(true);

  // Look up agent by name (returns new ref each poll, so derive a stable boolean)
  const agent = getAgentByName(agentName);
  const agentExists = !!agent;

  // Reset PR state when agent changes
  useEffect(() => {
    setPrBranch(null);
  }, [agentName]);

  // Fetch git status for PR link detection
  useEffect(() => {
    mountedRef.current = true;

    if (!agentExists) return;

    const fetchPr = async () => {
      try {
        const status = await fetchGitStatus(workspaceId, agentName);
        if (mountedRef.current && status.ahead > 0) {
          setPrBranch(status.branch);
        }
      } catch {
        // PR link is best-effort
      }
    };

    fetchPr();
    const interval = setInterval(fetchPr, PR_POLL_INTERVAL);

    return () => {
      mountedRef.current = false;
      clearInterval(interval);
    };
  }, [agentName, agentExists]);

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
