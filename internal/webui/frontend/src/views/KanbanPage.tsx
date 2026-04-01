import { ErrorBoundary, SwimLaneBoard } from "@/components";
import type { BlockedInfo } from "@/components/KanbanBoard";
import type { GroupByField } from "@/components/SwimLaneBoard";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { Issue, Status } from "@/types";

import styles from "./KanbanPage.module.css";

export interface KanbanPageProps {
  filteredIssues: Issue[];
  groupBy: GroupByField;
  onDragEnd: (issueId: string, newStatus: Status, oldStatus: Status) => void;
  onIssueClick: (issue: Issue) => void;
  isMultiRepo: boolean;
  activeView: ViewMode;
  blockedIssuesMap?: Map<string, BlockedInfo> | undefined;
  showBlocked?: boolean | undefined;
  pendingIds?: Set<string> | undefined;
}

export function KanbanPage({
  filteredIssues,
  groupBy,
  onDragEnd,
  onIssueClick,
  isMultiRepo,
  activeView,
  blockedIssuesMap,
  showBlocked,
  pendingIds,
}: KanbanPageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <div className={styles.kanbanShell}>
        <SwimLaneBoard
          issues={filteredIssues}
          groupBy={groupBy}
          onDragEnd={onDragEnd}
          onIssueClick={onIssueClick}
          isMultiRepo={isMultiRepo}
          {...(blockedIssuesMap !== undefined && {
            blockedIssues: blockedIssuesMap,
          })}
          {...(showBlocked !== undefined && {
            showBlocked,
          })}
          {...(pendingIds !== undefined &&
            pendingIds.size > 0 && { pendingIds })}
        />
      </div>
    </ErrorBoundary>
  );
}
