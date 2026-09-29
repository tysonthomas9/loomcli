/**
 * Pure helper distinguishing same-repo branch ancestry from cross-repo
 * delivery order for the compact stack-context strip (STACKED-PRS-11).
 */

export type DeliveryAncestryCue =
  | "based on prev branch"
  | "delivery after prev"
  | "cross-repo delivery";

export interface DeliveryAncestryInput {
  /** Canonical owner/repo (or comparable identity) for the current PR. */
  currentRepo: string;
  /** Canonical owner/repo for the previous member; null/empty = no previous. */
  prevRepo?: string | null;
  /** Current PR base branch (from readiness snapshot or GitHub list). */
  currentBaseRef?: string | null;
  /** Previous PR head branch. */
  prevHeadRef?: string | null;
}

function normalizeRepo(repo: string): string {
  return repo.trim().toLowerCase();
}

/**
 * Classify the relationship between the current member and its predecessor.
 * Returns null when there is no previous member.
 */
export function deliveryAncestryCue(
  input: DeliveryAncestryInput,
): DeliveryAncestryCue | null {
  const prev = (input.prevRepo ?? "").trim();
  if (!prev) return null;

  const current = normalizeRepo(input.currentRepo);
  if (!current || current !== normalizeRepo(prev)) {
    return "cross-repo delivery";
  }

  const base = (input.currentBaseRef ?? "").trim();
  const head = (input.prevHeadRef ?? "").trim();
  if (base && head && base === head) {
    return "based on prev branch";
  }
  return "delivery after prev";
}

/** Build a review-workspace deep link for a PR key. */
export function reviewPrDeepLink(
  workspaceId: string,
  prKey: string,
): string | null {
  const match = /^(?:github:)?([^/#]+)\/([^/#]+)#(\d+)$/i.exec(prKey.trim());
  if (!match?.[1] || !match[2] || !match[3]) return null;
  const ref = `${match[1].toLowerCase()}/${match[2].toLowerCase()}#${match[3]}`;
  const params = new URLSearchParams({ "review-pr": ref });
  return `/ws/${encodeURIComponent(workspaceId)}/prs?${params.toString()}`;
}

/** Build an Open-stack deep link that selects a group + member on /prs. */
export function stackGroupDeepLink(
  workspaceId: string,
  groupId: string,
  prKey: string,
): string {
  const params = new URLSearchParams({ group: groupId, pr: prKey });
  return `/ws/${encodeURIComponent(workspaceId)}/prs?${params.toString()}`;
}
