import {
  useMergeQueue,
  type QueuedMerge,
} from "@/hooks/workspace/useMergeQueue";

import styles from "./MergeQueue.module.css";

/** Plain words for a queued stack merge's phase. */
export function mergePhaseLabel(merge: QueuedMerge): string {
  switch (merge.phase) {
    case "ready":
      return "queued, waiting for checks and reviews";
    case "blocked":
      return merge.reason ? `blocked: ${merge.reason}` : "blocked";
    default:
      return "merging";
  }
}

function queuedByLabel(merge: QueuedMerge): string {
  if (merge.queued_by === "lead") return "Queued by the lead";
  return merge.queued_by ? `Queued by ${merge.queued_by}` : "Queued";
}

/**
 * The workspace's queued, running and blocked stack merges (D38). One server
 * queue serves the lead's `loom merge` and the Merge up to here button.
 */
export function MergeQueue({
  workspaceId,
}: {
  workspaceId: string;
}): JSX.Element | null {
  const queue = useMergeQueue(workspaceId);
  if (queue.length === 0) return null;
  return (
    <section
      className={styles.queue}
      aria-label="Merge queue"
      data-testid="merge-queue"
    >
      {queue.map((merge) => (
        <p
          key={merge.stack_id}
          className={styles.entry}
          data-testid="merge-queue-entry"
          data-phase={merge.phase}
        >
          <strong>Merge up to {merge.target}</strong>
          {merge.pr_number ? (
            <>
              {" "}
              (
              <a href={merge.pr_url} target="_blank" rel="noreferrer">
                #{merge.pr_number}
              </a>
              )
            </>
          ) : null}
          {" — "}
          {mergePhaseLabel(merge)}. {queuedByLabel(merge)}.
        </p>
      ))}
    </section>
  );
}
