/**
 * AgentSection displays every agent in the sidebar grouped by interaction mode
 * (Decision 5): Interactive (lead-style agents you talk to) on top, Autonomous
 * (background workers + all trigger bindings — scheduled and event-driven)
 * below. Role-agent rows stay drag-sortable; binding rows are clickable links
 * to the same detail route and are non-sortable for now.
 */

import { useCallback, useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import { useAgentStoreInstance, useWorkspaceContext } from "@/hooks";
import { useAutomations } from "@/hooks/workspace";
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
import {
  bindingCadenceLabel,
  bindingDotState,
  bindingDotTooltip,
} from "@/utils/bindingDisplay";
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

  // Trigger-binding "agents" (scheduled and event-driven) never enter the agent
  // store — they are workflow-plane agents. Surface ALL of them here (every
  // source_kind) so activating any workflow template shows up in the sidebar as
  // a first-class, clickable agent.
  const { bindings } = useAutomations(workspaceId, !!workspaceId);
  const workflowAgents = bindings;

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

  const { regular: interactive, background } = useMemo(
    () => splitAgentsByRuntime(orderedAgents),
    [orderedAgents],
  );

  const hasInteractive = interactive.length > 0;
  const hasAutonomous = background.length > 0 || workflowAgents.length > 0;
  // Label the primary Interactive list only when an Autonomous group also
  // exists (a lone interactive list under "Agents" needs no sublabel). Always
  // label the Autonomous group — it mixes background workers + bindings and
  // reads clearer named, even when it is the only group.
  const showInteractiveHeader = hasInteractive && hasAutonomous;
  const showAutonomousHeader = hasAutonomous;

  if (agents.length === 0 && workflowAgents.length === 0 && !onAddClick)
    return <></>;

  const interactiveList = (
    <SortableAgentList
      agents={interactive}
      fullOrder={agentOrder}
      onReorder={persistAgentOrder}
      onAgentClick={onAgentClick}
      selectedAgentName={selectedAgentName}
      agentTasks={agentTasks}
      listClassName={styles.sortableList}
    />
  );

  return (
    <div className={`${styles.section} agentSection`}>
      <div className={`${styles.header} agentSectionHeader`}>
        <span>Agents</span>
      </div>
      <div className={styles.list}>
        {hasInteractive &&
          (showInteractiveHeader ? (
            <div data-testid="agent-section-interactive">
              <div className={styles.groupHeader}>
                <span>Interactive</span>
              </div>
              {interactiveList}
            </div>
          ) : (
            interactiveList
          ))}

        {hasAutonomous && (
          <div data-testid="agent-section-autonomous">
            {showAutonomousHeader && (
              <div className={styles.groupHeader}>
                <span>Autonomous</span>
              </div>
            )}
            {background.length > 0 && (
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
            {workflowAgents.map((b) => {
              const selected =
                selectedAgentName != null &&
                b.binding_id.toLowerCase() === selectedAgentName.toLowerCase();
              return (
                <button
                  type="button"
                  key={b.binding_id}
                  className={styles.workflowRow}
                  data-testid={`autonomous-binding-${b.binding_id}`}
                  data-selected={selected || undefined}
                  onClick={() => onAgentClick?.(b.binding_id)}
                  title={bindingDotTooltip(b)}
                >
                  <span
                    className={styles.workflowDot}
                    data-state={bindingDotState(b)}
                    aria-hidden="true"
                  />
                  <span className={styles.workflowText}>
                    <span className={styles.workflowName}>{b.binding_id}</span>
                    <span className={styles.workflowMeta}>
                      {bindingCadenceLabel(b)}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
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
