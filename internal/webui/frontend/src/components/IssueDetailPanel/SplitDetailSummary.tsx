/**
 * SplitDetailSummary component.
 * Condensed issue details shown in the top pane of the terminal split view.
 * Shows description and design panel.
 */

import { updateIssue } from "@/hooks/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, IssueDetails } from "@/types";

import { EditableDescription, DesignPanel } from "./sections";
import styles from "./IssueDetailPanel.module.css";

export interface SplitDetailSummaryProps {
  issue: Issue | IssueDetails;
  onIssueUpdate?: ((issue: Issue) => void) | undefined;
}

export function SplitDetailSummary({
  issue,
  onIssueUpdate,
}: SplitDetailSummaryProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  return (
    <div className={styles.detailContent}>
      <div className={issue.design ? styles.detailColumns : undefined}>
        <div
          className={
            issue.design ? styles.detailColumnLeft : styles.detailColumnFull
          }
        >
          <section className={styles.section}>
            <h3 className={styles.sectionTitle}>Description</h3>
            <EditableDescription
              description={issue.description}
              isEditable={true}
              onSave={async (newDescription) => {
                const updatedIssue = await updateIssue(workspaceId, issue.id, {
                  description: newDescription,
                });
                onIssueUpdate?.(updatedIssue);
              }}
            />
          </section>
        </div>
        {issue.design && (
          <div
            className={styles.detailColumnRight}
            data-testid="design-section"
          >
            <DesignPanel content={issue.design} />
          </div>
        )}
      </div>
    </div>
  );
}
