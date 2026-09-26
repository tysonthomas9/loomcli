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
import {
  useMemo,
  useState,
  type Dispatch,
  type KeyboardEvent,
  type SetStateAction,
} from "react";
import { useSearchParams } from "react-router-dom";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue } from "@/types";
import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { usePullRequests } from "@/hooks/workspace";
import { getReviewType, isPRUrl, prKeyFromRef } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import { PRReviewWorkspace } from "./PRReviewWorkspace";
import styles from "./PRsPage.module.css";

type PRFilter = "all" | "review" | "merged";
type GroupMode = "none" | "repo" | "epic";

const FILTERS: { id: PRFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "review", label: "Needs review" },
  { id: "merged", label: "Merged" },
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

/** Parsed `?review-pr=owner/repo#number` deep-link subject. */
export interface PullRequestSubject {
  owner: string;
  repo: string;
  number: number;
}

/** Parse `owner/repo#number` from the review-pr query param. */
export function parseReviewPrParam(
  value: string | null | undefined,
): PullRequestSubject | null {
  if (!value) return null;
  const match = /^([^/#\s]+)\/([^/#\s]+)#(\d+)$/.exec(value.trim());
  if (!match?.[1] || !match[2] || !match[3]) return null;
  const number = Number.parseInt(match[3], 10);
  if (!Number.isFinite(number) || number <= 0) return null;
  return { owner: match[1], repo: match[2], number };
}

/** Minimal GitHub PR row so the review workspace can mount before the list loads. */
export function stubPullRequestFromSubject(
  subject: PullRequestSubject,
): GitPullRequest {
  return {
    number: subject.number,
    title: `${subject.owner}/${subject.repo}#${subject.number}`,
    url: `https://github.com/${subject.owner}/${subject.repo}/pull/${subject.number}`,
    state: "OPEN",
    is_draft: false,
    head_ref_name: "",
    base_ref_name: "",
    repo_name: `${subject.owner}/${subject.repo}`,
  };
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
  const { issues } = useWorkspaceViewData();
  const { pullRequests, warnings, loading, error } = usePullRequests({
    state: "all",
  });
  const [filter, setFilter] = useState<PRFilter>("review");
  const groupMode: GroupMode = "none";
  const [query, setQuery] = useState("");
  const [railQuery, setRailQuery] = useState("");
  const [selectedRepos, setSelectedRepos] = useState<Set<string>>(new Set());
  const [selectedEpics, setSelectedEpics] = useState<Set<string>>(new Set());
  const [searchParams, setSearchParams] = useSearchParams();
  const reviewId = searchParams.get("review");
  const reviewPrParam = searchParams.get("review-pr");
  const discussOpen =
    searchParams.get("discuss") === "1" ||
    searchParams.get("discuss") === "true";

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
      merged: 0,
    };
    for (const row of rows) {
      for (const f of FILTERS) {
        if (matchesFilter(row, f.id)) c[f.id] += 1;
      }
    }
    return c;
  }, [rows]);

  const repoFor = (row: PullRequestRow): string =>
    row.pr?.source_repo || row.pr?.repo_name || row.issue?.repo || "No repo";
  const epicFor = (row: PullRequestRow): string =>
    row.issue?.parent_title || "No epic";

  const repoOptions = useMemo(() => {
    const countsByRepo = new Map<string, number>();
    for (const row of rows) {
      const repo = repoFor(row);
      countsByRepo.set(repo, (countsByRepo.get(repo) ?? 0) + 1);
    }
    return [...countsByRepo.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [rows]);

  const epicOptions = useMemo(() => {
    const countsByEpic = new Map<string, number>();
    for (const row of rows) {
      const epic = epicFor(row);
      countsByEpic.set(epic, (countsByEpic.get(epic) ?? 0) + 1);
    }
    return [...countsByEpic.entries()].sort(([a], [b]) => a.localeCompare(b));
  }, [rows]);

  const filtered = useMemo(() => {
    const normalizedQuery = query.trim().toLowerCase();
    return rows.filter((row) => {
      if (!matchesFilter(row, filter)) return false;
      const repo = repoFor(row);
      const epic = epicFor(row);
      if (selectedRepos.size > 0 && !selectedRepos.has(repo)) return false;
      if (selectedEpics.size > 0 && !selectedEpics.has(epic)) return false;
      if (!normalizedQuery) return true;
      const searchable = [
        row.pr?.title,
        row.pr?.number,
        row.issue?.title,
        row.issue?.id,
        repo,
        epic,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return searchable.includes(normalizedQuery);
    });
  }, [filter, query, rows, selectedEpics, selectedRepos]);

  const toggleSelection = (
    value: string,
    setSelection: Dispatch<SetStateAction<Set<string>>>,
  ): void => {
    setSelection((current) => {
      const next = new Set(current);
      if (next.has(value)) next.delete(value);
      else next.add(value);
      return next;
    });
  };

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

  const reviewPrSubject = useMemo(
    () => parseReviewPrParam(reviewPrParam),
    [reviewPrParam],
  );

  const reviewPrLinkedIssue = useMemo(() => {
    if (!reviewPrSubject) return undefined;
    const key =
      `${reviewPrSubject.owner}/${reviewPrSubject.repo}#${reviewPrSubject.number}`.toLowerCase();
    return issues.find((issue) => {
      const issueKey = prKeyFromRef(issue.external_ref);
      return issueKey != null && issueKey.toLowerCase() === key;
    });
  }, [issues, reviewPrSubject]);

  if (reviewIssue) {
    return (
      <PRReviewWorkspace
        key={`review-${reviewIssue.id}`}
        issue={reviewIssue}
        {...(reviewPr ? { pullRequest: reviewPr } : {})}
        initialDiscussOpen={discussOpen}
        onBack={() => setSearchParams({}, { replace: true })}
      />
    );
  }

  const reviewPrRow = reviewPrParam
    ? rows.find((r) => r.pr && prReviewRef(r.pr) === reviewPrParam)
    : undefined;

  if (reviewPrParam && (reviewPrRow?.pr || reviewPrSubject)) {
    // Prefer the list-backed PR when available (real title/state). Otherwise
    // mount immediately from the deep-link subject so kanban → review does not
    // flash the PR list while usePullRequests is still cold.
    const pullRequest =
      reviewPrRow?.pr ?? stubPullRequestFromSubject(reviewPrSubject!);
    const linkedIssue = reviewPrRow?.issue ?? reviewPrLinkedIssue;
    return (
      <PRReviewWorkspace
        key={`review-pr-${reviewPrParam}`}
        pullRequest={pullRequest}
        {...(linkedIssue ? { issue: linkedIssue } : {})}
        initialDiscussOpen={discussOpen}
        onBack={() => setSearchParams({}, { replace: true })}
        onLinkedTicket={(issueId) => setSearchParams({ review: issueId })}
      />
    );
  }

  if (reviewPrParam && loading) {
    return (
      <div className={styles.page} data-testid="pr-review-loading">
        <header className={styles.header}>
          <h1 className={styles.title}>Pull Requests</h1>
        </header>
        <p className={styles.subtitle}>Opening pull request…</p>
      </div>
    );
  }

  return (
    <div className={styles.page}>
      <header className={styles.header}>
        <h1 className={styles.title}>Pull Requests</h1>
        <p className={styles.subtitle}>
          {loading && rows.length === 0 ? (
            <>Loading pull requests…</>
          ) : rows.length > 0 ? (
            <>
              <strong className={styles.subtitleCount}>{openCount}</strong> open
              {" · "}
              <strong className={styles.subtitleCount}>
                {counts.review}
              </strong>{" "}
              awaiting review
            </>
          ) : (
            <>Review-stage tasks and GitHub pull requests in this workspace.</>
          )}
        </p>
      </header>

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
        <div className={styles.queueLayout}>
          <aside
            className={styles.filterRail}
            aria-label="Pull request filters"
          >
            <label className={styles.railSearch}>
              <span aria-hidden="true">⌕</span>
              <input
                type="search"
                value={railQuery}
                onChange={(event) => setRailQuery(event.target.value)}
                placeholder="Filter repos & epics…"
                aria-label="Filter repositories and epics"
              />
            </label>

            <section className={styles.railSection}>
              <h2 className={styles.railHeading}>View</h2>
              <div
                className={styles.filterPills}
                role="tablist"
                aria-label="Filter pull requests"
              >
                {FILTERS.map((f) => {
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
                })}
              </div>
            </section>

            <section className={styles.railSection}>
              <header className={styles.railSectionHead}>
                <h2 className={styles.railHeading}>Repos</h2>
                {selectedRepos.size > 0 && (
                  <button
                    type="button"
                    onClick={() => setSelectedRepos(new Set())}
                  >
                    Clear
                  </button>
                )}
              </header>
              <div className={styles.checkList}>
                {repoOptions
                  .filter(([repo]) =>
                    repo.toLowerCase().includes(railQuery.trim().toLowerCase()),
                  )
                  .map(([repo, count]) => (
                    <button
                      key={repo}
                      type="button"
                      className={styles.checkRow}
                      aria-pressed={selectedRepos.has(repo)}
                      onClick={() => toggleSelection(repo, setSelectedRepos)}
                    >
                      <span className={styles.checkbox} aria-hidden="true">
                        {selectedRepos.has(repo) ? "✓" : ""}
                      </span>
                      <span className={styles.checkLabel}>{repo}</span>
                      <span className={styles.checkCount}>{count}</span>
                    </button>
                  ))}
              </div>
            </section>

            <section className={styles.railSection}>
              <header className={styles.railSectionHead}>
                <h2 className={styles.railHeading}>Epics</h2>
                {selectedEpics.size > 0 && (
                  <button
                    type="button"
                    onClick={() => setSelectedEpics(new Set())}
                  >
                    Clear
                  </button>
                )}
              </header>
              <div className={styles.checkList}>
                {epicOptions
                  .filter(([epic]) =>
                    epic.toLowerCase().includes(railQuery.trim().toLowerCase()),
                  )
                  .map(([epic, count]) => (
                    <button
                      key={epic}
                      type="button"
                      className={styles.checkRow}
                      aria-pressed={selectedEpics.has(epic)}
                      onClick={() => toggleSelection(epic, setSelectedEpics)}
                    >
                      <span className={styles.checkbox} aria-hidden="true">
                        {selectedEpics.has(epic) ? "✓" : ""}
                      </span>
                      <span className={styles.checkLabel}>{epic}</span>
                      <span className={styles.checkCount}>{count}</span>
                    </button>
                  ))}
              </div>
            </section>
          </aside>

          <main className={styles.listPane}>
            <div className={styles.toolbar}>
              <label className={styles.listSearch}>
                <span aria-hidden="true">⌕</span>
                <input
                  type="search"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Search pull requests…"
                  aria-label="Search pull requests"
                />
              </label>
              <span className={styles.resultCount}>
                {filtered.length} result{filtered.length === 1 ? "" : "s"}
              </span>
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
                      <ul className={styles.list}>
                        {groupRows.map(renderRow)}
                      </ul>
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
          </main>
        </div>
      ) : null}
    </div>
  );
}
