/**
 * PRsPage — Pull Requests view backed by real GitHub data (gh pr list).
 *
 * GitHub is the source of truth for title, state, draft status, and review
 * decision. Loom issues are joined by matching external_ref to the PR URL so
 * review can open the in-app diff workspace when a ticket is linked.
 */
import { useMemo, useState, type KeyboardEvent } from "react";
import { useSearchParams } from "react-router-dom";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue } from "@/types";
import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { usePullRequests } from "@/hooks/workspace";
import { normalizePrUrl } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import { PRReviewWorkspace } from "./PRReviewWorkspace";
import styles from "./PRsPage.module.css";

type PRFilter = "all" | "review" | "open" | "merged";
type GroupMode = "none" | "repo" | "epic";

const FILTERS: { id: PRFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "review", label: "Needs review" },
  { id: "open", label: "Open" },
  { id: "merged", label: "Merged" },
];

const GROUPS: { id: GroupMode; label: string }[] = [
  { id: "none", label: "None" },
  { id: "repo", label: "Repo" },
  { id: "epic", label: "Epic" },
];

export interface PullRequestRow {
  pr: GitPullRequest;
  issue?: Issue | undefined;
}

function isOpenPr(pr: GitPullRequest): boolean {
  return pr.state === "OPEN" && !pr.is_draft;
}

function needsReview(pr: GitPullRequest): boolean {
  if (!isOpenPr(pr)) return false;
  if (pr.review_decision === "APPROVED") return false;
  return true;
}

function matchesFilter(row: PullRequestRow, filter: PRFilter): boolean {
  const { pr } = row;
  switch (filter) {
    case "all":
      return true;
    case "review":
      return needsReview(pr);
    case "open":
      return isOpenPr(pr);
    case "merged":
      return pr.state === "MERGED";
    default:
      return true;
  }
}

function groupKeyFor(row: PullRequestRow, mode: GroupMode): string {
  if (mode === "repo") return row.pr.repo_name || row.issue?.repo || "No repo";
  if (mode === "epic") return row.issue?.parent_title || "No epic";
  return "";
}

/** Map GitHub PR metadata to a display label and CSS state key. */
export function prStateFromGithub(
  pr: GitPullRequest,
  issue?: Issue,
): { label: string; key: string } {
  if (pr.is_draft) return { label: "Draft", key: "open" };
  if (pr.state === "MERGED") return { label: "Merged", key: "merged" };
  if (pr.state === "CLOSED") return { label: "Closed", key: "merged" };
  if (pr.review_decision === "CHANGES_REQUESTED") {
    return { label: "Changes", key: "review" };
  }
  if (pr.review_decision === "APPROVED") {
    return { label: "Approved", key: "open" };
  }
  if (issue?.status === "review") return { label: "Review", key: "review" };
  return { label: "Open", key: "open" };
}

function buildIssueByPrUrl(issues: Issue[]): Map<string, Issue> {
  const map = new Map<string, Issue>();
  for (const issue of issues) {
    const key = normalizePrUrl(issue.external_ref);
    if (key && !map.has(key)) {
      map.set(key, issue);
    }
  }
  return map;
}

