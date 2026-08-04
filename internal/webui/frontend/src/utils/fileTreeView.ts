/**
 * Resolve how the file tree should be loaded for an agent.
 * Lead agents have no local worktree; browse the workspace primary repo instead.
 */

import type { RepoInfo } from "@/api/workspace";

import { isLeadRole } from "./agentRole";

export interface FileTreeViewMode {
  /** When true, list/read target the workspace primary repo (lead fallback). */
  useWorkspaceTree: boolean;
  /** Primary repo name for UI labeling, when browsing workspace tree. */
  repoLabel: string | null;
}

export function resolveFileTreeViewMode(
  agentRole: string | undefined,
  agentRepo: string | undefined,
  repos: RepoInfo[],
): FileTreeViewMode {
  if (!isLeadRole(agentRole)) {
    return { useWorkspaceTree: false, repoLabel: null };
  }

  const primaryRepos = repos.filter((repo) => !repo.is_linked_worktree);
  // `agentRepo && find()` returns "" (not nullish) for an empty-string repo, so
  // `??` would keep "" and skip the primary fallback — use an explicit ternary.
  const matchedRepo = agentRepo
    ? primaryRepos.find((repo) => repo.name === agentRepo)?.name
    : undefined;
  const repoName = matchedRepo ?? primaryRepos[0]?.name ?? null;

  return { useWorkspaceTree: true, repoLabel: repoName };
}
