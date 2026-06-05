/**
 * PRsPage — Aether Wireframe V3 "Pull Requests" view.
 *
 * Loom has no GitHub PR-list API, but issues carry a real `external_ref` that
 * can be a PR URL (see isPRUrl / getReviewType). This view surfaces those
 * PR-bearing issues as the review queue: each row links out to the PR and
 * opens the issue detail (Git / Diff tabs) for review. Fully data-backed — no
 * stub; it's simply empty until an agent opens a PR.
 */
import { useMemo } from "react";

import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import { isPRUrl } from "@/utils/issue";

import styles from "./PRsPage.module.css";

function statusLabel(status?: string): string {
  if (!status) return "open";
  return status.replace(/_/g, " ");
}

export function PRsPage(): JSX.Element {
  const { issues } = useWorkspaceViewData();
  const { handleIssueClick } = useWorkspaceViewActions();

  // The PR / review queue: an agent's work awaiting merge. In loom that's an
  // issue in `review` status, and/or one carrying a real PR URL in external_ref.
  const prIssues = useMemo(
    () =>
      issues.filter((i) => i.status === "review" || isPRUrl(i.external_ref)),
    [issues],
  );

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>Pull Requests</h1>
        <span className={styles.count}>{prIssues.length}</span>
      </header>
      <p className={styles.subtitle}>
        Open pull requests raised by agents in this workspace. Select one to
        review its diff and files.
      </p>

      {prIssues.length === 0 ? (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>No open pull requests</p>
          <p className={styles.emptyHint}>
            When an agent pushes a branch and opens a PR, it appears here for
            review.
          </p>
        </div>
      ) : (
        <ul className={styles.list}>
          {prIssues.map((issue) => (
            <li key={issue.id} className={styles.row}>
              <button
                type="button"
                className={styles.rowMain}
                onClick={() => handleIssueClick(issue)}
              >
                <span className={styles.rowHead}>
                  <code className={styles.key}>{issue.id}</code>
                  <span className={styles.status} data-status={issue.status}>
                    {statusLabel(issue.status)}
                  </span>
                </span>
                <span className={styles.rowTitle}>{issue.title}</span>
                <span className={styles.meta}>
                  {issue.assignee ? `@${issue.assignee}` : "unassigned"}
                  {issue.repo ? ` · ${issue.repo}` : ""}
                </span>
              </button>
              {issue.external_ref && (
                <a
                  className={styles.prLink}
                  href={issue.external_ref}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  View PR ↗
                </a>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