/** Leading pull-request glyph for a row. */
function PRGlyph(): JSX.Element {
  return (
    <svg viewBox="0 0 16 16" fill="none" aria-hidden="true">
      <circle cx="4" cy="4" r="1.7" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="4" cy="12" r="1.7" stroke="currentColor" strokeWidth="1.4" />
      <circle cx="12" cy="12" r="1.7" stroke="currentColor" strokeWidth="1.4" />
      <path
        d="M4 5.7v4.6M12 10.3V8a2 2 0 0 0-2-2H7"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Small avatar circle for an assignee or PR author. */
function Avatar({ name }: { name: string }): JSX.Element {
  const initial = name.replace(/^\[H\]\s*/, "").charAt(0).toUpperCase() || "?";
  const color = getAvatarColor(name);
  return (
    <span
      className={styles.avatar}
      style={{ background: color, color: shouldUseWhiteText(color) ? "#fff" : "#111" }}
      title={name}
      aria-label={`Assignee ${name}`}
    >
      {initial}
    </span>
  );
}

export function PRsPage(): JSX.Element {
  const { issues } = useWorkspaceViewData();
  const { pullRequests, loading, error } = usePullRequests({ state: "all" });
  const [filter, setFilter] = useState<PRFilter>("all");
  const [groupMode, setGroupMode] = useState<GroupMode>("none");
  const [searchParams, setSearchParams] = useSearchParams();
  const reviewId = searchParams.get("review");

  const rows = useMemo((): PullRequestRow[] => {
    const issueByUrl = buildIssueByPrUrl(issues);
    return pullRequests.map((pr): PullRequestRow => {
      const issue = issueByUrl.get(normalizePrUrl(pr.url) ?? "");
      return issue ? { pr, issue } : { pr };
    });
  }, [pullRequests, issues]);

  const openCount = useMemo(
    () => rows.filter((r) => isOpenPr(r.pr)).length,
    [rows],
  );

  const counts = useMemo(() => {
    const c: Record<PRFilter, number> = { all: 0, review: 0, open: 0, merged: 0 };
    for (const row of rows) {
      for (const f of FILTERS) {
        if (matchesFilter(row, f.id)) c[f.id] += 1;
      }
    }
    return c;
  }, [rows]);

  const filtered = useMemo(
    () => rows.filter((r) => matchesFilter(r, filter)),
    [rows, filter],
  );

  const groups = useMemo(() => {
    if (groupMode === "none") return null;
    const map = new Map<string, PullRequestRow[]>();
    for (const row of filtered) {
      const key = groupKeyFor(row, groupMode);
      const bucket = map.get(key);
      if (bucket) bucket.push(row);
      else map.set(key, [row]);
    }
    return [...map.entries()];
  }, [filtered, groupMode]);

  const openReview = (row: PullRequestRow): void => {
    if (row.issue) {
      setSearchParams({ review: row.issue.id });
      return;
    }
    window.open(row.pr.url, "_blank", "noopener,noreferrer");
  };

  function renderRow(row: PullRequestRow): JSX.Element {
    const { pr, issue } = row;
    const state = prStateFromGithub(pr, issue);
    const showRepo =
      groupMode !== "repo" && Boolean(pr.repo_name || issue?.repo);
    const showEpic = groupMode !== "epic" && Boolean(issue?.parent_title);
    const avatarName = issue?.assignee || pr.author_login;
    const title = pr.title || issue?.title || "Untitled pull request";
    const handleKeyDown = (event: KeyboardEvent<HTMLLIElement>) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      openReview(row);
    };

    return (
      <li
        key={pr.url}
        className={styles.row}
        role="button"
        tabIndex={0}
        aria-label={`Review ${title}`}
        onClick={() => openReview(row)}
        onKeyDown={handleKeyDown}
      >
        <span className={styles.rowIcon} aria-hidden="true">
          <PRGlyph />
        </span>
        <div className={styles.rowMain}>
          <span className={styles.rowHead}>
            <code className={styles.key}>#{pr.number}</code>
            <span className={styles.status} data-pr-state={state.key}>
              {state.label}
            </span>
            {showRepo && (
              <span className={styles.repoChip}>
                {pr.repo_name || issue?.repo}
              </span>
            )}
            {showEpic && (
              <span className={styles.epicChip} title={issue?.parent_title ?? ""}>
                {issue?.parent_title}
              </span>
            )}
            {issue && (
              <span className={styles.ticketChip} title={issue.id}>
                {issue.id}
              </span>
            )}
          </span>
          <span className={styles.rowTitle}>{title}</span>
        </div>
        <div className={styles.rowRight}>
          {avatarName ? (
            <Avatar name={avatarName} />
          ) : (
            <span className={styles.avatarEmpty} aria-label="Unassigned" />
          )}
          <span className={styles.chevron} aria-hidden="true">
            ›
          </span>
        </div>
      </li>
    );
  }

  const reviewIssue = reviewId
    ? issues.find((i) => i.id === reviewId)
    : undefined;
  const reviewPr = reviewIssue
    ? rows.find((r) => r.issue?.id === reviewIssue.id)?.pr
    : undefined;

  if (reviewIssue) {
    return (
      <PRReviewWorkspace
        issue={reviewIssue}
        {...(reviewPr ? { pullRequest: reviewPr } : {})}
        onBack={() => setSearchParams({}, { replace: true })}
      />
    );
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>Pull Requests</h1>
      </header>
      <p className={styles.subtitle}>
        {loading && rows.length === 0 ? (
          <>Loading pull requests from GitHub…</>
        ) : error ? (
          <>Could not load pull requests: {error.message}</>
        ) : rows.length > 0 ? (
          <>
            <strong className={styles.subtitleCount}>{openCount} open</strong>
            {" · "}
            {counts.review} awaiting review
          </>
        ) : (
          <>Open pull requests from GitHub in this workspace.</>
        )}
      </p>

      {!loading && !error && rows.length === 0 ? (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>No pull requests</p>
          <p className={styles.emptyHint}>
            When an agent pushes a branch and opens a PR on GitHub, it appears
            here for review.
          </p>
        </div>
      ) : rows.length > 0 ? (
        <>
          <div className={styles.toolbar}>
            <div
              className={styles.filterPills}
              role="tablist"
              aria-label="Filter pull requests"
            >
              {FILTERS.filter((f) => f.id === "all" || counts[f.id] > 0).map(
                (f) => {
                  const isActive = filter === f.id;
                  return (
                    <button
                      key={f.id}
                      type="button"
                      role="tab"
                      aria-selected={isActive}
                      className={styles.pill}
                      data-active={isActive || undefined}
                      onClick={() => setFilter(f.id)}
                    >
                      {f.label}
                      <span className={styles.pillCount}>{counts[f.id]}</span>
                    </button>
                  );
                },
              )}
            </div>
            <div className={styles.groupControl}>
              <span className={styles.groupLabel}>Group</span>
              <div
                className={styles.segmented}
                role="group"
                aria-label="Group pull requests by"
              >
                {GROUPS.map((g) => {
                  const isActive = groupMode === g.id;
                  return (
                    <button
                      key={g.id}
                      type="button"
                      className={styles.segButton}
                      data-active={isActive || undefined}
                      aria-pressed={isActive}
                      onClick={() => setGroupMode(g.id)}
                    >
                      {g.label}
                    </button>
                  );
                })}
              </div>
            </div>
          </div>

          {filtered.length === 0 ? (
            <div className={styles.empty}>
              <p className={styles.emptyTitle}>Nothing here</p>
              <p className={styles.emptyHint}>
                No pull requests match this filter.
              </p>
            </div>
          ) : groups ? (
            <div className={styles.groups}>
              {groups.map(([key, groupRows]) => (
                <section key={key} className={styles.group}>
                  <header className={styles.groupHeader}>
                    <span className={styles.groupName}>{key}</span>
                    <span className={styles.groupCount}>{groupRows.length}</span>
                  </header>
                  <ul className={styles.list}>{groupRows.map(renderRow)}</ul>
                </section>
              ))}
            </div>
          ) : (
            <ul className={styles.list}>{filtered.map(renderRow)}</ul>
          )}
        </>
      ) : null}
    </div>
  );
}
