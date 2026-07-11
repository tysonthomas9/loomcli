import { useCallback } from "react";

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

export interface SortableAgentListProps {
  agents: LoomAgentStatus[];
  fullOrder: string[];
  onReorder: (nextOrder: string[]) => void;
  onAgentClick?: ((agentName: string) => void) | undefined;
  selectedAgentName?: string | null | undefined;
  agentTasks?: Record<string, { title: string }> | undefined;
  listClassName?: string | undefined;
}

export function SortableAgentList({
  agents,
  fullOrder,
  onReorder,
  onAgentClick,
  selectedAgentName = null,
  agentTasks,
  listClassName,
}: SortableAgentListProps): JSX.Element | null {
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor),
  );

  const groupNames = agents.map((agent) => agent.name);

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

  if (agents.length === 0) return null;

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
          {agents.map((agent) => (
            <SortableAgentRow
              key={agent.name}
              agent={agent}
              taskTitle={agentTasks?.[agent.name]?.title}
              onAgentClick={onAgentClick}
              selected={
                selectedAgentName != null &&
                agent.name.toLowerCase() === selectedAgentName.toLowerCase()
              }
            />
          ))}
        </div>
      </SortableContext>
    </DndContext>
  );
}
