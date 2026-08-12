/**
 * AgentSection displays every agent in the sidebar grouped by interaction mode
 * (Decision 5): Interactive (lead-style agents you talk to) on top, Autonomous
 * (background workers + all trigger bindings — scheduled and event-driven)
 * below. Role-agent rows stay drag-sortable; binding rows are clickable links
 * to the same detail route and are non-sortable for now.
 */

import type React from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import {
  useAgentStoreInstance,
  useDeleteWorkspaceAgent,
  useWorkspaceContext,
} from "@/hooks";
import { useToast } from "@/hooks/ui";
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
import { isPRReviewerAgent } from "@/utils/agentDisplay";
import {
  bindingCadenceLabel,
  bindingDisplayName,
  bindingDotState,
  bindingDotTooltip,
} from "@/utils/bindingDisplay";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import styles from "./AgentSection.module.css";
import { AgentContextMenu } from "./menus/AgentContextMenu";
import { SortableAgentList } from "./SortableAgentList";

export interface AgentSectionProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  agentTasks?: Record<string, { title: string }> | undefined;
  onAddClick?: (() => void) | undefined;
  /** When "prs", only PR review agents are shown and Add agent is hidden. */
  activeView?: string | undefined;
}

interface AgentMenuState {
  name: string;
  x: number;
  y: number;
}

export function AgentSection({
  onAgentClick,
  selectedAgentName = null,
  agentTasks,
  onAddClick,
  activeView,
}: AgentSectionProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const {
    agents: workspaceConfigAgents,
    workspace,
    workspaceId,
    refetch,
  } = useWorkspaceContext();
  const { showToast } = useToast();
  const deleteAgent = useDeleteWorkspaceAgent();
  const [agentOrder, setAgentOrder] = useState<string[]>([]);
  const [contextMenu, setContextMenu] = useState<AgentMenuState | null>(null);
  const prsView = activeView === "prs";
  const addClick = prsView ? undefined : onAddClick;

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
    let merged: LoomAgentStatus[];
    if (workspaceConfigAgents.length === 0) {
      merged = orderedFleetAgents;
    } else {
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
      merged = [...orderedFleetAgents, ...configPlaceholders];
    }
    if (!prsView) return merged;
    return merged.filter(isPRReviewerAgent);
  }, [fleetAgents, workspaceConfigAgents, workspace?.name, prsView]);

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

  const handleArchive = useCallback(
    async (name: string) => {
      if (!workspaceId) return;
      setContextMenu(null);
      try {
        await deleteAgent(workspaceId, name);
        showToast(`Agent ${name} archived`, { type: "success" });
        refetch();
      } catch {
        showToast("Failed to archive agent", { type: "error" });
      }
    },
    [deleteAgent, refetch, showToast, workspaceId],
  );

  const handleAgentContextMenu = useCallback(
    (event: React.MouseEvent, name: string) => {
      if (!workspaceId) return;
      setContextMenu({ name, x: event.clientX, y: event.clientY });
    },
    [workspaceId],
  );

  const closeContextMenu = useCallback(() => {
    setContextMenu(null);
  }, []);

  if (agents.length === 0 && workflowAgents.length === 0 && !addClick)
    return <></>;

  const listProps = {
    fullOrder: agentOrder,
    onReorder: persistAgentOrder,
    onAgentClick,
    selectedAgentName,
    agentTasks,
    onArchive: workspaceId ? handleArchive : undefined,
    onAgentContextMenu: workspaceId ? handleAgentContextMenu : undefined,
  };

  const interactiveList = (
    <SortableAgentList
      agents={interactive}
      listClassName={styles.sortableList}
      {...listProps}
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
                listClassName={styles.sortableList}
                {...listProps}
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
                    <span className={styles.workflowName}>
                      {bindingDisplayName(b)}
                    </span>
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
      {addClick && (
        <button type="button" className={styles.addButton} onClick={addClick}>
          + Add agent
        </button>
      )}
      <AgentContextMenu
        isOpen={contextMenu != null}
        position={{
          x: contextMenu?.x ?? 0,
          y: contextMenu?.y ?? 0,
        }}
        onArchive={() => {
          if (contextMenu) void handleArchive(contextMenu.name);
        }}
        onClose={closeContextMenu}
      />
    </div>
  );
}
