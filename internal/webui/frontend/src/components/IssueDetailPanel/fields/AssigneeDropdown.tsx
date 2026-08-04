/**
 * AssigneeDropdown component.
 * Interactive dropdown for viewing/editing the assignee field with text input,
 * recent assignees suggestions, and agent selection with status indicators.
 */

import { useState, useCallback, useRef, useEffect, useMemo } from "react";

import { useRecentAssignees } from "@/hooks";
import type { LoomAgentStatus, LoomTaskInfo } from "@/types";
import { parseLoomStatus } from "@/types/agent";

import styles from "./AssigneeDropdown.module.css";

/** Status types considered available for assignment */
const AVAILABLE_TYPES = new Set(["ready", "idle", "done"]);
/** Status types considered busy */
const BUSY_TYPES = new Set(["working", "planning", "review"]);

/**
 * Props for the AssigneeDropdown component.
 */
export interface AssigneeDropdownProps {
  /** Current assignee value (may include [H] prefix) */
  assignee: string | undefined;
  /**
   * User-facing label for a system-owned assignee, such as the real agent
   * behind a driver-run claim. The persisted assignee remains the mutation
   * value; this affects only the closed trigger's display.
   */
  assigneeDisplayName?: string;
  /** Callback when assignee changes - receives new assignee, should throw on error */
  onSave: (newAssignee: string) => Promise<void>;
  /** Whether saving is in progress */
  isSaving?: boolean;
  /** Whether editing is disabled */
  disabled?: boolean;
  /** Additional CSS class name */
  className?: string;
  /** Agent list from useAgents() — when provided, shows agent section in dropdown */
  agents?: LoomAgentStatus[];
  /** Map of agent name to current task info (for busy status display) */
  agentTasks?: Record<string, LoomTaskInfo>;
}

/**
 * Strip the [H] prefix from an assignee name for display.
 */
function stripHumanPrefix(name: string): string {
  return name.replace(/^\[H\]\s*/, "");
}

/**
 * Get a short label for an agent's parsed status type.
 */
function getAgentStatusLabel(type: string): string {
  switch (type) {
    case "ready":
    case "idle":
    case "done":
      return "available";
    case "working":
      return "working";
    case "planning":
      return "planning";
    case "review":
      return "in review";
    case "error":
      return "error";
    case "dirty":
      return "dirty";
    case "changes":
      return "uncommitted";
    default:
      return type;
  }
}

/**
 * AssigneeDropdown renders an interactive dropdown for changing issue assignee.
 * Features:
 * - Agent list with availability status (when agents prop provided)
 * - Text input for free-form assignee names (human assignment with [H] prefix)
 * - Recent assignees list from localStorage
 * - Confirmation dialog when reassigning from an active agent
 * - Unassign capability
 * - Keyboard support (Escape to close, Enter to submit)
 * - Optimistic updates with error rollback
 */
