/**
 * PRsPage — Aether Wireframe V3 "Pull Requests" view.
 *
 * Loom has no GitHub PR-list API, but issues carry a real `external_ref` that
 * can be a PR URL (see isPRUrl / getReviewType). This view surfaces those
 * PR-bearing issues as the review queue: each row links out to the PR and
 * opens the issue detail (Git / Diff tabs) for review. Fully data-backed — no
 * stub; it's simply empty until an agent opens a PR.
 *
 * V3 adds two real, data-driven controls on top of the flat list:
 *   - Filter pills (All / Needs review / Open / Merged) mapped to loom's actual
 *     issue states (no fabricated draft/changes-requested states loom can't back).
 *   - Group by (None / Repo / Epic), grouping on issue.repo and parent epic.
 */
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";

import type { Issue } from "@/types";
import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { isPRUrl } from "@/utils/issue";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import { PRReviewWorkspace } from "./PRReviewWorkspace";
import styles from "./PRsPage.module.css";

/**
 * Review-queue filters, mapped to loom's real issue states:
 *   - review  → status === "review"          (awaiting review)
 *   - open    → has a PR URL, not yet in review and not merged/closed
 *   - merged  → has a PR URL and the issue is closed (PR landed)
 */
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

function matchesFilter(issue: Issue, filter: PRFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "review":
      return issue.status === "review";
    case "open":
      return (
        isPRUrl(issue.external_ref) &&
        issue.status !== "review" &&
        issue.status !== "closed"
      );
    case "merged":
      return isPRUrl(issue.external_ref) && issue.status === "closed";
    default:
      return true;
  }
}

function groupKeyFor(issue: Issue, mode: GroupMode): string {
  if (mode === "repo") return issue.repo || "No repo";
  if (mode === "epic") return issue.parent_title || "No epic";
  return "";
}

/** Extract the PR number from a validated PR URL (.../pull/142 → "142"). */
function prNumberFrom(ref: string | null | undefined): string | null {
  if (!isPRUrl(ref)) return null;
  return ref?.match(/\/pulls?\/(\d+)/)?.[1] ?? null;
}

/** Map loom's issue status to a PR review state (label + color key). */
function prState(issue: Issue): { label: string; key: string } {
  if (issue.status === "review") return { label: "Review", key: "review" };
  if (issue.status === "closed") return { label: "Merged", key: "merged" };
  return { label: "Open", key: "open" };
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

/** Small avatar circle for an assignee. */
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
  const [filter, setFilter] = useState<PRFilter>("all");
  const [groupMode, setGroupMode] = useState<GroupMode>("none");
  // Deep-linkable full-screen review: /prs?review=<issue id> (design's
  // review-ws — clicking a PR focuses its file diff).
  const [searchParams, setSearchParams] = useSearchParams();
  const reviewId = searchParams.get("review");

  // The PR / review queue: an agent's work awaiting merge. In loom that's an
  // issue in `review` status, and/or one carrying a real PR URL in external_ref.
  const prIssues = useMemo(
    () =>
      issues.filter((i) => i.status === "review" || isPRUrl(i.external_ref)),
    [issues],
  );

  // Live per-filter counts shown on each pill (always reflect totals, so the
  // numbers don't change as you switch filters).
  const counts = useMemo(() => {
    const c: Record<PRFilter, number> = { all: 0, review: 0, open: 0, merged: 0 };
    for (const issue of prIssues) {
      for (const f of FILTERS) {
        if (matchesFilter(issue, f.id)) c[f.id] += 1;
      }
    }
    return c;
  }, [prIssues]);

  const filtered = useMemo(
    () => prIssues.filter((i) => matchesFilter(i, filter)),
    [prIssues, filter],
  );

  // Insertion-ordered groups (Map preserves first-seen order) so headers appear
  // in a stable sequence.
  const groups = useMemo(() => {
    if (groupMode === "none") return null;
    const map = new Map<string, Issue[]>();
    for (const issue of filtered) {
      const key = groupKeyFor(issue, groupMode);
      const bucket = map.get(key);
      if (bucket) bucket.push(issue);
      else map.set(key, [issue]);
    }
    return [...map.entries()];
  }, [filtered, groupMode]);

  function renderRow(issue: Issue): JSX.Element {
    const pr = prNumberFrom(issue.external_ref);
    const state = prState(issue);
    // When grouped, the repo/epic is already in the group header — omit the
    // redundant chip from the row.
    const showRepo = groupMode !== "repo" && Boolean(issue.repo);
    const showEpic = groupMode !== "epic" && Boolean(issue.parent_title);
    return (
      <li key={issue.id} className={styles.row}>
        <span className={styles.rowIcon} aria-hidden="true">
          <PRGlyph />
        </span>
        <button
          type="button"
          className={styles.rowMain}
          onClick={() => setSearchParams({ review: issue.id })}
        >
          <span className={styles.rowHead}>
            <code className={styles.key}>{pr ? `#${pr}` : issue.id}</code>
            <span className={styles.status} data-pr-state={state.key}>
              {state.label}
            </span>
            {showRepo && <span className={styles.repoChip}>{issue.repo}</span>}
            {showEpic && (
              <span className={styles.epicChip} title={issue.parent_title ?? ""}>
                {issue.parent_title}
              </span>
            )}
          </span>
          <span className={styles.rowTitle}>{issue.title}</span>
        </button>
        <div className={styles.rowRight}>
          {issue.assignee ? (
            <Avatar name={issue.assignee} />
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

  // Full-screen review takeover (GitHub-PR-review style, diff focused).
  const reviewIssue = reviewId
    ? prIssues.find((i) => i.id === reviewId)
    : undefined;
  if (reviewIssue) {
    return (
      <PRReviewWorkspace
        issue={reviewIssue}
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
        {prIssues.length > 0 ? (
          <>
            <strong className={styles.subtitleCount}>
              {prIssues.length} open
            </strong>
            {" · "}
            {counts.review} awaiting review
          </>
        ) : (
          <>Open pull requests raised by agents in this workspace.</>
        )}
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
        <>
          <div className={styles.toolbar}>
            <div
              className={styles.filterPills}
              role="tablist"
              aria-label="Filter pull requests"
            >
              {/* Hide empty filters (design keeps only All + non-zero pills). */}
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
              {groups.map(([key, rows]) => (
                <section key={key} className={styles.group}>
                  <header className={styles.groupHeader}>
                    <span className={styles.groupName}>{key}</span>
                    <span className={styles.groupCount}>{rows.length}</span>
                  </header>
                  <ul className={styles.list}>{rows.map(renderRow)}</ul>
                </section>
              ))}
            </div>
          ) : (
            <ul className={styles.list}>{filtered.map(renderRow)}</ul>
          )}
        </>
      )}
    </div>
  );
}
