/**
 * WorkflowDetail — the right panel of the Workflows view for one selected
 * workflow: the built-in update banner, the version history + lifecycle actions,
 * and the authoring panel. All data + actions come from useWorkflowVersions.
 */

import type { CreateWorkflowVersionInput } from "@/api";
import { useWorkflowVersions } from "@/hooks";

import { AuthorVersionPanel } from "./AuthorVersionPanel";
import { BuiltinUpdateBanner } from "./BuiltinUpdateBanner";
import { WorkflowVersionsTable } from "./WorkflowVersionsTable";
import styles from "./WorkflowsDashboard.module.css";

export interface WorkflowDetailProps {
  workspaceId: string;
  workflowName: string;
  /**
   * Called after a successful lifecycle action so the parent can refresh
   * anything derived from the workflow summary (e.g. the left-rail trust badge),
   * which this panel's own hook does not own.
   */
  onMutated?: (() => void) | undefined;
}

export function WorkflowDetail({
  workspaceId,
  workflowName,
  onMutated,
}: WorkflowDetailProps): JSX.Element {
  const {
    data,
    isLoading,
    error,
    actionPending,
    actionError,
    approve,
    unapprove,
    activate,
    rollback,
    adoptBuiltin,
    authorVersion,
  } = useWorkflowVersions(workspaceId, workflowName);

  // Fire-and-forget wrappers: the hook records actionError; a rejected promise
  // here would otherwise surface as an unhandled rejection. On success we also
  // notify the parent so summary-derived UI (the left-rail badge) stays in sync.
  const swallow = (p: Promise<void>) => {
    void p.then(() => onMutated?.()).catch(() => undefined);
  };

  return (
    <section className={styles.detail} data-testid="workflow-detail">
      <h2 className={styles.detailTitle}>{workflowName}</h2>

      {error ? (
        <p className={styles.error} data-testid="detail-error">
          {error.message}
        </p>
      ) : null}
      {actionError ? (
        <p className={styles.error} data-testid="action-error">
          {actionError.message}
        </p>
      ) : null}

      <BuiltinUpdateBanner
        builtin={data?.builtin}
        pending={actionPending}
        onAdopt={() => swallow(adoptBuiltin())}
      />

      {isLoading && !data ? (
        <p className={styles.subtle} data-testid="detail-loading">
          Loading versions…
        </p>
      ) : (
        <WorkflowVersionsTable
          versions={data?.versions ?? []}
          pending={actionPending}
          onApprove={(id) => swallow(approve(id))}
          onUnapprove={(id) => swallow(unapprove(id))}
          onActivate={(id) => swallow(activate(id))}
          onRollback={(id) => swallow(rollback(id))}
        />
      )}

      <AuthorVersionPanel
        workflowName={workflowName}
        pending={actionPending}
        onAuthor={(input: CreateWorkflowVersionInput) =>
          swallow(authorVersion(input))
        }
      />
    </section>
  );
}
