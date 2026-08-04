/**
 * PRsPage — Loom-first review queue with GitHub enrichment.
 *
 * Loom issues are the primary row source: every issue in review (or carrying
 * a PR external_ref) appears even when gh is unavailable, so the queue keeps
 * working offline. GitHub metadata (gh pr list) enriches rows — title, draft/
 * merged state, review decision, author — joined by the stable owner/repo#n
 * key. GitHub PRs with no linked issue render as unlinked rows that open
 * externally, and gh failures degrade to a warning banner, never a blank page.
 */
import { useMemo, useState, type KeyboardEvent } from "react";
import { useSearchParams } from "react-router-dom";

import type { Issue } from "@/types";
import { getReviewType, isPRUrl, prKeyFromRef } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import type { GitPullRequest } from "./api/pullRequests";
import { useSourceControlContext } from "./context";
import { PRReviewWorkspace } from "./PRReviewWorkspace";
import { usePullRequests } from "./usePullRequests";
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
  /** Loom issue backing the row — primary source when present. */
  issue?: Issue | undefined;
  /** GitHub metadata enrichment; rows without it are loom-only. */
  pr?: GitPullRequest | undefined;
}

function isOpenPr(pr: GitPullRequest): boolean {
  return pr.state === "OPEN" && !pr.is_draft;
}

function needsReview(row: PullRequestRow): boolean {
  if (row.pr) {
    return isOpenPr(row.pr) && row.pr.review_decision !== "APPROVED";
  }
  // Loom-only rows are in the queue exactly when the task awaits review
  // (e.g. a plan review with no PR yet).
  return row.issue?.status === "review";
}

function isOpenRow(row: PullRequestRow): boolean {
  if (row.pr) return isOpenPr(row.pr);
  return row.issue?.status === "review";
}

function matchesFilter(row: PullRequestRow, filter: PRFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "review":
      return needsReview(row);
    case "open":
      return isOpenRow(row);
    case "merged":
      return row.pr?.state === "MERGED";
    default:
      return true;
  }
}

