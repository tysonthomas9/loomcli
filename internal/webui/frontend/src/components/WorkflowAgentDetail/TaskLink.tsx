import type { MouseEvent } from "react";

export interface TaskLinkProps {
  workspaceId: string;
  taskId: string;
  className?: string | undefined;
  onOpenTask?: ((taskId: string) => void) | undefined;
}

/**
 * Link a run's durable task identity to its canonical route.
 *
 * Agent pages can intercept an ordinary primary click to preserve the agent
 * route and open Loom's inline task pane. Modified clicks retain native anchor
 * behavior so users can copy the URL or open the canonical task route in a new
 * tab.
 */
export function TaskLink({
  workspaceId,
  taskId,
  className,
  onOpenTask,
}: TaskLinkProps): JSX.Element {
  const handleClick = (event: MouseEvent<HTMLAnchorElement>): void => {
    if (
      !onOpenTask ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    event.preventDefault();
    onOpenTask(taskId);
  };

  return (
    <a
      className={className}
      href={`/ws/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(taskId)}`}
      title={`Open task ${taskId}`}
      onClick={handleClick}
    >
      {taskId}
    </a>
  );
}
