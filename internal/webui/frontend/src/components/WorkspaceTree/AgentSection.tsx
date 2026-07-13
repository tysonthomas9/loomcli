/**
 * AgentSection displays agents in the sidebar, split into regular (interactive
 * lead) agents and background (daemon-supervised plan/task) workers.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import { useAgentStoreInstance, useWorkspaceContext } from "@/hooks";
import type { LoomAgentStatus } from "@/types";
import {
  SK_AGENT_SECTION_ORDER,
  applyAgentSectionOrder,
  mergeAgentSectionOrder,
  parseStoredAgentSectionOrder,
} from "@/utils/agentSectionOrder";
import {
  orderAgentsForEpicRunner,
  splitAgentsByRuntime,
} from "@/utils/agentRole";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import styles from "./AgentSection.module.css";
import { SortableAgentList } from "./SortableAgentList";

export interface AgentSectionProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  agentTasks?: Record<string, { title: string }> | undefined;
  onAddClick?: (() => void) | undefined;
}

export function AgentSection({
  onAgentClick,
  selectedAgentName = null,
  agentTasks,
  onAddClick,
}: AgentSectionProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const {
    agents: workspaceConfigAgents,
    workspace,
    workspaceId,
  } = useWorkspaceContext();
  const [agentOrder, setAgentOrder] = useState<string[]>([]);

  // Merge fleet agents with workspace config agents.
  // Config agents that aren't yet running appear as "configured" placeholders.
  const agents = useMemo<LoomAgentStatus[]>(() => {
    const orderedFleetAgents = orderAgentsForEpicRunner(fleetAgents);
    if (workspaceConfigAgents.length === 0) return orderedFleetAgents;

    const fleetNames = new Set(orderedFleetAgents.map((a) => a.name));
    const configPlaceholders: LoomAgentStatus[] = workspaceConfigAgents
      .filter((ca) => !fleetNames.has(ca.name))
      .map((ca) => {
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
        return entry;
      });
    return [...orderedFleetAgents, ...configPlaceholders];
  }, [fleetAgents, workspaceConfigAgents, workspace?.name]);

  const agentNames = useMemo(() => agents.map((agent) => agent.name), [agents]);
  const agentNamesKey = agentNames.join("\0");

  useEffect(() => {
    if (!workspaceId) {
      setAgentOrder(agentNames);
      return;
    }

    const stored = parseStoredAgentSectionOrder(
      wsGet(workspaceId, SK_AGENT_SECTION_ORDER),
    );
    setAgentOrder(mergeAgentSectionOrder(agentNames, stored));
  }, [workspaceId, agentNamesKey, agentNames]);

  const persistAgentOrder = useCallback(
    (nextOrder: string[]) => {
      setAgentOrder(nextOrder);
      if (workspaceId) {
        wsSet(workspaceId, SK_AGENT_SECTION_ORDER, JSON.stringify(nextOrder));
      }
    },
    [workspaceId],
  );

  const orderedAgents = useMemo(
    () => applyAgentSectionOrder(agents, agentOrder),
    [agents, agentOrder],
  );

  const { regular, background } = useMemo(
    () => splitAgentsByRuntime(orderedAgents),
    [orderedAgents],
  );
  const showBackgroundGroup = regular.length > 0 && background.length > 0;

  if (agents.length === 0 && !onAddClick) return <></>;

  return (
    <div className={`${styles.section} agentSection`}>
      <div className={`${styles.header} agentSectionHeader`}>
        <span>Agents</span>
      </div>
      <div className={styles.list}>
        <SortableAgentList
          agents={regular}
          fullOrder={agentOrder}
          onReorder={persistAgentOrder}
          onAgentClick={onAgentClick}
          selectedAgentName={selectedAgentName}
          agentTasks={agentTasks}
          listClassName={styles.sortableList}
        />
        {showBackgroundGroup ? (
          <div
            className={styles.subgroup}
            data-testid="agent-section-background"
          >
            <div className={styles.subgroupHeader}>
              <span>Background</span>
            </div>
            <SortableAgentList
              agents={background}
              fullOrder={agentOrder}
              onReorder={persistAgentOrder}
              onAgentClick={onAgentClick}
              selectedAgentName={selectedAgentName}
              agentTasks={agentTasks}
              listClassName={styles.subgroupList}
            />
          </div>
        ) : (
          <SortableAgentList
            agents={background}
            fullOrder={agentOrder}
            onReorder={persistAgentOrder}
            onAgentClick={onAgentClick}
            selectedAgentName={selectedAgentName}
            agentTasks={agentTasks}
            listClassName={styles.sortableList}
          />
        )}
      </div>
      {onAddClick && (
        <button type="button" className={styles.addButton} onClick={onAddClick}>
          + Add agent
        </button>
      )}
    </div>
  );
}
