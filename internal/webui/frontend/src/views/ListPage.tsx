/**
 * ListPage — the Aether design's List layout: issues grouped by epic into
 * collapsible sections of flat rows (status dot · id · title · status chip ·
 * assignee), each section headed by the epic title + count + open-epic
 * button. The sortable data table remains available at /table.
 */

import { useMemo, useState } from "react";

import {
  groupIssuesByField,
  sortLanes,
  type LaneGroup,
} from "@/components/SwimLaneBoard/groupingUtils";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import type { Issue, Status } from "@/types";
import { formatIssueId, formatStatusLabel } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import styles from "./ListPage.module.css";

/** Row ordering within a section — kanban column order (design COL_RANK). */
const STATUS_RANK: Record<string, number> = {
  deferred: 0,
  open: 1,
  blocked: 2,
  in_progress: 3,
  review: 4,
  closed: 5,
};

function statusKey(issue: Issue): Status {
  return (issue.status ?? "open") as Status;
}

function Avatar({ name }: { name: string }): JSX.Element {
  const color = getAvatarColor(name);
  return (
    <span
      className={styles.avatar}
      style={{
        backgroundColor: color,
        color: shouldUseWhiteText(color) ? "#fff" : "#171717",
      }}
      title={name}
      aria-label={`Assignee ${name}`}
    >
      {name.replace(/^\[H\]\s*/, "").charAt(0).toUpperCase() || "?"}
    </span>
  );
}

export function ListPage(): JSX.Element {
  const { filteredIssues } = useWorkspaceViewData();
  const { handleIssueClick } = useWorkspaceViewActions();
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());

  const lanes = useMemo<LaneGroup[]>(() => {
    const grouped = groupIssuesByField(filteredIssues, "epic");
    // Hide empty sections (design returns null for empty epic groups) unless
    // the epic itself is still open.
    const visible = grouped.filter(
      (lane) =>
        lane.issues.length > 0 || lane.groupIssue?.status !== "closed",
    );
    return sortLanes(visible, "title");
  }, [filteredIssues]);

  const toggle = (id: string): void => {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  return (
    <div className={styles.page} data-testid="list-page">
      <div className={styles.inner}>
        {lanes.length === 0 && (
          <p className={styles.empty}>No issues yet — create one to start.</p>
        )}
        {lanes.map((lane) => {
          const isCollapsed = collapsed.has(lane.id);
          const rows = [...lane.issues].sort(
            (a, b) =>
              (STATUS_RANK[statusKey(a)] ?? 9) -
              (STATUS_RANK[statusKey(b)] ?? 9),
          );
          return (
            <section key={lane.id} className={styles.epicSection}>
              <div className={styles.epicHead}>
                <button
                  type="button"
                  className={styles.epicToggle}
                  onClick={() => toggle(lane.id)}
                  aria-expanded={!isCollapsed}
                >
                  <span
                    className={styles.chevron}
                    data-collapsed={isCollapsed || undefined}
                    aria-hidden="true"
                  >
                    ▸
                  </span>
                  <h2 className={styles.epicTitle}>{lane.title}</h2>
                  <span className={styles.count}>{lane.issues.length}</span>
                </button>
                {lane.groupIssue && (
                  <button
                    type="button"
                    className={styles.epicOpen}
                    onClick={() => handleIssueClick(lane.groupIssue as Issue)}
                    aria-label={`Open epic: ${lane.title}`}
                    title="View epic details"
                  >
                    ⤢
                  </button>
                )}
              </div>
              {!isCollapsed && (
                <ul className={styles.rows}>
                  {rows.length === 0 && (
                    <li className={styles.emptyRow}>No issues in this epic</li>
                  )}
                  {rows.map((issue) => {
                    const status = statusKey(issue);
                    return (
                      <li key={issue.id}>
                        <button
                          type="button"
                          className={styles.row}
                          onClick={() => handleIssueClick(issue)}
                        >
                          <span
                            className={styles.dot}
                            data-status={status}
                            aria-hidden="true"
                          />
                          <code className={styles.rowId} title={issue.id}>
                            {formatIssueId(issue.id)}
                          </code>
                          <span className={styles.rowTitle}>{issue.title}</span>
                          <span className={styles.spacer} />
                          <span
                            className={styles.statusChip}
                            data-status={status}
                          >
                            {formatStatusLabel(status)}
                          </span>
                          {issue.assignee && <Avatar name={issue.assignee} />}
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </section>
          );
        })}
      </div>
    </div>
  );
}
