/**
 * AgentSection displays every agent in the sidebar grouped by interaction mode
 * (Decision 5): Interactive (lead-style agents you talk to) on top, Autonomous
 * (background workers + durable agent records + legacy trigger bindings)
 * below. Role-agent rows stay drag-sortable; record/binding rows are clickable
 * links to the same detail route and are non-sortable for now.
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
import { splitAgentsByRuntime } from "@/utils/agentRole";
import { mergeAgentRoster } from "@/utils/agentRoster";
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
import {
  buildAgentAutomationRows,
  durableRecordCadence,
  durableRecordDotState,
  durableRecordTooltip,
  selectedDurableRecordID,
} from "./agentSectionAutomationRows";

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

  // Durable records own attached trigger bindings and therefore own the rail
  // identity. Render each record once, including records with zero bindings;
  // only unattached compatibility bindings remain standalone rail entries.
  const { agentRecords, bindings } = useAutomations(workspaceId, !!workspaceId);
  const { durableRecords, legacyBindings } = useMemo(
    () => buildAgentAutomationRows(agentRecords, bindings),
    [agentRecords, bindings],
  );
  const selectedRecordID = useMemo(
    () => selectedDurableRecordID(selectedAgentName, bindings),
    [bindings, selectedAgentName],
  );

  const agents = useMemo<LoomAgentStatus[]>(() => {
    const merged = mergeAgentRoster(
      fleetAgents,
      workspaceConfigAgents,
      workspace?.name ?? "",
    );
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
  const hasAutonomous =
    background.length > 0 ||
    durableRecords.length > 0 ||
    legacyBindings.length > 0;
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

  if (
    agents.length === 0 &&
    durableRecords.length === 0 &&
    legacyBindings.length === 0 &&
    !addClick
  )
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
            {durableRecords.map((row) => {
              const selected = selectedRecordID === row.id;
              return (
                <button
                  type="button"
                  key={row.id}
                  className={styles.workflowRow}
                  data-testid={`autonomous-agent-${row.id}`}
                  data-selected={selected || undefined}
                  onClick={() => onAgentClick?.(row.id)}
                  title={durableRecordTooltip(row)}
                >
                  <span
                    className={styles.workflowDot}
                    data-state={durableRecordDotState(row)}
                    aria-hidden="true"
                  />
                  <span className={styles.workflowText}>
                    <span className={styles.workflowName}>
                      {row.record.name.trim() || row.record.id}
                    </span>
                    <span className={styles.workflowMeta}>
                      {durableRecordCadence(row)}
                    </span>
                  </span>
                </button>
              );
            })}
            {legacyBindings.map((row) => {
              const selected = selectedAgentName === row.id;
              return (
                <button
                  type="button"
                  key={row.id}
                  className={styles.workflowRow}
                  data-testid={`autonomous-binding-${row.id}`}
                  data-selected={selected || undefined}
                  onClick={() => onAgentClick?.(row.id)}
                  title={bindingDotTooltip(row.binding)}
                >
                  <span
                    className={styles.workflowDot}
                    data-state={bindingDotState(row.binding)}
                    aria-hidden="true"
                  />
                  <span className={styles.workflowText}>
                    <span className={styles.workflowName}>
                      {bindingDisplayName(row.binding)}
                    </span>
                    <span className={styles.workflowMeta}>
                      {bindingCadenceLabel(row.binding)}
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
