/**
 * CollapsedAgentRail — vertical agent avatar pills when WorkspaceTree is
 * collapsed (Aether wireframe pin 24: sidebar shrinks to agent switcher).
 * It lists the fleet agents, then the Agent API agents the expanded tree's
 * AgentList shows, by the same rules (RAIL1, SB2/SB4).
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
import { isPRReviewerAgent } from "@/utils/agentDisplay";

import { ApiAgentRailItems } from "./ApiAgentRailItems";
import styles from "./CollapsedAgentRail.module.css";

export interface CollapsedAgentRailProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  onAddClick?: (() => void) | undefined;
  /** When "prs", only PR review agents are shown and Add agent is hidden. */
  activeView?: string | undefined;
}

export function CollapsedAgentRail({
  onAgentClick,
  selectedAgentName = null,
  onAddClick,
  activeView,
}: CollapsedAgentRailProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const {
    agents: workspaceConfigAgents,
    workspace,
    workspaceId,
  } = useWorkspaceContext();
  const prsView = activeView === "prs";
  const addClick = prsView ? undefined : onAddClick;
  // The expanded tree shows the Agent API rows outside the PRs view.
  const showApi = !prsView && !!workspaceId;

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
        if (ca.role_name) entry.role = ca.role_name;
        merged.push(entry);
      }
    }
    const ordered = orderAgentsForEpicRunner(merged).filter(
      (agent) => agent.status === "configured" || isLiveAgentRailVisible(agent),
    );
    if (!prsView) return ordered;
    return ordered.filter(isPRReviewerAgent);
  }, [fleetAgents, workspaceConfigAgents, workspace?.name, prsView]);

  const emptyHint = (
    <CompactRailHost label="No agents" className={styles.emptyHint}>
      —
    </CompactRailHost>
  );

  return (
    <nav
      className={styles.rail}
      aria-label="Agents"
      data-testid="collapsed-agent-rail"
    >
      {agents.map((agent) => (
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
      ))}
      {/* The hint shows when neither the fleet nor the Agent API has one. */}
      {showApi ? (
        <ApiAgentRailItems
          workspaceId={workspaceId}
          empty={agents.length === 0 ? emptyHint : null}
        />
      ) : agents.length === 0 ? (
        emptyHint
      ) : null}
      {addClick ? (
        <CompactRailHost
          as="button"
          type="button"
          label="Add agent"
          className={styles.addButton}
          onClick={addClick}
        >
          +
        </CompactRailHost>
      ) : null}
    </nav>
  );
}
