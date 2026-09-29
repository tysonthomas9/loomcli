/**
 * Read-only model for the epic-detail "Delivery & lineage" rollup
 * (STACKED-PRS-7).
 *
 * Two relations are kept strictly apart:
 * - Delivery order: FleetDB `DeliveryGroup.members[]` order. May cross repos.
 * - Branch base: GitHub head/base refs observed on the same repository. Only
 *   ever rendered as a footnote; never renumbers or reorders delivery steps.
 *
 * Nothing here infers a group or lineage from ticket PR URLs.
 */

import type {
  DeliveryGroupMemberView,
  DeliveryGroupView,
} from "@/api/workspace/deliveryGroups";
import type { PullRequestReadinessView } from "@/api/workspace/pullRequests";

import {
  readinessDisplay,
  type ReadinessDisplay,
  type ReadinessDisplayKey,
} from "./readinessDisplay";
import { memberRepo } from "./stackedPrModel";

/** Member sources that mean the PR was not produced by a Loom task. */
const EXTERNAL_SOURCES: ReadonlySet<DeliveryGroupMemberView["source"]> =
  new Set(["manual", "native_stack_suggestion", "identity_heal"]);

/** External = assigned to the group but not backed by a Loom task. */
export function isExternalMember(member: DeliveryGroupMemberView): boolean {
  return !member.task_id?.trim() || EXTERNAL_SOURCES.has(member.source);
}

/** Readiness for a member: decorated view first, then a separately read one. */
export function memberReadiness(
  member: DeliveryGroupMemberView,
  extra: ReadonlyMap<string, PullRequestReadinessView>,
): PullRequestReadinessView | undefined {
  return member.readiness ?? extra.get(member.pr_key);
}

/**
 * Worst-first ranking for the group header badge. Anything that cannot be
 * established (stale, unknown, not observed) outranks positive verdicts so a
 * group is never summarized as Ready on weak evidence.
 */
const SEVERITY: Record<ReadinessDisplayKey, number> = {
  blocked: 0,
  stale: 1,
  unknown: 2,
  waiting: 3,
  queued: 4,
  draft: 5,
  ready: 6,
  closed: 7,
  merged: 8,
};

/** Worst member readiness, or null for a group with no members. */
export function worstGroupReadiness(
  group: DeliveryGroupView,
  extra: ReadonlyMap<string, PullRequestReadinessView>,
): ReadinessDisplay | null {
  let worst: ReadinessDisplay | null = null;
  for (const member of group.members) {
    const display = readinessDisplay(memberReadiness(member, extra));
    if (!worst || SEVERITY[display.key] < SEVERITY[worst.key]) worst = display;
  }
  return worst;
}

/** Repo identity of the previous delivery step when it differs, else null. */
export function crossRepoFrom(
  members: readonly DeliveryGroupMemberView[],
  index: number,
): string | null {
  if (index <= 0) return null;
  const prev = members[index - 1];
  const current = members[index];
  if (!prev || !current) return null;
  const from = memberRepo(prev);
  return from !== memberRepo(current) ? from : null;
}

export interface BranchBaseLink {
  /** 0-based delivery index of the PR based on another member's head. */
  childIndex: number;
  /** 0-based delivery index of the member whose head branch is that base. */
  baseIndex: number;
  repo: string;
  /** Shared branch: child's base_ref === base member's head_ref. */
  branch: string;
  /** True when either side's refs come from non-fresh evidence. */
  lastKnown: boolean;
}

/**
 * Same-repository branch-base pairs inside one group: member A's observed
 * `base_ref` equals member B's observed `head_ref` in the same repo. Refs come
 * only from readiness snapshots; members without an observed snapshot yield no
 * link. Results are in delivery order of the child and never change it.
 */
export function branchBaseLinks(
  group: DeliveryGroupView,
  extra: ReadonlyMap<string, PullRequestReadinessView>,
): BranchBaseLink[] {
  const observed = group.members.map((member) => {
    const view = memberReadiness(member, extra);
    const snap = view?.snapshot;
    return {
      repo: memberRepo(member),
      head: snap?.head_ref?.trim() ?? "",
      base: snap?.base_ref?.trim() ?? "",
      fresh: view?.freshness === "fresh",
    };
  });

  const links: BranchBaseLink[] = [];
  observed.forEach((child, childIndex) => {
    if (!child.base) return;
    const baseIndex = observed.findIndex(
      (other, i) =>
        i !== childIndex &&
        other.repo === child.repo &&
        other.head === child.base,
    );
    if (baseIndex < 0) return;
    links.push({
      childIndex,
      baseIndex,
      repo: child.repo,
      branch: child.base,
      lastKnown: !child.fresh || !observed[baseIndex]!.fresh,
    });
  });
  return links;
}

/** Member keys that still need a readiness read (no decorated view). */
export function prKeysMissingReadiness(
  groups: readonly DeliveryGroupView[],
  known: ReadonlyMap<string, PullRequestReadinessView>,
): string[] {
  const out = new Set<string>();
  for (const group of groups) {
    for (const member of group.members) {
      if (!member.readiness && !known.has(member.pr_key)) {
        out.add(member.pr_key);
      }
    }
  }
  return [...out];
}

function prsBase(workspaceId: string): string {
  return `/ws/${encodeURIComponent(workspaceId)}/prs`;
}

/** Deep link into the Pull Requests workspace for one group (and PR). */
export function prsGroupHref(
  workspaceId: string,
  groupId: string,
  prKey?: string,
): string {
  const params = new URLSearchParams({ group: groupId });
  if (prKey) params.set("pr", prKey);
  return `${prsBase(workspaceId)}?${params.toString()}`;
}

/** Deep link into the Pull Requests workspace filtered to one epic. */
export function prsEpicHref(workspaceId: string, epicId: string): string {
  const params = new URLSearchParams({ epic: epicId });
  return `${prsBase(workspaceId)}?${params.toString()}`;
}
