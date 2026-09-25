/**
 * Pure model for the stacked PR workspace queue: filtering, counts, and
 * repo-filter dimming that keeps cross-repo delivery context visible.
 */

import type { GitPullRequest } from "@/api/workspace/pullRequests";
import type { DeliveryGroupView } from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";
import { pullRequestKey } from "@/utils/issue";
import {
  readinessDisplay,
  type ReadinessDisplayKey,
} from "@/utils/pullRequest/readinessDisplay";

export type QueueTab = "all" | "review" | "ready" | "attention" | "merged";
export type QueueKind = "group" | "standalone";
export type QueueViewMode = "path" | "list";
export type QueueMode = "queue" | "history";

export interface StandalonePRItem {
  kind: "standalone";
  prKey: string;
  pr: GitPullRequest;
  issueId?: string;
  assignee?: string;
  epicId?: string;
  epicTitle?: string;
  readiness?: PullRequestReadinessView;
  /** True when membership completeness is unverified — not confirmed standalone. */
  membershipUnverified?: boolean;
}

export interface DeliveryGroupItem {
  kind: "group";
  group: DeliveryGroupView;
}

export type WorkspaceItem = StandalonePRItem | DeliveryGroupItem;

export interface StackedPRFilters {
  tab: QueueTab;
  query: string;
  repos: ReadonlySet<string>;
  epics: ReadonlySet<string>;
  kinds: ReadonlySet<QueueKind>;
  /** When true, only items tied to mineIdentity. */
  mine: boolean;
  mineIdentity?: string | null;
}

export interface FilterMatch {
  matches: boolean;
  /** Member is outside the selected repo filter but kept for dependency context. */
  dimmed: boolean;
}

function repoNameOf(pr: GitPullRequest): string {
  return pr.source_repo || pr.repo_name || "No repo";
}

function memberRepoName(repoName: string | undefined): string {
  const raw = repoName || "";
  if (raw.includes("/")) {
    const parts = raw.split("/");
    return parts[parts.length - 1] || raw;
  }
  return raw || "No repo";
}

function memberRepo(member: { repo_name?: string }): string {
  return memberRepoName(member.repo_name);
}

export function statusKeyForItem(
  item: WorkspaceItem,
  readinessByKey: ReadonlyMap<string, PullRequestReadinessView>,
): ReadinessDisplayKey {
  if (item.kind === "standalone") {
    const view = item.readiness ?? readinessByKey.get(item.prKey);
    if (item.pr.state === "MERGED") return "merged";
    if (item.pr.is_draft) return "draft";
    return readinessDisplay(view).key;
  }
  // Group status = worst non-merged member by readiness.
  let worst: ReadinessDisplayKey = "ready";
  const rank: Record<ReadinessDisplayKey, number> = {
    ready: 0,
    queued: 1,
    waiting: 2,
    draft: 3,
    unknown: 4,
    stale: 5,
    blocked: 6,
    closed: 7,
    merged: -1,
  };
  for (const m of item.group.members) {
    const view = m.readiness ?? readinessByKey.get(m.pr_key);
    const key = readinessDisplay(view).key;
    if (key === "merged") continue;
    if ((rank[key] ?? 0) > (rank[worst] ?? 0)) worst = key;
  }
  return worst;
}

function tabMatches(
  tab: QueueTab,
  statusKey: ReadinessDisplayKey,
): boolean {
  switch (tab) {
    case "all":
      return true;
    case "review":
      return (
        statusKey === "waiting" ||
        statusKey === "draft" ||
        statusKey === "queued"
      );
    case "ready":
      return statusKey === "ready";
    case "attention":
      return (
        statusKey === "blocked" ||
        statusKey === "stale" ||
        statusKey === "unknown"
      );
    case "merged":
      return statusKey === "merged";
    default:
      return true;
  }
}

function queryMatches(haystack: string, query: string): boolean {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return true;
  const hay = haystack.toLowerCase();
  return words.every((w) => hay.includes(w));
}

function identityMatches(
  identity: string | null | undefined,
  ...candidates: Array<string | null | undefined>
): boolean {
  if (!identity) return false;
  const want = identity.trim().toLowerCase();
  if (!want) return false;
  return candidates.some((c) => (c ?? "").trim().toLowerCase() === want);
}

