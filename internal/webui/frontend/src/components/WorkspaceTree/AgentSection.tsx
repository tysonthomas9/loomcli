/**
 * AgentSection displays agents in the sidebar, split into regular (interactive
 * lead) agents and background (daemon-supervised plan/task) workers.
 */

import type React from "react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { useStore } from "zustand";

import { ConfirmDialog } from "@/components/ConfirmDialog";
import {
  useAgentServiceMutations,
  useAgentStoreInstance,
  useAgentServices,
  useDeleteWorkspaceAgent,
  useWorkspaceContext,
} from "@/hooks";
import { useToast } from "@/hooks/ui";
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
  agentServiceCadenceLabel,
  agentServiceDotColor,
  agentServiceDotState,
  agentServiceDotTooltip,
} from "@/utils/bindingDisplay";
import { wsGet, wsSet } from "@/utils/scopedStorage";

import styles from "./AgentSection.module.css";
import {
  agentServiceCardAgent,
  withoutDurableAgentProjections,
} from "./agentSectionAutomationRows";
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
  const agentServiceMutations = useAgentServiceMutations(workspaceId);
  const [agentOrder, setAgentOrder] = useState<string[]>([]);
  const [contextMenu, setContextMenu] = useState<AgentMenuState | null>(null);
  const [serviceToRemove, setServiceToRemove] = useState<string | null>(null);
  const [removingService, setRemovingService] = useState(false);
  const prsView = activeView === "prs";
  const addClick = prsView ? undefined : onAddClick;
  const { services: allAgentServices } = useAgentServices(workspaceId, {
    enabled: Boolean(workspaceId),
  });
  // The PR view's established contract is "PR-review roster agents only".
  // Durable background services belong to the normal workspace Agents view.
  const agentServices = useMemo(
    () => (prsView ? [] : allAgentServices),
    [allAgentServices, prsView],
  );
  const serviceById = useMemo(
    () => new Map(agentServices.map((record) => [record.id, record])),
    [agentServices],
  );

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
    merged = withoutDurableAgentProjections(merged, agentServices);
    if (!prsView) {
      return [
        ...merged,
        ...agentServices.map((record) =>
          agentServiceCardAgent(record, workspace?.name ?? ""),
        ),
      ];
    }
    return merged.filter(isPRReviewerAgent);
  }, [
    agentServices,
    fleetAgents,
    workspaceConfigAgents,
    workspace?.name,
    prsView,
  ]);

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
  const showBackgroundGroup =
    agentServices.length > 0 || (regular.length > 0 && background.length > 0);

  const servicePresentations = useMemo(
    () =>
      Object.fromEntries(
        agentServices.map((record) => [
          record.id,
          {
            taskTitle: agentServiceCadenceLabel(record),
            statusDotColor: agentServiceDotColor(record),
            title: agentServiceDotTooltip(record),
            testId: `autonomous-agent-${record.id}`,
            state: agentServiceDotState(record),
          },
        ]),
      ),
    [agentServices],
  );

  const handleArchive = useCallback(
    async (name: string) => {
      if (!workspaceId) return;
      setContextMenu(null);
      if (serviceById.has(name)) {
        setServiceToRemove(name);
        return;
      }
      try {
        await deleteAgent(workspaceId, name);
        showToast(`Agent ${name} archived`, { type: "success" });
        refetch();
      } catch {
        showToast("Failed to archive agent", { type: "error" });
      }
    },
    [deleteAgent, refetch, serviceById, showToast, workspaceId],
  );

  const confirmServiceRemoval = useCallback(async () => {
    if (!serviceToRemove) return;
    const record = serviceById.get(serviceToRemove);
    setRemovingService(true);
    try {
      await agentServiceMutations.remove(serviceToRemove);
      showToast(`Scheduled agent ${record?.name || serviceToRemove} removed`, {
        type: "success",
      });
      setServiceToRemove(null);
    } catch {
      showToast("Failed to remove scheduled agent", { type: "error" });
    } finally {
      setRemovingService(false);
    }
  }, [agentServiceMutations, serviceById, serviceToRemove, showToast]);

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

  if (agents.length === 0 && agentServices.length === 0 && !addClick)
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

  return (
    <div className={`${styles.section} agentSection`}>
      <div className={`${styles.header} agentSectionHeader`}>
        <span>Agents</span>
      </div>
      <div className={styles.list}>
        <SortableAgentList
          agents={regular}
          listClassName={styles.sortableList}
          {...listProps}
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
              listClassName={styles.subgroupList}
              presentations={servicePresentations}
              {...listProps}
            />
          </div>
        ) : (
          <SortableAgentList
            agents={background}
            listClassName={styles.sortableList}
            {...listProps}
          />
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
      {serviceToRemove ? (
        <ConfirmDialog
          isOpen
          title="Remove scheduled agent"
          message={
            <span>
              Remove <strong>{serviceToRemove}</strong> and its trigger binding?
              This cannot be undone.
            </span>
          }
          confirmLabel={removingService ? "Removing…" : "Remove"}
          variant="danger"
          onConfirm={() => void confirmServiceRemoval()}
          onCancel={() => setServiceToRemove(null)}
        />
      ) : null}
    </div>
  );
}
