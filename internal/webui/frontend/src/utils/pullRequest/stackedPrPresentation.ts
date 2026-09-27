/**
 * Presentation helpers for the stacked PR workspace (STACKED-PRS-83).
 *
 * Pure display derivations only. Every requirement line is derived from the
 * readiness snapshot and is never shown as met unless the evidence is fresh,
 * so stale / unknown / unavailable observations stay explicit.
 */

import type {
  GitPullRequest,
  PullRequestReadinessView,
} from "@/api/workspace/pullRequests";
import type { ReadinessDisplayKey } from "./readinessDisplay";

/** Badge / node tone used by the reference palette. */
export type ReadinessTone =
  | "ready"
  | "merged"
  | "review"
  | "blocked"
  | "draft"
  | "unknown";

export function toneForKey(key: ReadinessDisplayKey): ReadinessTone {
  switch (key) {
    case "ready":
      return "ready";
    case "merged":
      return "merged";
    case "waiting":
    case "queued":
      return "review";
    case "blocked":
      return "blocked";
    case "draft":
    case "closed":
      return "draft";
    default:
      return "unknown";
  }
}

/** Two-letter avatar initials from a GitHub login or Loom actor. */
export function initialsFor(name: string | null | undefined): string {
  const raw = (name ?? "").trim().replace(/^@/, "");
  if (!raw) return "?";
  const local = raw.includes("@") ? raw.split("@")[0]! : raw;
  const parts = local.split(/[\s._-]+/).filter(Boolean);
  if (parts.length >= 2) {
    return `${parts[0]![0]}${parts[1]![0]}`.toUpperCase();
  }
  return local.slice(0, 2).toUpperCase();
}

/** Compact relative age ("12m ago") from an ISO timestamp. */
export function relativeAge(
  iso: string | null | undefined,
  now: number = Date.now(),
): string | null {
  if (!iso) return null;
  const at = Date.parse(iso);
  if (!Number.isFinite(at)) return null;
  const s = Math.max(0, Math.floor((now - at) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

/** Basename for compact repo chips (`acme/loomcli` → `loomcli`). */
export function repoBasename(repo: string | null | undefined): string {
  const raw = (repo ?? "").trim();
  if (!raw) return "No repo";
  const parts = raw.split("/");
  return parts[parts.length - 1] || raw;
}

export type RequirementState = "met" | "pending" | "failing" | "unknown";

export interface RequirementLine {
  id: "review" | "checks" | "conflicts";
  label: string;
  state: RequirementState;
  detail?: string;
}

/**
 * Merge requirement lines from readiness facts. Non-fresh evidence is never
 * reported as met — it downgrades to "unknown" with a "last observed" note.
 */
export function requirementLines(
  view: PullRequestReadinessView | null | undefined,
): RequirementLine[] {
  const facts = view?.snapshot?.facts;
  if (!view || !facts) {
    return [
      {
        id: "review",
        label: "Review status not observed",
        state: "unknown",
      },
      {
        id: "checks",
        label: "Required checks not observed",
        state: "unknown",
      },
      {
        id: "conflicts",
        label: "Conflict status not observed",
        state: "unknown",
      },
    ];
  }
  const current = view.freshness === "fresh";
  const downgrade = (line: RequirementLine): RequirementLine =>
    current || line.state !== "met"
      ? line
      : { ...line, state: "unknown", detail: "last observed · not current" };

  const review: RequirementLine = (() => {
    const f = facts.review;
    if (f.status !== "known") {
      return { id: "review", label: "Review status unknown", state: "unknown" };
    }
    switch (f.value) {
      case "approved":
        return { id: "review", label: "Approved", state: "met" };
      case "changes_requested":
        return { id: "review", label: "Changes requested", state: "failing" };
      case "review_required":
        return { id: "review", label: "Review required", state: "pending" };
      // GitHub's reviewDecision was null: the repo may require no review, or
      // GitHub simply did not say (internal/prreadiness/types.go). Neutral,
      // never "met" — the backend verdict and its no_review_required
      // warning stay on the readiness badge untouched.
      case "not_reported":
        return {
          id: "review",
          label: "No review decision reported",
          state: "unknown",
          detail: "GitHub did not say",
        };
      default:
        return {
          id: "review",
          label: "Review status unknown",
          state: "unknown",
        };
    }
  })();

  const checks: RequirementLine = (() => {
    const f = facts.required_checks;
    const c = facts.required_check_counts;
    if (f.status !== "known") {
      return {
        id: "checks",
        label: "Required checks unknown",
        state: "unknown",
      };
    }
    const counts = c && c.total > 0 ? `${c.passed}/${c.total}` : undefined;
    switch (f.value) {
      case "passing":
        return {
          id: "checks",
          label: counts ? `All ${c.total} checks passing` : "Checks passing",
          state: "met",
          detail: "required",
        };
      case "pending":
        return {
          id: "checks",
          label: counts ? `${counts} checks passing` : "Checks pending",
          state: "pending",
          detail: `${c?.pending ?? 0} pending`,
        };
      case "failing":
        return {
          id: "checks",
          label: counts
            ? `${c.failed} of ${c.total} checks failing`
            : "Checks failing",
          state: "failing",
          ...(c?.failing_names?.length
            ? { detail: c.failing_names.slice(0, 2).join(", ") }
            : {}),
        };
      case "none_required":
      case "none":
        return {
          id: "checks",
          label: "No required checks",
          state: "met",
        };
      default:
        return {
          id: "checks",
          label: "Required checks unknown",
          state: "unknown",
        };
    }
  })();

  const conflicts: RequirementLine = (() => {
    const f = facts.conflicts;
    if (f.status !== "known") {
      return {
        id: "conflicts",
        label: "Conflict status unknown",
        state: "unknown",
      };
    }
    if (f.value === "conflicting") {
      return {
        id: "conflicts",
        label: "Merge conflicts",
        state: "failing",
        detail: "base branch",
      };
    }
    if (f.value === "none") {
      return {
        id: "conflicts",
        label: "No merge conflicts",
        state: "met",
        detail: "base branch",
      };
    }
    return {
      id: "conflicts",
      label: "Conflict status unknown",
      state: "unknown",
    };
  })();

  return [review, checks, conflicts].map(downgrade);
}

/** Required-check pass count for compact row display, when known. */
export function checkCountLabel(
  view: PullRequestReadinessView | null | undefined,
): string | null {
  const c = view?.snapshot?.facts?.required_check_counts;
  if (!c || c.total <= 0) return null;
  return `${c.passed}/${c.total}`;
}

/** File-change summary text, or null when GitHub did not report stats. */
export function changeSummary(pr: GitPullRequest | null | undefined): {
  files: string;
  additions: number | null;
  deletions: number | null;
} | null {
  if (!pr) return null;
  if (
    pr.changed_files == null &&
    pr.additions == null &&
    pr.deletions == null
  ) {
    return null;
  }
  return {
    files:
      pr.changed_files != null
        ? `${pr.changed_files} file${pr.changed_files === 1 ? "" : "s"}`
        : "files unknown",
    additions: pr.additions ?? null,
    deletions: pr.deletions ?? null,
  };
}
