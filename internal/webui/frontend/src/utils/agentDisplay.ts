/**
 * Short display labels for agents. Prefer API display_name / role_label when
 * present; fall back to deriving PR-reviewer titles from the stable agent name.
 */

import type { LoomAgentStatus } from "@/types";

const PR_REVIEWER_ROLE = "pr-reviewer";
const PR_REVIEWER_ROLE_LABEL = "Review";
const PR_NUMBER_SUFFIX = /-pr-(\d+)$/i;
const PR_HASHED_BODY = /^review-(.+)-([0-9a-f]{8})$/i;
const PR_HASHED_FULL = /^review-(.+)-([0-9a-f]{8})-pr-(\d+)$/i;
const PR_LEGACY_FULL = /^review-(.+)-pr-(\d+)$/i;

function lastSegment(value: string): string {
  const parts = value.replace(/^-+|-+$/g, "").split("-");
  for (let i = parts.length - 1; i >= 0; i -= 1) {
    if (parts[i]) return parts[i]!;
  }
  return "";
}

/** Mirror the backend's safeAgentSegment normalization for name matching. */
function reviewerAgentSegment(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

function repoHintFromAgent(
  agent: Pick<LoomAgentStatus, "display_name" | "repo">,
): string {
  const repoField = (agent.repo ?? "").trim();
  if (repoField.includes("/")) {
    return repoField.split("/").pop() ?? repoField;
  }
  if (repoField) return repoField;
  const display = (agent.display_name ?? "").trim();
  const match = /^([^/#]+)#\d+$/.exec(display);
  return match?.[1] ?? "";
}

/** Derive "repo#number" from a reviewer agent name when API fields are absent. */
export function derivePRReviewerDisplayName(
  agentName: string,
  repoHint?: string | undefined,
): string | undefined {
  const name = agentName.trim().toLowerCase();
  const match = PR_NUMBER_SUFFIX.exec(name);
  if (!match?.[1]) return undefined;
  const number = Number.parseInt(match[1], 10);
  if (!Number.isFinite(number) || number <= 0) return undefined;

  const fromHint = (repoHint ?? "").trim();
  if (fromHint) return `${fromHint}#${number}`;

  const body = name.slice(0, match.index);
  const hashed = PR_HASHED_BODY.exec(body);
  if (hashed?.[1]) {
    const repo = lastSegment(hashed[1]);
    if (repo) return `${repo}#${number}`;
  }
  if (body.startsWith("review-")) {
    const repo = body.slice("review-".length);
    if (repo) return `${repo}#${number}`;
  }
  return undefined;
}

/**
 * Build the PRs-page `review-pr` query value (`owner/repo#number`) for a
 * pr-reviewer agent so sidebar clicks open the matching review workspace.
 */
export function prReviewRefFromAgent(
  agent: Pick<LoomAgentStatus, "name" | "role" | "display_name" | "repo">,
): string | null {
  if ((agent.role ?? "").trim().toLowerCase() !== PR_REVIEWER_ROLE) {
    return null;
  }
  const name = agent.name.trim().toLowerCase();
  const hashed = PR_HASHED_FULL.exec(name);
  if (hashed?.[1] && hashed[3]) {
    const mid = hashed[1];
    const number = hashed[3];
    const repoHint = repoHintFromAgent(agent);
    const repoSegment = reviewerAgentSegment(repoHint);
    if (
      repoSegment &&
      (mid === repoSegment || mid.endsWith(`-${repoSegment}`))
    ) {
      const owner =
        mid === repoSegment ? "" : mid.slice(0, -(repoSegment.length + 1));
      if (owner) return `${owner}/${repoHint}#${number}`;
    }
    const split = mid.lastIndexOf("-");
    if (split > 0) {
      return `${mid.slice(0, split)}/${mid.slice(split + 1)}#${number}`;
    }
    return `${mid}#${number}`;
  }

  const legacy = PR_LEGACY_FULL.exec(name);
  if (legacy?.[1] && legacy[2]) {
    const repoField = (agent.repo ?? "").trim();
    if (repoField.includes("/")) {
      return `${repoField}#${legacy[2]}`;
    }
    return `${legacy[1]}#${legacy[2]}`;
  }
  return null;
}

export function isPRReviewerAgent(
  agent: Pick<LoomAgentStatus, "role"> | undefined,
): boolean {
  return (agent?.role ?? "").trim().toLowerCase() === PR_REVIEWER_ROLE;
}

export function agentDisplayTitle(
  agent: Pick<LoomAgentStatus, "name" | "role" | "display_name" | "repo">,
): string {
  const fromApi = agent.display_name?.trim();
  if (fromApi) return fromApi;
  if ((agent.role ?? "").trim().toLowerCase() === PR_REVIEWER_ROLE) {
    const derived = derivePRReviewerDisplayName(
      agent.name,
      repoHintFromAgent(agent) || undefined,
    );
    if (derived) return derived;
  }
  return agent.name;
}

export function agentDisplayRoleLabel(
  agent: Pick<LoomAgentStatus, "role" | "role_label">,
): string {
  const fromApi = agent.role_label?.trim();
  if (fromApi) return fromApi;
  const role = (agent.role ?? "").trim();
  if (!role) return "Agent";
  if (role.toLowerCase() === PR_REVIEWER_ROLE) return PR_REVIEWER_ROLE_LABEL;
  return role.charAt(0).toUpperCase() + role.slice(1);
}

export function agentUsesLiteralTitle(
  agent: Pick<LoomAgentStatus, "name" | "role" | "display_name" | "repo">,
): boolean {
  return agentDisplayTitle(agent) !== agent.name;
}

/**
 * Compact avatar glyph for rails/cards. PR reviewers show `#N` instead of
 * initials derived from the hashed `review-…` agent name (which became "RT").
 */
export function agentCompactAvatarLabel(
  agent: Pick<LoomAgentStatus, "name" | "role" | "display_name" | "repo">,
): string {
  if (isPRReviewerAgent(agent)) {
    const title = agentDisplayTitle(agent);
    const fromTitle = /#(\d+)$/.exec(title);
    if (fromTitle?.[1]) return `#${fromTitle[1]}`;
    const fromName = PR_NUMBER_SUFFIX.exec(agent.name.trim().toLowerCase());
    if (fromName?.[1]) return `#${fromName[1]}`;
  }
  return "";
}
