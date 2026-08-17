/**
 * Cell rendering functions for IssueTable and IssueRow.
 * Extracted to enable code sharing between components.
 */

import type { ReactNode } from "react";

import { HighlightText } from "@/components/HighlightText";
import { IssueLabelChips } from "@/components/IssueLabelChips";
import type { BlockedInfo } from "@/types/issue";
import type { Issue, Priority, Status, IssueType } from "@/types";

import { BlockedCell } from "./BlockedCell";
import {
  formatPriority,
  getPriorityClassName,
  formatStatus,
  getStatusClassName,
  formatIssueType,
  formatDate,
} from "./columns";

/**
 * Options for rendering cell content.
 */
export interface RenderCellOptions {
  /** Blocked info for the issue (used by 'blocked' column) */
  blockedInfo?: BlockedInfo | undefined;
  /** Click handler for blocked cell */
  onBlockedClick?: (() => void) | undefined;
  /** Search term for title highlighting */
  searchTerm?: string | undefined;
}

/**
 * Render a cell value based on column type.
 * Used by both IssueTable and IssueRow for consistent cell rendering.
 */
export function renderCellContent(
  columnId: string,
  value: unknown,
  issue: Issue,
  options?: RenderCellOptions,
): ReactNode {
  switch (columnId) {
    case "id":
      return <span className="issue-table__id">{String(value)}</span>;

    case "priority": {
      const priority = value as Priority;
      const validPriority = priority >= 0 && priority <= 4 ? priority : 2;
      return (
        <span
          className={`issue-table__priority ${getPriorityClassName(validPriority)}`}
        >
          {formatPriority(validPriority)}
        </span>
      );
    }

    case "title":
      return (
        <div className="issue-table__title-content">
          <span className="issue-table__title" title={String(value)}>
            <HighlightText
              text={String(value)}
              searchTerm={options?.searchTerm ?? ""}
            />
          </span>
          <IssueLabelChips labels={issue.labels} />
        </div>
      );

    case "status": {
      const status = value as Status | undefined;
      return (
        <span className={`issue-table__status ${getStatusClassName(status)}`}>
          {formatStatus(status)}
        </span>
      );
    }

    case "blocked": {
      const blockedInfo = options?.blockedInfo;
      return (
        <BlockedCell
          blockedByCount={blockedInfo?.blockedByCount ?? 0}
          blockedBy={blockedInfo?.blockedBy}
          onClick={options?.onBlockedClick}
        />
      );
    }

    case "issue_type": {
      const issueType = value as IssueType | undefined;
      return (
        <span className="issue-table__type">{formatIssueType(issueType)}</span>
      );
    }

    case "assignee":
      return (
        <span className="issue-table__assignee">
          {value ? String(value) : "-"}
        </span>
      );

    case "repo":
      return (
        <span className="issue-table__repo">
          {value ? String(value) : "\u2014"}
        </span>
      );

    case "updated_at":
      return (
        <span className="issue-table__date">
          {formatDate(value as string | undefined)}
        </span>
      );

    default:
      return value != null ? String(value) : "-";
  }
}