export function groupKeyFor(row: PullRequestRow, mode: GroupMode): string {
  if (mode === "repo") {
    if (row.pr) {
      return (
        row.pr.source_repo || row.pr.repo_name || row.issue?.repo || "No repo"
      );
    }
    return row.issue?.repo || "No repo";
  }
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

/** Display state for any row, with or without GitHub metadata. */
export function rowState(row: PullRequestRow): { label: string; key: string } {
  if (row.pr) return prStateFromGithub(row.pr, row.issue);
  const issue = row.issue;
  if (issue && getReviewType(issue) === "plan") {
    return { label: "Plan review", key: "review" };
  }
  return { label: "Review", key: "review" };
}

export function prReviewRef(pr: GitPullRequest): string | null {
  return pr.repo_name && pr.number ? `${pr.repo_name}#${pr.number}` : null;
}

/**
 * Build the review queue: loom issues first (status=review or PR-linked),
 * enriched with GitHub metadata by owner/repo#number; then unlinked GitHub
 * PRs. Sorted by most recent update.
 */
export function buildPullRequestRows(
  issues: Issue[],
  pullRequests: GitPullRequest[],
): PullRequestRow[] {
  const prByKey = new Map<string, GitPullRequest>();
  for (const pr of pullRequests) {
    const key = prKeyFromRef(pr.url);
    if (key && !prByKey.has(key)) prByKey.set(key, pr);
  }

  const linkedKeys = new Set<string>();
  const rows: PullRequestRow[] = [];
  for (const issue of issues) {
    const inQueue = issue.status === "review" || isPRUrl(issue.external_ref);
    if (!inQueue) continue;
    const key = prKeyFromRef(issue.external_ref);
    const pr = key ? prByKey.get(key) : undefined;
    if (key && pr) linkedKeys.add(key);
    rows.push(pr ? { issue, pr } : { issue });
  }

  const emittedKeys = new Set(linkedKeys);
  for (const pr of pullRequests) {
    const key = prKeyFromRef(pr.url);
    if (key && emittedKeys.has(key)) continue;
    if (key) emittedKeys.add(key);
    rows.push({ pr });
  }

  return rows.sort((a, b) => {
    const aTime = a.pr?.updated_at ?? a.issue?.updated_at ?? "";
    const bTime = b.pr?.updated_at ?? b.issue?.updated_at ?? "";
    return bTime.localeCompare(aTime);
  });
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
  const initial =
    name
      .replace(/^\[H\]\s*/, "")
      .charAt(0)
      .toUpperCase() || "?";
  const color = getAvatarColor(name);
  return (
    <span
      className={styles.avatar}
      style={{
        background: color,
        color: shouldUseWhiteText(color) ? "#fff" : "#111",
      }}
      title={name}
      aria-label={`Assignee ${name}`}
    >
      {initial}
    </span>
  );
}

export function PRsPage(): JSX.Element {
  const { issues, workspaceId } = useSourceControlContext();
  const { pullRequests, warnings, loading, error } = usePullRequests({
    workspaceId,
    state: "all",
  });
  const [filter, setFilter] = useState<PRFilter>("all");
  const [groupMode, setGroupMode] = useState<GroupMode>("none");
  const [searchParams, setSearchParams] = useSearchParams();
  const reviewId = searchParams.get("review");
  const reviewPrParam = searchParams.get("review-pr");

  const rows = useMemo(
    () => buildPullRequestRows(issues, pullRequests),
    [issues, pullRequests],
  );

  // GitHub metadata is an enrichment: a fetch error or a warning (e.g. the
  // connector fell back to local gh, or a per-repo issue) degrades to a banner
  // while loom-backed rows keep rendering. Warnings are already self-describing.
  const githubWarning = error
    ? `GitHub metadata unavailable: ${error.message}`
    : warnings.length > 0
      ? `${warnings[0]}${warnings.length > 1 ? ` (+${warnings.length - 1} more)` : ""}`
      : null;

  const openCount = useMemo(() => rows.filter(isOpenRow).length, [rows]);

  const counts = useMemo(() => {
    const c: Record<PRFilter, number> = {
      all: 0,
      review: 0,
      open: 0,
      merged: 0,
    };
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
    if (row.pr) {
      const ref = prReviewRef(row.pr);
      if (ref) {
        setSearchParams({ "review-pr": ref });
        return;
      }
      window.open(row.pr.url, "_blank", "noopener,noreferrer");
    }
  };

  function renderRow(row: PullRequestRow): JSX.Element {
    const { pr, issue } = row;
    const state = rowState(row);
    const showRepo =
      groupMode !== "repo" && Boolean(pr?.repo_name || issue?.repo);
    const showEpic = groupMode !== "epic" && Boolean(issue?.parent_title);
    const avatarName = issue?.assignee || pr?.author_login;
    const title = pr?.title || issue?.title || "Untitled pull request";
    const handleKeyDown = (event: KeyboardEvent<HTMLLIElement>) => {
      if (event.key !== "Enter" && event.key !== " ") return;
      event.preventDefault();
      openReview(row);
    };

    return (
      <li
        key={pr?.url ?? issue?.id}
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
            {pr && <code className={styles.key}>#{pr.number}</code>}
            <span className={styles.status} data-pr-state={state.key}>
              {state.label}
            </span>
            {showRepo && (
              <span className={styles.repoChip}>
                {pr?.repo_name || issue?.repo}
              </span>
            )}
            {showEpic && (
              <span
                className={styles.epicChip}
                title={issue?.parent_title ?? ""}
              >
                {issue?.parent_title}
              </span>
            )}
            {issue && (
              <span className={styles.ticketChip} title={issue.id}>
                {issue.id}
              </span>
            )}
            {!issue && pr && (
              <span className={styles.ticketChip} title="No linked loom issue">
                Unlinked
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

  const reviewPrRow = reviewPrParam
    ? rows.find((r) => r.pr && prReviewRef(r.pr) === reviewPrParam)
    : undefined;

  if (reviewPrParam && reviewPrRow?.pr) {
    // If a hand-edited/bookmarked ?review-pr points at a PR that DOES have a
    // linked ticket, render it issue-linked so we don't offer "Create ticket"
    // on an already-ticketed PR (which would make a duplicate).
    return (
      <PRReviewWorkspace
        pullRequest={reviewPrRow.pr}
        {...(reviewPrRow.issue ? { issue: reviewPrRow.issue } : {})}
        onBack={() => setSearchParams({}, { replace: true })}
        onLinkedTicket={(issueId) => setSearchParams({ review: issueId })}
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
          <>Loading pull requests…</>
        ) : rows.length > 0 ? (
          <>
            <strong className={styles.subtitleCount}>{openCount} open</strong>
            {" · "}
            {counts.review} awaiting review
          </>
        ) : (
          <>Review-stage tasks and GitHub pull requests in this workspace.</>
        )}
      </p>

      {githubWarning && (
        <p
          className={styles.githubWarning}
          role="status"
          data-testid="prs-github-warning"
        >
          {githubWarning}
        </p>
      )}

      {!loading && rows.length === 0 ? (
        <div className={styles.empty}>
          <p className={styles.emptyTitle}>No pull requests</p>
          <p className={styles.emptyHint}>
            When a task moves to review or an agent opens a PR on GitHub, it
            appears here for review.
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
            <div
              className={styles.scrollRegion}
              role="region"
              aria-label="Pull request list"
            >
              <div className={styles.groups}>
                {groups.map(([key, groupRows]) => (
                  <section key={key} className={styles.group}>
                    <header className={styles.groupHeader}>
                      <span className={styles.groupName}>{key}</span>
                      <span className={styles.groupCount}>
                        {groupRows.length}
                      </span>
                    </header>
                    <ul className={styles.list}>{groupRows.map(renderRow)}</ul>
                  </section>
                ))}
              </div>
            </div>
          ) : (
            <div
              className={styles.scrollRegion}
              role="region"
              aria-label="Pull request list"
            >
              <ul className={styles.list}>{filtered.map(renderRow)}</ul>
            </div>
          )}
        </>
      ) : null}
    </div>
  );
}
