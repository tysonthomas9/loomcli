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
import { CompactRailHost } from "@/components/CompactRail";
import { useAgentStoreInstance, useWorkspaceContext } from "@/hooks";
import type { LoomAgentStatus } from "@/types";

import styles from "./CollapsedAgentRail.module.css";

export interface CollapsedAgentRailProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  onAddClick?: (() => void) | undefined;
}

export function CollapsedAgentRail({
  onAgentClick,
  selectedAgentName = null,
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
      (agent) => agent.status === "configured" || isLiveAgentRailVisible(agent),
    );
  }, [fleetAgents, workspaceConfigAgents, workspace?.name]);

  return (
    <nav
      className={styles.rail}
      aria-label="Agents"
      data-testid="collapsed-agent-rail"
    >
      {agents.length === 0 ? (
        <CompactRailHost label="No agents" className={styles.emptyHint}>
          —
        </CompactRailHost>
      ) : (
        agents.map((agent) => (
          <AgentAvatarButton
            key={agent.name}
            agent={agent}
            selected={
              selectedAgentName != null &&
              agent.name.toLowerCase() === selectedAgentName.toLowerCase()
            }
            size={32}
            onClick={() => onAgentClick?.(agent.name)}
          />
        ))
      )}
      {onAddClick ? (
        <CompactRailHost
          as="button"
          type="button"
          label="Add agent"
          className={styles.addButton}
          onClick={onAddClick}
        >
          +
        </CompactRailHost>
      ) : null}
    </nav>
  );
}
