import type React from "react";
import { useCallback, type ReactNode } from "react";

import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  arrayMove,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";

import type { LoomAgentStatus } from "@/types";
import { reorderAgentGroup } from "@/utils/agentSectionOrder";

import { SortableAgentRow } from "./SortableAgentRow";
import styles from "./AgentSection.module.css";

interface ListCommon {
  fullOrder: string[];
  onReorder: (nextOrder: string[]) => void;
  listClassName?: string | undefined;
  onArchive?: ((agentName: string) => void) | undefined;
  onAgentContextMenu?:
    | ((event: React.MouseEvent, agentName: string) => void)
    | undefined;
}

/** Fleet agents, drawn as AgentCards and keyed by name. */
interface FleetList {
  agents: LoomAgentStatus[];
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  agentTasks?: Record<string, { title: string }> | undefined;
}

/**
 * A row with its own content (Agent API agents). `below` (its pinned
 * children) shows under the row and drags with it as one unit.
 */
export interface SortableAgentItem {
  id: string;
  label: string;
  content: ReactNode;
  below?: ReactNode;
}

interface ItemList {
  items: SortableAgentItem[];
}

export type SortableAgentListProps = ListCommon & (FleetList | ItemList);

export function SortableAgentList(
  props: SortableAgentListProps,
): JSX.Element | null {
  const { fullOrder, onReorder, listClassName, onArchive, onAgentContextMenu } =
    props;
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  const fleet = "agents" in props ? props : null;
  const items = fleet ? null : (props as ItemList).items;
  const groupNames = fleet
    ? fleet.agents.map((agent) => agent.name)
    : items!.map((item) => item.id);

  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;

      onReorder(
        reorderAgentGroup(
          fullOrder,
          groupNames,
          String(active.id),
          String(over.id),
          arrayMove,
        ),
      );
    },
    [fullOrder, groupNames, onReorder],
  );

  if (groupNames.length === 0) return null;

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragEnd={handleDragEnd}
    >
      <SortableContext
        items={groupNames}
        strategy={verticalListSortingStrategy}
      >
        <div className={listClassName ?? styles.list}>
          {fleet
            ? fleet.agents.map((agent) => (
                <SortableAgentRow
                  key={agent.name}
                  agent={agent}
                  taskTitle={fleet.agentTasks?.[agent.name]?.title}
                  onAgentClick={fleet.onAgentClick}
                  selected={
                    fleet.selectedAgentName != null &&
                    agent.name.toLowerCase() ===
                      fleet.selectedAgentName.toLowerCase()
                  }
                  onArchive={onArchive}
                  onContextMenu={onAgentContextMenu}
                />
              ))
            : items!.map((item) => (
                <SortableAgentRow
                  key={item.id}
                  id={item.id}
                  label={item.label}
                  below={item.below}
                  onArchive={onArchive}
                  onContextMenu={onAgentContextMenu}
                >
                  {item.content}
                </SortableAgentRow>
              ))}
        </div>
      </SortableContext>
    </DndContext>
  );
}
