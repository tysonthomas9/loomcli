/**
 * RepoGroupList renders collapsible repo groups with nested AgentCards.
 * Extracted from WorkspaceTree to keep file sizes manageable.
 */

import type { ConnectionState } from "@/api/common";
import type { LoomAgentStatus } from "@/types";
import { AgentCard } from "@/components/AgentCard";
import type { WorkspaceHealthSummary } from "@/utils/workspace";

import { ConnectionIndicator } from "./ConnectionIndicator";
import styles from "../WorkspaceTree.module.css";

interface RepoInfo {
  name: string;
  path: string;
}

export interface RepoGroupListProps {
  repos: RepoInfo[];
  repoAgents: Map<string, LoomAgentStatus[]>;
  unassignedAgents: LoomAgentStatus[];
  activeRepoName?: string | null | undefined;
  repoCollapseState: Record<string, boolean>;
  onWorkspaceSelect?: ((repoName: string | null) => void) | undefined;
  onAgentClick?: ((agentName: string) => void) | undefined;
  onRepoToggle: (repoName: string) => void;
  agentTasks?: Record<string, { title: string }> | undefined;
  /** SSE connection state */
  connectionState?: ConnectionState | undefined;
  /** Timestamp when disconnect began */
  disconnectedSince?: number | null | undefined;
  /** Health summary per repo for status dot coloring */
  repoHealthMap?: Map<string, WorkspaceHealthSummary>;
}

export function RepoGroupList({
  repos,
  repoAgents,
  unassignedAgents,
  activeRepoName,
  repoCollapseState,
  onWorkspaceSelect,
  onAgentClick,
  onRepoToggle,
  agentTasks,
  connectionState,
  disconnectedSince,
  repoHealthMap,
}: RepoGroupListProps): JSX.Element {
  const isDisconnected =
    connectionState !== undefined &&
    connectionState !== "connected" &&
    connectionState !== "connecting" &&
    disconnectedSince != null;
  return (
    <>
      {repos.map((repo) => {
        const repoAgentList = repoAgents.get(repo.name) ?? [];
        const agentCount = repoAgentList.length;
        const hasAgents = agentCount > 0;
        const isSelected = activeRepoName === repo.name;
        const isRepoCollapsed = !!repoCollapseState[repo.name];
        const health = repoHealthMap?.get(repo.name);
        const healthColor = health?.healthColor ?? "green";
        const activeAgentCount = health?.activeCount ?? 0;
        const errorAgentCount = health?.errorCount ?? 0;

        // Build tooltip: include health stats when agents exist
        const tooltipParts = [repo.path];
        if (hasAgents) {
          tooltipParts.push(
            `Agents: ${agentCount} | Active: ${activeAgentCount} | Errors: ${errorAgentCount}`,
          );
        }
        const tooltip = tooltipParts.join("\n");

        // Agent count label: "active/total" when active > 0, else just total
        const agentCountLabel =
          activeAgentCount > 0
            ? `${activeAgentCount}/${agentCount}`
            : `${agentCount}`;

        return (
          <div key={repo.name} className={styles.repoGroup}>
            <button
              type="button"
              className={styles.repoGroupHeader}
              onClick={() => onWorkspaceSelect?.(repo.name)}
              title={tooltip}
              role="radio"
              aria-checked={isSelected}
            >
              <span
                className={styles.radioIndicator}
                data-active={isSelected}
              />
              <span className={styles.repoIcon}>
                <svg
                  width="16"
                  height="16"
                  viewBox="0 0 16 16"
                  fill="none"
                  xmlns="http://www.w3.org/2000/svg"
                >
                  <path
                    d="M1.5 2.5C1.5 1.95 1.95 1.5 2.5 1.5H6.29L8.29 3.5H13.5C14.05 3.5 14.5 3.95 14.5 4.5V12.5C14.5 13.05 14.05 13.5 13.5 13.5H2.5C1.95 13.5 1.5 13.05 1.5 12.5V2.5Z"
                    fill="currentColor"
                  />
                </svg>
              </span>
              <span className={styles.repoName}>{repo.name}</span>
              <span className={styles.repoMeta}>
                {hasAgents &&
                  (connectionState === undefined ||
                    connectionState === "connected") && (
                    <span
                      className={styles.agentCount}
                      data-has-agents="true"
                      data-health={healthColor}
                    >
                      {agentCountLabel}
                    </span>
                  )}
                {isDisconnected ? (
                  <ConnectionIndicator
                    state={connectionState!}
                    disconnectedSince={disconnectedSince!}
                  />
                ) : (
                  <span
                    className={styles.statusDot}
                    data-health={healthColor}
                    data-has-agents={hasAgents}
                  />
                )}
              </span>
              <span
                className={styles.collapseChevron}
                data-expanded={!isRepoCollapsed}
                role="button"
                tabIndex={-1}
                aria-label={
                  isRepoCollapsed ? "Expand agents" : "Collapse agents"
                }
                onClick={(e) => {
                  e.stopPropagation();
                  onRepoToggle(repo.name);
                }}
              >
                &rsaquo;
              </span>
            </button>
            {!isRepoCollapsed && repoAgentList.length > 0 && (
              <div className={styles.repoGroupAgents}>
                {repoAgentList.map((agent) => (
                  <AgentCard
                    key={agent.name}
                    agent={agent}
                    taskTitle={agentTasks?.[agent.name]?.title}
                    {...(onAgentClick
                      ? {
                          onClick: () => onAgentClick(agent.name),
                        }
                      : {})}
                  />
                ))}
              </div>
            )}
          </div>
        );
      })}

      {unassignedAgents.length > 0 && (
        <div className={styles.repoGroup}>
          <div className={styles.unassignedHeader}>
            <span className={styles.repoName}>Unassigned</span>
            <span className={styles.repoMeta}>
              <span
                className={styles.agentCount}
                data-active={unassignedAgents.length > 0}
              >
                {unassignedAgents.length}
              </span>
            </span>
            <span
              className={styles.collapseChevron}
              data-expanded={!repoCollapseState["__unassigned"]}
              role="button"
              tabIndex={-1}
              aria-label={
                repoCollapseState["__unassigned"]
                  ? "Expand agents"
                  : "Collapse agents"
              }
              onClick={() => onRepoToggle("__unassigned")}
            >
              &rsaquo;
            </span>
          </div>
          {!repoCollapseState["__unassigned"] && (
            <div className={styles.repoGroupAgents}>
              {unassignedAgents.map((agent) => (
                <AgentCard
                  key={agent.name}
                  agent={agent}
                  taskTitle={agentTasks?.[agent.name]?.title}
                  {...(onAgentClick
                    ? {
                        onClick: () => onAgentClick(agent.name),
                      }
                    : {})}
                />
              ))}
            </div>
          )}
        </div>
      )}
    </>
  );
}
