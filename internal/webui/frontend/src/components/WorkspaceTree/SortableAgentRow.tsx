import type React from "react";
import { useSortable } from "@dnd-kit/sortable";

import { AgentCard } from "@/components/AgentCard";
import type { LoomAgentStatus } from "@/types";

import styles from "./AgentSection.module.css";

export interface SortableAgentRowProps {
  agent: LoomAgentStatus;
  taskTitle?: string | undefined;
  onAgentClick?: ((agentName: string) => void) | undefined;
  selected?: boolean | undefined;
}

export function SortableAgentRow({
  agent,
  taskTitle,
  onAgentClick,
  selected = false,
}: SortableAgentRowProps): JSX.Element {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: agent.name });

  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0)`
      : undefined,
    transition: transition ?? undefined,
    opacity: isDragging ? 0.6 : 1,
  };

  const handleClick = onAgentClick ? () => onAgentClick(agent.name) : undefined;

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={styles.agentRow}
      data-dragging={isDragging || undefined}
    >
      <AgentCard
        agent={agent}
        compact
        selected={selected}
        showRepoBadge={false}
        taskTitle={taskTitle}
        className={styles.agentCardInRow}
        onClick={handleClick}
      />
      <span
        className={styles.dragHandle}
        {...attributes}
        {...listeners}
        aria-label={`Drag to reorder ${agent.name}`}
        onClick={(event) => event.stopPropagation()}
        onKeyDown={(event) => event.stopPropagation()}
      >
        <svg width="8" height="14" viewBox="0 0 8 14" fill="currentColor">
          <circle cx="2" cy="2" r="1.2" />
          <circle cx="6" cy="2" r="1.2" />
          <circle cx="2" cy="7" r="1.2" />
          <circle cx="6" cy="7" r="1.2" />
          <circle cx="2" cy="12" r="1.2" />
          <circle cx="6" cy="12" r="1.2" />
        </svg>
      </span>
    </div>
  );
}