export function matchStandalone(
  item: StandalonePRItem,
  filters: StackedPRFilters,
  readinessByKey: ReadonlyMap<string, PullRequestReadinessView>,
): FilterMatch {
  if (!filters.kinds.has("standalone")) {
    return { matches: false, dimmed: false };
  }

  const repo = repoNameOf(item.pr);
  const epic = item.epicId || item.epicTitle || "none";
  const statusKey = statusKeyForItem(item, readinessByKey);

  if (filters.epics.size > 0 && !filters.epics.has(epic) && !filters.epics.has(item.epicTitle ?? "")) {
    // Also allow matching by epic title when options use titles.
    const epicOk =
      [...filters.epics].some(
        (e) =>
          e === epic ||
          e === item.epicTitle ||
          e === item.epicId ||
          (e === "none" && !item.epicId && !item.epicTitle),
      );
    if (!epicOk) return { matches: false, dimmed: false };
  }

  if (filters.mine) {
    if (
      !identityMatches(
        filters.mineIdentity,
        item.pr.author_login,
        item.assignee,
      )
    ) {
      return { matches: false, dimmed: false };
    }
  }

  if (!tabMatches(filters.tab, statusKey)) {
    return { matches: false, dimmed: false };
  }

  const hay = [
    item.prKey,
    item.pr.title,
    repo,
    item.pr.head_ref_name,
    item.pr.base_ref_name,
    item.pr.author_login,
    item.issueId,
    item.epicTitle,
    item.membershipUnverified ? "unverified membership" : "",
  ]
    .filter(Boolean)
    .join(" ");
  if (!queryMatches(hay, filters.query)) {
    return { matches: false, dimmed: false };
  }

  const dimmed =
    filters.repos.size > 0 && !filters.repos.has(repo) && !filters.repos.has(item.pr.repo_name ?? "");
  if (filters.repos.size > 0 && dimmed) {
    // Standalone outside selected repos is hidden (no dependency context).
    return { matches: false, dimmed: false };
  }

  return { matches: true, dimmed: false };
}

export function matchDeliveryGroup(
  item: DeliveryGroupItem,
  filters: StackedPRFilters,
  readinessByKey: ReadonlyMap<string, PullRequestReadinessView>,
  /** Optional PR metadata keyed by pr_key for search/author. */
  prByKey: ReadonlyMap<string, GitPullRequest> = new Map(),
): FilterMatch & { memberDimmed: ReadonlyMap<string, boolean> } {
  const memberDimmed = new Map<string, boolean>();
  if (!filters.kinds.has("group")) {
    return { matches: false, dimmed: false, memberDimmed };
  }

  const g = item.group;
  if (filters.epics.size > 0) {
    const epic = g.epic_id || "none";
    if (![...filters.epics].some((e) => e === epic || e === "none" && !g.epic_id)) {
      return { matches: false, dimmed: false, memberDimmed };
    }
  }

  if (filters.mine) {
    const authors = g.members.map((m) => prByKey.get(m.pr_key)?.author_login);
    const mineOk =
      identityMatches(filters.mineIdentity, g.owner, g.created_by) ||
      authors.some((a) => identityMatches(filters.mineIdentity, a));
    if (!mineOk) return { matches: false, dimmed: false, memberDimmed };
  }

  const statusKey = statusKeyForItem(item, readinessByKey);
  if (!tabMatches(filters.tab, statusKey)) {
    return { matches: false, dimmed: false, memberDimmed };
  }

  const hayParts = [
    g.title,
    g.id,
    g.epic_id,
    g.owner,
    ...g.members.map((m) => {
      const pr = prByKey.get(m.pr_key);
      return [m.pr_key, m.repo_name, String(m.pr_number), pr?.title, pr?.author_login]
        .filter(Boolean)
        .join(" ");
    }),
  ];
  if (!queryMatches(hayParts.join(" "), filters.query)) {
    return { matches: false, dimmed: false, memberDimmed };
  }

  let anyRepoMatch = filters.repos.size === 0;
  for (const m of g.members) {
    const repo = memberRepo(m);
    const inFilter =
      filters.repos.size === 0 ||
      filters.repos.has(repo) ||
      filters.repos.has(m.repo_name);
    memberDimmed.set(m.pr_key, filters.repos.size > 0 && !inFilter);
    if (inFilter) anyRepoMatch = true;
  }

  // Keep the whole group when any member matches the repo filter so dependency
  // context stays visible; dim non-matching members.
  if (filters.repos.size > 0 && !anyRepoMatch) {
    return { matches: false, dimmed: false, memberDimmed };
  }

  return {
    matches: true,
    dimmed: false,
    memberDimmed,
  };
}

