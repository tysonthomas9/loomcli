/**
 * CollapsedAgentRail — vertical agent avatar pills when WorkspaceTree is
 * collapsed (Aether wireframe pin 24: sidebar shrinks to agent switcher).
 */

import { useMemo } from "react";
import { useStore } from "zustand";

import {
  AgentAvatarButton,
  isLiveAgentRailVisible,
  orderAgentsForEpicRunner,
} from "@/components/AgentIconRail";
import { useAgentStoreInstance, useWorkspaceContext } from "@/hooks";
import type { LoomAgentStatus } from "@/types";

import styles from "./CollapsedAgentRail.module.css";

export interface CollapsedAgentRailProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  onAddClick?: (() => void) | undefined;
}

export function CollapsedAgentRail({
  onAgentClick,
  onAddClick,
}: CollapsedAgentRailProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const { agents: workspaceConfigAgents, workspace } = useWorkspaceContext();

  const agents = useMemo<LoomAgentStatus[]>(() => {
    const merged: LoomAgentStatus[] = [...fleetAgents];
    if (workspaceConfigAgents.length > 0) {
      const fleetNames = new Set(fleetAgents.map((a) => a.name));
      for (const ca of workspaceConfigAgents) {
        if (fleetNames.has(ca.name)) continue;
        const entry: LoomAgentStatus = {
          name: ca.name,
          branch: "",
          status: "configured",
          ahead: 0,
          behind: 0,
          workspace: workspace?.name ?? "",
          cross_repo: ca.cross_repo,
        };
        if (ca.repos?.[0]) entry.repo = ca.repos[0];
        merged.push(entry);
      }
    }
    return orderAgentsForEpicRunner(merged).filter(
      (agent) =>
        agent.status === "configured" || isLiveAgentRailVisible(agent),
    );
  }, [fleetAgents, workspaceConfigAgents, workspace?.name]);

  return (
    <nav
      className={styles.rail}
      aria-label="Agents"
      data-testid="collapsed-agent-rail"
    >
      {agents.length === 0 ? (
        <span className={styles.emptyHint} title="No agents">
          —
        </span>
      ) : (
        agents.map((agent) => (
          <AgentAvatarButton
            key={agent.name}
            agent={agent}
            selected={false}
            size={32}
            onClick={() => onAgentClick?.(agent.name)}
          />
        ))
      )}
      {onAddClick ? (
        <button
          type="button"
          className={styles.addButton}
          onClick={onAddClick}
          aria-label="Add agent"
        >
          +
          <span className={styles.tooltip} role="tooltip">
            Add agent
          </span>
        </button>
      ) : null}
    </nav>
  );
}
