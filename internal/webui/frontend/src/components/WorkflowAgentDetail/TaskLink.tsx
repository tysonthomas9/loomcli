export interface TaskLinkProps {
  workspaceId: string;
  taskId: string;
  className?: string | undefined;
}

/** Link a run's durable task identity to the canonical task detail route. */
export function TaskLink({
  workspaceId,
  taskId,
  className,
}: TaskLinkProps): JSX.Element {
  return (
    <a
      className={className}
      href={`/ws/${encodeURIComponent(workspaceId)}/issues/${encodeURIComponent(taskId)}`}
      title={`Open task ${taskId}`}
    >
      {taskId}
    </a>
  );
}