export interface TabCounts {
  all: number;
  review: number;
  ready: number;
  attention: number;
  merged: number;
}

/**
 * Counts for tab pills. Uses the same filter axes as the list except the tab
 * itself, so counts stay internally consistent with visible results.
 */
export function computeTabCounts(
  items: readonly WorkspaceItem[],
  filters: Omit<StackedPRFilters, "tab">,
  readinessByKey: ReadonlyMap<string, PullRequestReadinessView>,
  prByKey: ReadonlyMap<string, GitPullRequest> = new Map(),
): TabCounts {
  const counts: TabCounts = {
    all: 0,
    review: 0,
    ready: 0,
    attention: 0,
    merged: 0,
  };
  const tabs: QueueTab[] = ["all", "review", "ready", "attention", "merged"];
  for (const item of items) {
    for (const tab of tabs) {
      const f: StackedPRFilters = { ...filters, tab };
      const match =
        item.kind === "standalone"
          ? matchStandalone(item, f, readinessByKey)
          : matchDeliveryGroup(item, f, readinessByKey, prByKey);
      if (match.matches) counts[tab] += 1;
    }
  }
  return counts;
}

export function buildPrByKey(
  pullRequests: readonly GitPullRequest[],
): Map<string, GitPullRequest> {
  const map = new Map<string, GitPullRequest>();
  for (const pr of pullRequests) {
    const key = pullRequestKey(pr);
    if (key && !map.has(key)) map.set(key, pr);
  }
  return map;
}

export function repoOptionsFromItems(
  items: readonly WorkspaceItem[],
  prByKey: ReadonlyMap<string, GitPullRequest>,
): Array<[string, number]> {
  const counts = new Map<string, number>();
  const bump = (repo: string) =>
    counts.set(repo, (counts.get(repo) ?? 0) + 1);

  for (const item of items) {
    if (item.kind === "standalone") {
      bump(repoNameOf(item.pr));
    } else {
      for (const m of item.group.members) {
        bump(memberRepo(m));
        void prByKey;
      }
    }
  }
  return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b));
}

export interface IssueLinkMeta {
  id: string;
  title?: string;
  status?: string;
  assignee?: string;
  epicId?: string;
  epicTitle?: string;
  prKey?: string | null;
  /** True when the issue is in the review queue even without a PR. */
  inReviewQueue?: boolean;
}

/**
 * Build workspace items: active delivery groups first, then standalone PRs
 * (facade already excludes active-group members). When membership is
 * incomplete, mark standalone rows as unverified — never confirmed standalone.
 */
export function buildWorkspaceItems(input: {
  deliveryGroups: readonly DeliveryGroupView[];
  pullRequests: readonly GitPullRequest[];
  issueByPrKey?: ReadonlyMap<string, IssueLinkMeta>;
  membershipComplete?: boolean;
  readinessByKey?: ReadonlyMap<string, PullRequestReadinessView>;
}): WorkspaceItem[] {
  const {
    deliveryGroups,
    pullRequests,
    issueByPrKey = new Map(),
    membershipComplete = true,
    readinessByKey = new Map(),
  } = input;

  const items: WorkspaceItem[] = [];
  for (const group of deliveryGroups) {
    if (group.state === "archived") continue;
    items.push({ kind: "group", group });
  }

  for (const pr of pullRequests) {
    const prKey = pullRequestKey(pr);
    if (!prKey) continue;
    const link = issueByPrKey.get(prKey);
    items.push({
      kind: "standalone",
      prKey,
      pr,
      ...(link?.id ? { issueId: link.id } : {}),
      ...(link?.assignee ? { assignee: link.assignee } : {}),
      ...(link?.epicId ? { epicId: link.epicId } : {}),
      ...(link?.epicTitle ? { epicTitle: link.epicTitle } : {}),
      ...(readinessByKey.get(prKey)
        ? { readiness: readinessByKey.get(prKey) }
        : {}),
      ...(membershipComplete ? {} : { membershipUnverified: true }),
    });
  }
  return items;
}

/**
 * Loom review-stage issues without a GitHub PR (e.g. plan review) stay visible
 * on the workspace as synthetic standalone rows so the queue does not go blank
 * when gh is unavailable.
 */