export function AssigneeDropdown({
  assignee,
  assigneeDisplayName,
  onSave,
  isSaving,
  disabled,
  className,
  agents,
  agentTasks,
}: AssigneeDropdownProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [optimisticAssignee, setOptimisticAssignee] = useState<
    string | undefined
  >(assignee);
  const [inputValue, setInputValue] = useState("");
  const [confirmAgent, setConfirmAgent] = useState<string | null>(null);

  const containerRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  const { recentAssignees, addRecentAssignee } = useRecentAssignees();

  const hasAgents = agents && agents.length > 0;

  // Categorize agents
  const { availableAgents, busyAgents, warningAgents } = useMemo(() => {
    if (!agents)
      return { availableAgents: [], busyAgents: [], warningAgents: [] };

    const available: LoomAgentStatus[] = [];
    const busy: LoomAgentStatus[] = [];
    const warning: LoomAgentStatus[] = [];

    for (const agent of agents) {
      // Only consider "task" role agents (skip "plan" role)
      if (agent.role && agent.role !== "task") continue;

      const parsed = parseLoomStatus(agent.status);
      if (AVAILABLE_TYPES.has(parsed.type)) {
        available.push(agent);
      } else if (BUSY_TYPES.has(parsed.type)) {
        busy.push(agent);
      } else {
        warning.push(agent);
      }
    }

    return {
      availableAgents: available,
      busyAgents: busy,
      warningAgents: warning,
    };
  }, [agents]);

  // Filter agents and recent assignees by search input
  const searchFilter = inputValue.trim().toLowerCase();

  const filteredAvailable = searchFilter
    ? availableAgents.filter((a) => a.name.toLowerCase().includes(searchFilter))
    : availableAgents;
  const filteredBusy = searchFilter
    ? busyAgents.filter((a) => a.name.toLowerCase().includes(searchFilter))
    : busyAgents;
  const filteredWarning = searchFilter
    ? warningAgents.filter((a) => a.name.toLowerCase().includes(searchFilter))
    : warningAgents;
  const filteredRecent = searchFilter
    ? recentAssignees.filter((n) => n.toLowerCase().includes(searchFilter))
    : recentAssignees;

  const hasFilteredAgents =
    filteredAvailable.length > 0 ||
    filteredBusy.length > 0 ||
    filteredWarning.length > 0;

  // Check if current assignee is an active agent (not [H] prefixed)
  const currentIsAgent =
    assignee &&
    !assignee.startsWith("[H]") &&
    agents?.some((a) => a.name === assignee);

  // Sync optimistic value when prop changes
  useEffect(() => {
    setOptimisticAssignee(assignee);
  }, [assignee]);

  // Focus input when dropdown opens
  useEffect(() => {
    if (isOpen) {
      requestAnimationFrame(() => {
        inputRef.current?.focus();
      });
    }
  }, [isOpen]);

  // Handle click outside to close
  useEffect(() => {
    if (!isOpen) return;

    const handleClickOutside = (event: MouseEvent) => {
      if (
        containerRef.current &&
        !containerRef.current.contains(event.target as Node)
      ) {
        setIsOpen(false);
        setInputValue("");
        setConfirmAgent(null);
      }
    };

    document.addEventListener("mousedown", handleClickOutside);
    return () => document.removeEventListener("mousedown", handleClickOutside);
  }, [isOpen]);

  const handleDropdownKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        if (confirmAgent) {
          setConfirmAgent(null);
        } else {
          setIsOpen(false);
          setInputValue("");
          triggerRef.current?.focus();
        }
      }
    },
    [confirmAgent],
  );

  const handleTriggerClick = useCallback(() => {
    if (disabled || isSaving) return;
    setError(null);
    setIsOpen((prev) => !prev);
    setInputValue("");
    setConfirmAgent(null);
  }, [disabled, isSaving]);

  const handleSave = useCallback(
    async (newAssignee: string) => {
      const previousAssignee = assignee;
      setOptimisticAssignee(newAssignee || undefined);
      setIsOpen(false);
      setInputValue("");
      setConfirmAgent(null);
      setError(null);

      // Add to recent assignees (without [H] prefix)
      if (newAssignee) {
        const nameWithoutPrefix = stripHumanPrefix(newAssignee);
        addRecentAssignee(nameWithoutPrefix);
      }

      try {
        await onSave(newAssignee);
      } catch (err) {
        // Rollback on error
        setOptimisticAssignee(previousAssignee);
        const message =
          err instanceof Error ? err.message : "Failed to update assignee";
        setError(message);
      }
    },
    [assignee, onSave, addRecentAssignee],
  );

  const handleAgentClick = useCallback(
    (agentName: string) => {
      // If current assignee is an agent and we're reassigning to a different one, confirm
      if (currentIsAgent && assignee !== agentName) {
        setConfirmAgent(agentName);
        return;
      }
      handleSave(agentName);
    },
    [currentIsAgent, assignee, handleSave],
  );

  const handleConfirmReassign = useCallback(() => {
    if (confirmAgent) {
      handleSave(confirmAgent);
    }
  }, [confirmAgent, handleSave]);

  const handleInputSubmit = useCallback(() => {
    const trimmed = inputValue.trim();
    if (!trimmed) return;
    // Add [H] prefix for manually entered names
    handleSave(`[H] ${trimmed}`);
  }, [inputValue, handleSave]);

  const handleRecentClick = useCallback(
    (name: string) => {
      // Recent names are stored without prefix, add [H] for human assignment
      handleSave(`[H] ${name}`);
    },
    [handleSave],
  );

  const handleUnassign = useCallback(() => {
    handleSave("");
  }, [handleSave]);

  const handleInputKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === "Enter") {
        event.preventDefault();
        handleInputSubmit();
      }
    },
    [handleInputSubmit],
  );

  const displayName =
    optimisticAssignee === assignee && assigneeDisplayName?.trim()
      ? assigneeDisplayName.trim()
      : optimisticAssignee
        ? stripHumanPrefix(optimisticAssignee)
        : "Unassigned";
  const hasAssignee = Boolean(optimisticAssignee);
  const isDisabled = disabled || isSaving;
  const rootClassName = [styles.assigneeDropdown, className]
    .filter(Boolean)
    .join(" ");

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        onClick={handleTriggerClick}
        disabled={isDisabled}
        data-saving={isSaving || undefined}
        data-unassigned={!hasAssignee || undefined}
        aria-expanded={isOpen}
        aria-haspopup="true"
        aria-label={`Assignee: ${displayName}. Click to change.`}
        data-testid="assignee-dropdown-trigger"
      >
        <svg
          className={styles.personIcon}
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
        >
          <circle cx="8" cy="5" r="3" stroke="currentColor" strokeWidth="1.5" />
          <path
            d="M2 14c0-2.5 2.5-4 6-4s6 1.5 6 4"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinecap="round"
          />
        </svg>
        <span className={styles.triggerText}>{displayName}</span>
        <span className={styles.dropdownArrow} aria-hidden="true">
          ▾
        </span>
      </button>

      {isOpen && (
        <div
          className={styles.menu}
          data-testid="assignee-dropdown-menu"
          onKeyDown={handleDropdownKeyDown}
        >
          {/* Confirmation dialog for agent reassignment */}
          {confirmAgent && (
            <div
              className={styles.confirmOverlay}
              data-testid="reassign-confirm"
            >
              <p className={styles.confirmText}>
                <strong>{stripHumanPrefix(assignee ?? "")}</strong> is currently
                working on this issue. Reassigning will not stop its current
                session.
              </p>
              <div className={styles.confirmActions}>
                <button
                  type="button"
                  className={styles.confirmCancel}
                  onClick={() => setConfirmAgent(null)}
                >
                  Cancel
                </button>
                <button
                  type="button"
                  className={styles.confirmProceed}
                  onClick={handleConfirmReassign}
                  data-testid="reassign-confirm-proceed"
                >
                  Reassign
                </button>
              </div>
            </div>
          )}

          {!confirmAgent && (
            <>
              {/* Search/input row */}
              <div className={styles.inputRow}>
                <input
                  ref={inputRef}
                  type="text"
                  className={styles.input}
                  value={inputValue}
                  onChange={(e) => setInputValue(e.target.value)}
                  onKeyDown={handleInputKeyDown}
                  placeholder={
                    hasAgents ? "Search or type a name..." : "Type a name..."
                  }
                  data-testid="assignee-input"
                />
                <button
                  type="button"
                  className={styles.submitButton}
                  onClick={handleInputSubmit}
                  disabled={!inputValue.trim()}
                  data-testid="assignee-submit"
                >
                  Assign
                </button>
              </div>

              {/* Agents section */}
              {hasAgents && hasFilteredAgents && (
                <div className={styles.agentSection}>
                  <span className={styles.sectionHeader}>Agents</span>
                  {filteredAvailable.map((agent) => (
                    <button
                      key={agent.name}
                      type="button"
                      className={styles.agentItem}
                      onClick={() => handleAgentClick(agent.name)}
                      data-testid={`agent-assignee-${agent.name}`}
                    >
                      <span
                        className={styles.agentStatusDot}
                        data-status="available"
                      />
                      <span className={styles.agentName}>{agent.name}</span>
                      <span className={styles.agentStatusLabel}>available</span>
                    </button>
                  ))}
                  {filteredWarning.map((agent) => {
                    const parsed = parseLoomStatus(agent.status);
                    return (
                      <button
                        key={agent.name}
                        type="button"
                        className={styles.agentItem}
                        onClick={() => handleAgentClick(agent.name)}
                        data-testid={`agent-assignee-${agent.name}`}
                      >
                        <span
                          className={styles.agentStatusDot}
                          data-status="warning"
                        />
                        <span className={styles.agentName}>{agent.name}</span>
                        <span className={styles.agentStatusLabel}>
                          {getAgentStatusLabel(parsed.type)}
                        </span>
                      </button>
                    );
                  })}
                  {filteredBusy.map((agent) => {
                    const parsed = parseLoomStatus(agent.status);
                    const task = agentTasks?.[agent.name];
                    return (
                      <div
                        key={agent.name}
                        className={styles.agentItemBusy}
                        title={
                          task
                            ? `Working on: ${task.title}`
                            : `Status: ${parsed.type}`
                        }
                        data-testid={`agent-assignee-${agent.name}`}
                      >
                        <span
                          className={styles.agentStatusDot}
                          data-status="busy"
                        />
                        <span className={styles.agentName}>{agent.name}</span>
                        <span className={styles.agentStatusLabel}>
                          {getAgentStatusLabel(parsed.type)}
                        </span>
                      </div>
                    );
                  })}
                </div>
              )}

              {/* No agents message */}
              {hasAgents && !hasFilteredAgents && searchFilter && (
                <div className={styles.agentSection}>
                  <span className={styles.sectionHeader}>Agents</span>
                  <span className={styles.noResults}>No matching agents</span>
                </div>
              )}

              {/* People section (recent assignees) */}
              {filteredRecent.length > 0 && (
                <div className={styles.recentSection}>
                  <span className={styles.sectionHeader}>People</span>
                  {filteredRecent.map((name) => (
                    <button
                      key={name}
                      type="button"
                      className={styles.recentItem}
                      onClick={() => handleRecentClick(name)}
                      data-testid={`recent-assignee-${name}`}
                    >
                      {name}
                    </button>
                  ))}
                </div>
              )}

              {hasAssignee && (
                <button
                  type="button"
                  className={styles.unassignButton}
                  onClick={handleUnassign}
                  data-testid="assignee-unassign"
                >
                  Unassign
                </button>
              )}
            </>
          )}
        </div>
      )}

      {isSaving && (
        <span
          className={styles.savingIndicator}
          aria-label="Saving..."
          data-testid="assignee-saving"
        />
      )}

      {error && (
        <span
          className={styles.error}
          role="alert"
          data-testid="assignee-error"
        >
          {error}
        </span>
      )}
    </div>
  );
}
