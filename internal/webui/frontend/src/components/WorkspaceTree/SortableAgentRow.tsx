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
  onArchive?: ((agentName: string) => void) | undefined;
  onContextMenu?:
    | ((event: React.MouseEvent, agentName: string) => void)
    | undefined;
}

function ArchiveIcon(): JSX.Element {
  // Lucide-style archive box (lid + body + slot) — not a trash can.
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      aria-hidden
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <rect width="20" height="5" x="2" y="3" rx="1" />
      <path d="M4 8v11a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8" />
      <path d="M10 12h4" />
    </svg>
  );
}

export function SortableAgentRow({
  agent,
  taskTitle,
  onAgentClick,
  selected = false,
  onArchive,
  onContextMenu,
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
      data-testid="sortable-agent-row"
      onContextMenu={(event) => {
        if (!onContextMenu) return;
        event.preventDefault();
        onContextMenu(event, agent.name);
      }}
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
      {onArchive && (
        <button
          type="button"
          className={styles.archiveButton}
          aria-label={`Archive ${agent.name}`}
          data-testid="agent-row-archive"
          onClick={(event) => {
            event.stopPropagation();
            event.preventDefault();
            onArchive(agent.name);
          }}
          onPointerDown={(event) => event.stopPropagation()}
          onKeyDown={(event) => event.stopPropagation()}
        >
          <ArchiveIcon />
        </button>
      )}
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