export function buildLoomOnlyReviewItems(
  issues: readonly IssueLinkMeta[],
  coveredPrKeys: ReadonlySet<string>,
): StandalonePRItem[] {
  const out: StandalonePRItem[] = [];
  for (const issue of issues) {
    if (!issue.inReviewQueue) continue;
    if (issue.prKey && coveredPrKeys.has(issue.prKey)) continue;
    const stubKey = issue.prKey || `loom:${issue.id}`;
    out.push({
      kind: "standalone",
      prKey: stubKey,
      issueId: issue.id,
      ...(issue.assignee ? { assignee: issue.assignee } : {}),
      ...(issue.epicId ? { epicId: issue.epicId } : {}),
      ...(issue.epicTitle ? { epicTitle: issue.epicTitle } : {}),
      pr: {
        number: 0,
        pr_key: stubKey,
        title: issue.title || issue.id,
        url: "",
        state: "OPEN",
        is_draft: false,
        head_ref_name: "",
        base_ref_name: "",
        repo_name: "",
        source_repo: "loom",
      },
    });
  }
  return out;
}

export function buildReadinessByKey(
  groups: readonly DeliveryGroupView[],
  extra: readonly PullRequestReadinessView[] = [],
): Map<string, PullRequestReadinessView> {
  const map = new Map<string, PullRequestReadinessView>();
  for (const g of groups) {
    for (const m of g.members) {
      if (m.readiness) map.set(m.pr_key, m.readiness);
    }
  }
  for (const view of extra) {
    map.set(view.pr_key, view);
  }
  return map;
}

export interface HistoryEntry {
  id: string;
  at: string;
  source: string;
  text: string;
  searchText: string;
}

/**
 * Reconstruct a best-effort history timeline from durable group/member fields
 * and merged standalone PRs. Labeled as derived from current durable state —
 * not a complete audit log.
 */
export function buildHistoryEntries(input: {
  deliveryGroups: readonly DeliveryGroupView[];
  standalone: readonly StandalonePRItem[];
}): HistoryEntry[] {
  const entries: HistoryEntry[] = [];
  for (const g of input.deliveryGroups) {
    entries.push({
      id: `grp-created-${g.id}`,
      at: g.created_at,
      source: g.created_by
        ? `Loom · ${g.created_by}`
        : "Loom · delivery group",
      text: `Created delivery group “${g.title}” (rev ${g.revision}).`,
      searchText: `${g.title} ${g.id} ${g.epic_id ?? ""}`,
    });
    if (g.updated_at !== g.created_at) {
      entries.push({
        id: `grp-updated-${g.id}-${g.revision}`,
        at: g.updated_at,
        source: g.updated_by
          ? `Loom · ${g.updated_by}`
          : "Loom · delivery group",
        text: `Updated “${g.title}” to revision ${g.revision}${
          g.inconsistent ? " · inconsistent row" : ""
        }.`,
        searchText: `${g.title} ${g.id}`,
      });
    }
    for (const m of g.members) {
      entries.push({
        id: `mem-${g.id}-${m.pr_key}-${m.added_at}`,
        at: m.added_at,
        source: m.added_by ? `Loom · ${m.added_by}` : "Loom · membership",
        text: `Added ${shortKey(m.pr_key)} to “${g.title}” (${m.repo_name}).`,
        searchText: `${m.pr_key} ${g.title} ${m.repo_name}`,
      });
    }
  }
  for (const s of input.standalone) {
    if (s.pr.state !== "MERGED") continue;
    entries.push({
      id: `merged-${s.prKey}`,
      at: s.pr.updated_at ?? s.pr.created_at ?? "",
      source: "GitHub observation",
      text: `Standalone ${shortKey(s.prKey)} observed merged${
        s.pr.base_ref_name ? ` into ${s.pr.base_ref_name}` : ""
      }.`,
      searchText: `${s.prKey} ${s.pr.title} merged`,
    });
  }
  return entries
    .filter((e) => e.at)
    .sort((a, b) => b.at.localeCompare(a.at));
}

function shortKey(prKey: string): string {
  return prKey.replace(/^github:/i, "");
}

export function epicOptionsFromItems(
  items: readonly WorkspaceItem[],
): Array<[string, number]> {
  const counts = new Map<string, number>();
  const bump = (epic: string) =>
    counts.set(epic, (counts.get(epic) ?? 0) + 1);
  for (const item of items) {
    if (item.kind === "standalone") {
      bump(item.epicTitle || item.epicId || "No epic");
    } else {
      bump(item.group.epic_id || "No epic");
    }
  }
  return [...counts.entries()].sort(([a], [b]) => a.localeCompare(b));
}

export { repoNameOf, memberRepo, memberRepoName };
