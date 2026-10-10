import type React from "react";
import type { ReactNode } from "react";
import { useSortable } from "@dnd-kit/sortable";

import { AgentCard } from "@/components/AgentCard";
import type { LoomAgentStatus } from "@/types";

import styles from "./AgentSection.module.css";
import { ArchiveIcon } from "./ArchiveIcon";

interface RowActions {
  onArchive?: ((agentName: string) => void) | undefined;
  onContextMenu?:
    | ((event: React.MouseEvent, agentName: string) => void)
    | undefined;
}

/** A fleet agent, drawn as its AgentCard and keyed by its name. */
interface FleetRow {
  agent: LoomAgentStatus;
  taskTitle?: string | undefined;
  onAgentClick?: ((agentName: string) => void) | undefined;
  selected?: boolean | undefined;
}

/** A row with its own content (Agent API agents), keyed by id. */
interface ContentRow {
  id: string;
  /** The agent's name, for the archive and drag labels. */
  label: string;
  children: ReactNode;
  /** No drag handle and no drop target: the row moves with its parent. */
  pinned?: boolean | undefined;
  /** Shown under the row (its children), dragged with it as one unit. */
  below?: ReactNode;
}

export type SortableAgentRowProps = RowActions & (FleetRow | ContentRow);

export function SortableAgentRow(props: SortableAgentRowProps): JSX.Element {
  const { onArchive, onContextMenu } = props;
  const fleet = "agent" in props ? props : null;
  const id = fleet ? fleet.agent.name : (props as ContentRow).id;
  const label = fleet ? fleet.agent.name : (props as ContentRow).label;
  const pinned = !fleet && (props as ContentRow).pinned === true;
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id,
    disabled: { draggable: pinned, droppable: pinned },
  });

  const style: React.CSSProperties = {
    transform: transform
      ? `translate3d(${transform.x}px, ${transform.y}px, 0)`
      : undefined,
    transition: transition ?? undefined,
    opacity: isDragging ? 0.6 : 1,
  };

  const onAgentClick = fleet?.onAgentClick;
  const handleClick = onAgentClick ? () => onAgentClick(id) : undefined;

  const row = (
    <div
      {...(fleet ? { ref: setNodeRef, style } : {})}
      className={styles.agentRow}
      data-dragging={isDragging || undefined}
      data-testid="sortable-agent-row"
      onContextMenu={(event) => {
        if (!onContextMenu) return;
        event.preventDefault();
        onContextMenu(event, id);
      }}
    >
      {fleet ? (
        <AgentCard
          agent={fleet.agent}
          compact
          selected={fleet.selected ?? false}
          showRepoBadge={false}
          taskTitle={fleet.taskTitle}
          className={styles.agentCardInRow}
          onClick={handleClick}
        />
      ) : (
        (props as ContentRow).children
      )}
      {onArchive && (
        <button
          type="button"
          className={styles.archiveButton}
          aria-label={`Archive ${label}`}
          data-testid="agent-row-archive"
          onClick={(event) => {
            event.stopPropagation();
            event.preventDefault();
            onArchive(id);
          }}
          onPointerDown={(event) => event.stopPropagation()}
          onKeyDown={(event) => event.stopPropagation()}
        >
          <ArchiveIcon />
        </button>
      )}
      {!pinned && (
        <span
          className={styles.dragHandle}
          {...attributes}
          {...listeners}
          aria-label={`Drag to reorder ${label}`}
          onClick={(event) => event.stopPropagation()}
          onKeyDown={(event) => {
            // The sensor starts here, then listens on document while dragging.
            (listeners?.onKeyDown as React.KeyboardEventHandler | undefined)?.(
              event,
            );
            if (!isDragging) event.stopPropagation();
          }}
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
      )}
    </div>
  );
  if (fleet) return row;
  return (
    <div
      ref={setNodeRef}
      style={style}
      className={styles.sortableItem}
      data-dragging={isDragging || undefined}
      data-testid="sortable-agent-item"
    >
      {row}
      {(props as ContentRow).below}
    </div>
  );
}
