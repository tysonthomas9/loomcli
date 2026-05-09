/**
 * StartWorkButton component.
 * Shows a "Start Work" button with agent selector popover for assigning
 * an available agent to an issue and starting implementation.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import type { LoomAgentStatus, LoomTaskInfo } from "@/types";
import { parseLoomStatus } from "@/types/agent";
import type { Status } from "@/types/issue";

import styles from "./StartWorkButton.module.css";

/**
 * Props for the StartWorkButton component.
 */
export interface StartWorkButtonProps {
  /** The issue ID to assign */
  issueId: string;
  /** Current issue status */
  issueStatus: Status | undefined;
  /** Current assignee */
  currentAssignee?: string | undefined;
  /** List of agents from useAgents() */
  agents: LoomAgentStatus[];
  /** Map of agent name to current task info */
  agentTasks: Record<string, LoomTaskInfo>;
  /** Whether loom server is reachable */
  isConnected: boolean;
  /** Callback to assign agent */
  onAssign: (agentName: string) => Promise<void>;
  /** Agent role that should handle this issue stage. Defaults to implementation agents. */
  preferredRole?: "task" | "plan";
  /** External disable flag */
  disabled?: boolean;
}

/** Status types considered available for assignment */
const AVAILABLE_TYPES = new Set(["ready", "idle", "done"]);
/** Status types considered busy */
const BUSY_TYPES = new Set(["working", "planning", "review"]);

/**
 * StartWorkButton renders a button that opens a popover listing available agents.
 * Selecting an idle agent assigns the issue to that agent and asks the daemon to claim it.
 */
export function StartWorkButton({
  issueId: _issueId,
  issueStatus,
  currentAssignee,
  agents,
  agentTasks,
  isConnected,
  onAssign,
  preferredRole = "task",
  disabled,
}: StartWorkButtonProps): JSX.Element | null {
  const [isOpen, setIsOpen] = useState(false);
  const [isAssigning, setIsAssigning] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);

  // Only show for open issues without an existing agent assignee
  const isActionable = issueStatus === "open" && !currentAssignee;

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  // Handle Escape key (local handler with stopPropagation)
  const handlePopoverKeyDown = useCallback((e: React.KeyboardEvent) => {
    if (e.key === "Escape") {
      e.stopPropagation();
      setIsOpen(false);
      triggerRef.current?.focus();
    }
  }, []);

  const handleTriggerClick = useCallback(() => {
    if (disabled || isAssigning) return;
    setError(null);
    setIsOpen((prev) => !prev);
  }, [disabled, isAssigning]);

  const handleSelectAgent = useCallback(
    async (agentName: string) => {
      setIsAssigning(true);
      setIsOpen(false);
      setError(null);

      try {
        await onAssign(agentName);
      } catch (err) {
        const message =
          err instanceof Error ? err.message : "Failed to assign agent";
        setError(message);
      } finally {
        setIsAssigning(false);
      }
    },
    [onAssign],
  );

  // Categorize agents
  const availableAgents: LoomAgentStatus[] = [];
  const busyAgents: LoomAgentStatus[] = [];
  const warningAgents: LoomAgentStatus[] = [];

  for (const agent of agents) {
    if (agent.role && agent.role !== preferredRole) continue;

    const parsed = parseLoomStatus(agent.status);
    if (AVAILABLE_TYPES.has(parsed.type)) {
      availableAgents.push(agent);
    } else if (BUSY_TYPES.has(parsed.type)) {
      busyAgents.push(agent);
    } else {
      // error, dirty, changes - shown with warning
      warningAgents.push(agent);
    }
  }

  const totalAgents =
    availableAgents.length + busyAgents.length + warningAgents.length;

  if (!isActionable) return null;

  const isDisabled = disabled || isAssigning || !isConnected;

  return (
    <div ref={containerRef} className={styles.startWorkContainer}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.startWorkButton}
        onClick={handleTriggerClick}
        disabled={isDisabled}
        aria-expanded={isOpen}
        aria-haspopup="listbox"
        aria-label="Start work - assign an agent"
        data-testid="start-work-button"
      >
        <svg
          className={styles.playIcon}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <path d="M4 2.5v11l9-5.5-9-5.5z" fill="currentColor" />
        </svg>
        <span>{isAssigning ? "Assigning..." : "Start Work"}</span>
      </button>

      {isOpen && (
        <div
          className={styles.popover}
          data-testid="start-work-popover"
          onKeyDown={handlePopoverKeyDown}
        >
          {!isConnected && (
            <div
              className={styles.connectionWarning}
              data-testid="connection-warning"
            >
              <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path
                  d="M8 1L1 15h14L8 1z"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                />
                <path
                  d="M8 6v3M8 11.5v.5"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                />
              </svg>
              Loom server not connected
            </div>
          )}

          <div className={styles.popoverHeader}>
            <span className={styles.popoverTitle}>
              {availableAgents.length} available
            </span>
            {busyAgents.length > 0 && (
              <span className={styles.busyCount}>{busyAgents.length} busy</span>
            )}
          </div>

          {totalAgents === 0 && (
            <div className={styles.emptyMessage}>No agents configured</div>
          )}

          <div
            className={styles.agentList}
            role="listbox"
            aria-label="Available agents"
          >
            {availableAgents.map((agent) => (
              <div
                key={agent.name}
                className={styles.agentItem}
                onClick={() => handleSelectAgent(agent.name)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    handleSelectAgent(agent.name);
                  }
                }}
                role="option"
                aria-selected={false}
                tabIndex={0}
                data-testid={`agent-option-${agent.name}`}
              >
                <span className={styles.statusDot} data-status="available" />
                <span className={styles.agentName}>{agent.name}</span>
              </div>
            ))}

            {warningAgents.map((agent) => {
              const parsed = parseLoomStatus(agent.status);
              return (
                <div
                  key={agent.name}
                  className={styles.agentItem}
                  onClick={() => handleSelectAgent(agent.name)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      handleSelectAgent(agent.name);
                    }
                  }}
                  role="option"
                  aria-selected={false}
                  tabIndex={0}
                  data-testid={`agent-option-${agent.name}`}
                >
                  <span className={styles.statusDot} data-status="warning" />
                  <span className={styles.agentName}>{agent.name}</span>
                  <span className={styles.agentStatus}>{parsed.type}</span>
                </div>
              );
            })}

            {busyAgents.map((agent) => {
              const parsed = parseLoomStatus(agent.status);
              const task = agentTasks[agent.name];
              return (
                <div
                  key={agent.name}
                  className={styles.agentItemBusy}
                  title={
                    task
                      ? `Working on: ${task.title}`
                      : `Status: ${parsed.type}`
                  }
                  data-testid={`agent-option-${agent.name}`}
                >
                  <span className={styles.statusDot} data-status="busy" />
                  <span className={styles.agentName}>{agent.name}</span>
                  {task && <span className={styles.agentTask}>{task.id}</span>}
                </div>
              );
            })}
          </div>

          {availableAgents.length === 0 && totalAgents > 0 && (
            <div className={styles.queueInfo}>
              All {totalAgents} {totalAgents === 1 ? "agent" : "agents"} busy
            </div>
          )}
        </div>
      )}

      {isAssigning && (
        <span
          className={styles.savingIndicator}
          aria-label="Assigning..."
          data-testid="start-work-saving"
        />
      )}

      {error && (
        <span
          className={styles.error}
          role="alert"
          data-testid="start-work-error"
        >
          {error}
        </span>
      )}
    </div>
  );
}
