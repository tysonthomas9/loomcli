import type { RepoInfo } from "@/api/workspace";

export function findWorkspaceRepo(
  repos: readonly RepoInfo[],
  sourceRepo: string | undefined,
): RepoInfo | undefined {
  if (!sourceRepo) return undefined;

  return repos.find(
    (repo) => repo.source_repo_id === sourceRepo || repo.name === sourceRepo,
  );
}

export function repoNameForSource(
  repos: readonly RepoInfo[],
  sourceRepo: string | undefined,
): string {
  if (!sourceRepo) return "no repo";
  return findWorkspaceRepo(repos, sourceRepo)?.name ?? sourceRepo;
}

export function targetBranchForSource(
  repos: readonly RepoInfo[],
  sourceRepo: string | undefined,
): string {
  return (
    findWorkspaceRepo(repos, sourceRepo)?.default_branch ?? "the target branch"
  );
}
