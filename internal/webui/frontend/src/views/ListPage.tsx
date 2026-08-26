/**
 * ListPage — the Aether design's List layout: issues grouped by epic into
 * collapsible sections of flat rows (status dot · id · title · status chip ·
 * assignee), each section headed like the kanban swim-lane epic header.
 * The sortable data table remains available at /table.
 */

import { useMemo, useState } from "react";
import { useStore } from "zustand";

import {
  groupIssuesByField,
  sortLanes,
  type LaneGroup,
} from "@/components/SwimLaneBoard/groupingUtils";
import laneStyles from "@/components/SwimLane/SwimLane.module.css";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import { useAgentStoreInstance } from "@/hooks/common";
import { useRunEpicWorkflow } from "@/hooks/workspace";
import type { Issue, Status } from "@/types";
import { buildEpicLeadClaims } from "@/utils/agentRole";
import { formatIssueId, formatStatusLabel, isPRUrl } from "@/utils/issue";
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
      {name
        .replace(/^\[H\]\s*/, "")
        .charAt(0)
        .toUpperCase() || "?"}
    </span>
  );
}

export function ListPage(): JSX.Element {
  const { filteredIssues } = useWorkspaceViewData();
  const { handleIssueClick, showToast } = useWorkspaceViewActions();
  const { runEpic, isRunningEpic } = useRunEpicWorkflow({ showToast });
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const epicLeadClaims = useMemo(() => buildEpicLeadClaims(agents), [agents]);

  const lanes = useMemo<LaneGroup[]>(() => {
    const grouped = groupIssuesByField(filteredIssues, "epic");
    const visible = grouped.filter(
      (lane) => lane.issues.length > 0 || lane.groupIssue?.status !== "closed",
    );
    return sortLanes(visible, "title");
  }, [filteredIssues]);

  // Precompute per-lane row ordering and PR counts so collapse toggles (which
  // re-render ListPage) don't re-sort/-filter every lane's issues each render.
  const laneViews = useMemo(
    () =>
      lanes.map((lane) => ({
        lane,
        rows: [...lane.issues].sort(
          (a, b) =>
            (STATUS_RANK[statusKey(a)] ?? 9) - (STATUS_RANK[statusKey(b)] ?? 9),
        ),
        prCount: lane.issues.filter((issue) => isPRUrl(issue.external_ref))
          .length,
      })),
    [lanes],
  );

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
        {laneViews.map(({ lane, rows, prCount }) => {
          const isCollapsed = collapsed.has(lane.id);
          const headerId = `list-lane-header-${lane.id}`;
          const epicKey =
            lane.groupIssue?.id ?? lane.id.replace(/^lane-epic-/, "");
          const epicRunner =
            epicKey !== "__ungrouped__"
              ? (epicLeadClaims.get(epicKey) ?? null)
              : undefined;
          const epicDisplayId = lane.groupIssue
            ? formatIssueId(lane.groupIssue.id)
            : undefined;
          const openEpicAriaLabel = epicDisplayId
            ? `Open epic ${epicDisplayId}: ${lane.title}`
            : `Open epic: ${lane.title}`;
          const runEpicLabel = epicDisplayId
            ? `Run epic ${epicDisplayId}`
            : "Run epic";
          const showRunEpic =
            lane.groupIssue?.issue_type === "epic" &&
            lane.groupIssue.status !== "closed" &&
            epicRunner === null;
          const runningEpic = lane.groupIssue
            ? isRunningEpic(lane.groupIssue.id)
            : false;
          const laneTitleContent = (
            <span className={laneStyles.laneTitleContent}>
              <span className={laneStyles.laneTitleText}>{lane.title}</span>
              {epicDisplayId !== undefined && (
                <span
                  className={laneStyles.laneTitleId}
                  title={lane.groupIssue?.id}
                >
                  {epicDisplayId}
                </span>
              )}
            </span>
          );

          return (
            <section
              key={lane.id}
              className={laneStyles.swimLane}
              data-collapsed={isCollapsed}
              aria-labelledby={headerId}
            >
              <header
                className={`${laneStyles.laneHeader} ${styles.laneHeader}`}
                id={headerId}
              >
                <button
                  type="button"
                  className={laneStyles.collapseToggle}
                  onClick={() => toggle(lane.id)}
                  aria-expanded={!isCollapsed}
                  aria-label={
                    isCollapsed
                      ? `Expand ${lane.title}`
                      : `Collapse ${lane.title}`
                  }
                  data-testid="collapse-toggle"
                >
                  <svg
                    className={laneStyles.chevronIcon}
                    width="16"
                    height="16"
                    viewBox="0 0 16 16"
                    fill="none"
                    aria-hidden="true"
                  >
                    <path
                      d="M6 4l4 4-4 4"
                      stroke="currentColor"
                      strokeWidth="2"
                      strokeLinecap="round"
                      strokeLinejoin="round"
                    />
                  </svg>
                </button>
                <h2 className={laneStyles.laneTitle}>
                  {lane.groupIssue ? (
                    <button
                      type="button"
                      className={laneStyles.laneTitleButton}
                      onClick={() => handleIssueClick(lane.groupIssue as Issue)}
                      aria-label={openEpicAriaLabel}
                      data-testid="lane-title-button"
                    >
                      {laneTitleContent}
                    </button>
                  ) : (
                    laneTitleContent
                  )}
                </h2>
                {lane.groupIssue && prCount > 0 && (
                  <span
                    className={laneStyles.lanePrCount}
                    aria-label={`${prCount} open pull request${prCount === 1 ? "" : "s"}`}
                    title={`${prCount} open pull request${prCount === 1 ? "" : "s"}`}
                  >
                    <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
                      <circle
                        cx="4"
                        cy="4"
                        r="1.6"
                        stroke="currentColor"
                        strokeWidth="1.4"
                      />
                      <circle
                        cx="4"
                        cy="12"
                        r="1.6"
                        stroke="currentColor"
                        strokeWidth="1.4"
                      />
                      <circle
                        cx="12"
                        cy="12"
                        r="1.6"
                        stroke="currentColor"
                        strokeWidth="1.4"
                      />
                      <path
                        d="M4 5.6v4.8M12 10.4V8a2 2 0 0 0-2-2H7.5"
                        stroke="currentColor"
                        strokeWidth="1.4"
                        strokeLinecap="round"
                      />
                    </svg>
                    {prCount} {prCount === 1 ? "PR" : "PRs"}
                  </span>
                )}
                <span
                  className={laneStyles.laneCount}
                  aria-label={`${lane.issues.length} issues`}
                >
                  {lane.issues.length}
                </span>
                {showRunEpic && lane.groupIssue ? (
                  <button
                    type="button"
                    className={laneStyles.runEpicButton}
                    onClick={(event) => {
                      event.stopPropagation();
                      void runEpic(lane.groupIssue as Issue);
                    }}
                    disabled={runningEpic}
                    aria-label={
                      runningEpic ? `Starting ${runEpicLabel}` : runEpicLabel
                    }
                    data-testid="lane-run-epic-button"
                  >
                    {runningEpic ? "Starting" : "Run"}
                  </button>
                ) : null}
                {epicRunner !== undefined &&
                  (epicRunner !== null ? (
                    <span
                      className={laneStyles.runnerBadge}
                      title={`Epic run by ${epicRunner}`}
                      data-testid="lane-runner-badge"
                    >
                      <span
                        className={laneStyles.runnerDot}
                        aria-hidden="true"
                      />
                      {epicRunner}
                    </span>
                  ) : (
                    <span
                      className={laneStyles.unclaimedBadge}
                      data-testid="lane-unclaimed-badge"
                    >
                      Unclaimed
                    </span>
                  ))}
              </header>
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
                          <span className={styles.avatarSlot}>
                            {issue.assignee && <Avatar name={issue.assignee} />}
                          </span>
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
