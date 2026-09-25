/**
 * PRsPage — stacked PR workspace with preserved review deep links.
 *
 * Queue UI lives in StackedPRWorkspace. Linked (`?review=`) and unlinked
 * (`?review-pr=`) paths still mount PRReviewWorkspace unchanged.
 */
import { useMemo } from "react";
import { useSearchParams } from "react-router-dom";

import type { GitPullRequest } from "@/api/workspace";
import type { Issue } from "@/types";
import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { usePullRequests } from "@/hooks/workspace";
import {
  formatPrKey,
  getReviewType,
  isPRUrl,
  prKeyFromRef,
  pullRequestKey,
} from "@/utils/issue";
import { StackedPRWorkspace } from "@/components/StackedPRWorkspace";

import { PRReviewWorkspace } from "./PRReviewWorkspace";
import styles from "./PRsPage.module.css";

export interface PullRequestRow {
  /** Loom issue backing the row — primary source when present. */
  issue?: Issue | undefined;
  /** GitHub metadata enrichment; rows without it are loom-only. */
  pr?: GitPullRequest | undefined;
}

type GroupMode = "none" | "repo" | "epic";

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

function prKeyField(key: string | null): { pr_key?: string } {
  return key ? { pr_key: key } : {};
}

/** Minimal GitHub PR row so the review workspace can mount before the list loads. */
export function stubPullRequestFromSubject(
  subject: PullRequestSubject,
): GitPullRequest {
  return {
    number: subject.number,
    ...prKeyField(formatPrKey(subject.owner, subject.repo, subject.number)),
    title: `${subject.owner}/${subject.repo}#${subject.number}`,
    url: `https://github.com/${subject.owner}/${subject.repo}/pull/${subject.number}`,
    state: "OPEN",
    is_draft: false,
    head_ref_name: "",
    base_ref_name: "",
    repo_name: `${subject.owner}/${subject.repo}`,
  };
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
    const key = pullRequestKey(pr);
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
    const key = pullRequestKey(pr);
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

export function PRsPage(): JSX.Element {
  const { issues } = useWorkspaceViewData();
  const {
    pullRequests,
    warnings,
    deliveryGroups,
    deliveryGroupsHasMore,
    standaloneContinuation,
    loading,
    error,
    refetch,
  } = usePullRequests({
    state: "all",
  });
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
    const key = formatPrKey(
      reviewPrSubject.owner,
      reviewPrSubject.repo,
      reviewPrSubject.number,
    );
    if (!key) return undefined;
    return issues.find((issue) => prKeyFromRef(issue.external_ref) === key);
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
    <StackedPRWorkspace
      issues={issues}
      pullRequests={pullRequests}
      deliveryGroups={deliveryGroups}
      deliveryGroupsHasMore={deliveryGroupsHasMore}
      {...(standaloneContinuation ? { standaloneContinuation } : {})}
      warnings={warnings}
      loading={loading}
      error={error}
      onRefetch={refetch}
      onOpenReview={({ issueId, reviewPr }) => {
        if (issueId) {
          setSearchParams({ review: issueId });
          return;
        }
        if (reviewPr) {
          setSearchParams({ "review-pr": reviewPr });
        }
      }}
    />
  );
}
