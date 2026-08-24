/**
 * WorkflowsDashboard — the Workflows management view. Left: the workflow list.
 * Right: the selected workflow's versions, update banner, and authoring panel.
 *
 * DEV-V5-40 (spec DEV-V5-32 UI) + the versioning UI deferred from DEV-V5-41.
 */

import { useState } from "react";
import { useParams } from "react-router-dom";

import { useWorkflows } from "@/hooks";

import { WorkflowDetail } from "./WorkflowDetail";
import { WorkflowList } from "./WorkflowList";
import styles from "./WorkflowsDashboard.module.css";

export function WorkflowsDashboard(): JSX.Element {
  const { workspaceId = "" } = useParams<{ workspaceId: string }>();
  const { workflows, isLoading, error, refetch } = useWorkflows(workspaceId);
  const [selected, setSelected] = useState<string | undefined>(undefined);

  // Default to the first workflow without an effect: an explicit selection wins,
  // otherwise fall back to the first row once the list has loaded.
  const activeName =
    selected && workflows.some((wf) => wf.name === selected)
      ? selected
      : workflows[0]?.name;

  return (
    <div className={styles.dashboard} data-testid="workflows-dashboard">
      <aside className={styles.sidebar}>
        <div className={styles.sidebarHeader}>
          <h1 className={styles.heading}>Workflows</h1>
          <button
            type="button"
            className={styles.refreshButton}
            onClick={() => void refetch()}
            disabled={isLoading}
            data-testid="workflows-refresh"
          >
            Refresh
          </button>
        </div>
        {error ? (
          <p className={styles.error} data-testid="workflows-error">
            {error.message}
          </p>
        ) : null}
        <WorkflowList
          workflows={workflows}
          selected={activeName}
          onSelect={setSelected}
        />
      </aside>
      <main className={styles.content}>
        {activeName ? (
          <WorkflowDetail
            key={activeName}
            workspaceId={workspaceId}
            workflowName={activeName}
            onMutated={refetch}
          />
        ) : (
          <p className={styles.subtle} data-testid="no-workflow-selected">
            {isLoading ? "Loading workflows…" : "Select a workflow."}
          </p>
        )}
      </main>
    </div>
  );
}
