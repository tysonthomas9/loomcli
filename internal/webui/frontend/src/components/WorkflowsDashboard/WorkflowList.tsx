/**
 * WorkflowList — the left rail of the Workflows view: every registered workflow
 * with a built-in badge and its active version's trust, selectable to load the
 * detail panel.
 */

import type { WorkflowSummary } from "@/api";

import styles from "./WorkflowsDashboard.module.css";

export interface WorkflowListProps {
  workflows: WorkflowSummary[];
  selected?: string | undefined;
  onSelect: (name: string) => void;
}

export function WorkflowList({
  workflows,
  selected,
  onSelect,
}: WorkflowListProps): JSX.Element {
  if (workflows.length === 0) {
    return (
      <p className={styles.empty} data-testid="workflows-empty">
        No workflows registered.
      </p>
    );
  }

  return (
    <ul className={styles.workflowList} data-testid="workflow-list">
      {workflows.map((wf) => (
        <li key={wf.driver_id}>
          <button
            type="button"
            className={styles.workflowItem}
            aria-current={wf.name === selected}
            data-selected={wf.name === selected}
            onClick={() => onSelect(wf.name)}
            data-testid={`workflow-item-${wf.name}`}
          >
            <span className={styles.workflowName}>{wf.name}</span>
            {wf.built_in ? (
              <span className={styles.badge} data-variant="builtin">
                built-in
              </span>
            ) : null}
            {wf.effective_trust ? (
              <span className={styles.badge} data-variant={wf.effective_trust}>
                {wf.effective_trust}
              </span>
            ) : null}
          </button>
        </li>
      ))}
    </ul>
  );
}
