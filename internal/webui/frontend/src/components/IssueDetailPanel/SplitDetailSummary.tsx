/**
 * SplitDetailSummary component.
 * Condensed issue details shown in the top pane of the terminal split view.
 * Shows metadata dropdowns, description, and design panel.
 */

import { updateIssue } from "@/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { Issue, IssueDetails, Priority, IssueType } from "@/types";
import type { LoomAgentStatus, LoomTaskInfo } from "@/types";

import { EditableDescription } from "./EditableDescription";
import { DesignPanel } from "./DesignPanel";
import { PriorityDropdown } from "./PriorityDropdown";
import { TypeDropdown } from "./TypeDropdown";
import { AssigneeDropdown } from "./AssigneeDropdown";
import styles from "./IssueDetailPanel.module.css";

export interface SplitDetailSummaryProps {
  issue: Issue | IssueDetails;
  isSavingPriority: boolean;
  isSavingType: boolean;
  isSavingAssignee: boolean;
  agents: LoomAgentStatus[];
  agentTasks: Record<string, LoomTaskInfo>;
  onPrioritySave: (priority: Priority) => Promise<void>;
  onTypeSave: (type: IssueType) => Promise<void>;
  onAssigneeSave: (assignee: string) => Promise<void>;
  onIssueUpdate?: ((issue: Issue) => void) | undefined;
}

export function SplitDetailSummary({
  issue,
  isSavingPriority,
  isSavingType,
  isSavingAssignee,
  agents,
  agentTasks,
  onPrioritySave,
  onTypeSave,
  onAssigneeSave,
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
          <div className={styles.statusRow}>
            <PriorityDropdown
              priority={issue.priority as Priority}
              onSave={onPrioritySave}
              isSaving={isSavingPriority}
            />
            <TypeDropdown
              type={issue.issue_type}
              onSave={onTypeSave}
              isSaving={isSavingType}
            />
            <AssigneeDropdown
              assignee={issue.assignee}
              onSave={onAssigneeSave}
              isSaving={isSavingAssignee}
              agents={agents}
              agentTasks={agentTasks}
            />
          </div>
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
