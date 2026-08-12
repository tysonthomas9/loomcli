import type { LocalSettingsData } from "@/api/common";
import type { RepoInfo } from "@/api/workspace";

export interface EpicRunnerRuntimePayload {
  runner?: string;
  repoUrl?: string;
  baseBranch?: string;
  deliveryMode: "patch-back" | "pull-request";
  openPullRequest?: boolean;
  stackedPullRequests?: boolean;
}

export function issueRepoName(
  issue:
    | {
        repo?: string;
        source_repo?: string;
        labels?: string[];
      }
    | null
    | undefined,
): string | null {
  if (issue?.repo) return issue.repo;
  if (issue?.source_repo) return issue.source_repo;
  const repoLabel = issue?.labels?.find((label) => label.startsWith("repo:"));
  return repoLabel ? repoLabel.slice(5) : null;
}

export function epicRunnerRuntimePayload({
  localSettings,
  repos,
  currentRepo,
}: {
  localSettings: LocalSettingsData | null | undefined;
  repos: Pick<
    RepoInfo,
    "name" | "source_repo_id" | "remote" | "remote_url" | "default_branch"
  >[];
  currentRepo: string | null;
}): EpicRunnerRuntimePayload {
  const repo = runnerRepoUrl(repos, currentRepo);
  if (localSettings?.agent_runtime.default !== "daytona") {
    // Local ("Locally") runtime: pin the runner explicitly so the request never
    // falls through to an unspecified server-side default. The local task runner
    // execFile's the user-selected backend CLI over the prepared worktree. When
    // a workspace repo is selected, opt in to PR delivery; the runner fails
    // closed if the desktop GitHub credential is not configured.
    return {
      runner: "local-task-runner",
      deliveryMode: repo.repoUrl ? "pull-request" : "patch-back",
      ...(repo.repoUrl
        ? {
            repoUrl: repo.repoUrl,
            baseBranch: repo.baseBranch,
            openPullRequest: true,
          }
        : {}),
    };
  }
  if (!repo.repoUrl) {
    throw new Error(
      "Daytona runtime requires a GitHub repo URL or owner/repo repo selection",
    );
  }
  return {
    runner: "daytona-task-runner",
    deliveryMode: "pull-request",
    repoUrl: repo.repoUrl,
    baseBranch: repo.baseBranch,
    openPullRequest: true,
    stackedPullRequests: true,
  };
}

export function runnerRepoUrl(
  repos: Pick<
    RepoInfo,
    "name" | "source_repo_id" | "remote" | "remote_url" | "default_branch"
  >[],
  currentRepo: string | null,
): { repoUrl: string; baseBranch: string } {
  const selected =
    repos.find(
      (repo) =>
        repo.name === currentRepo || repo.source_repo_id === currentRepo,
    ) || (repos.length === 1 ? repos[0] : undefined);
  const remote = selected?.remote_url || selected?.remote || currentRepo || "";
  return {
    repoUrl: normalizeGitHubRepoUrl(remote),
    baseBranch: selected?.default_branch || "main",
  };
}

export function leadAgentRepoNames(
  repos: Pick<RepoInfo, "name" | "source_repo_id">[],
  currentRepo: string | null,
): string[] {
  const selected =
    repos.find(
      (repo) =>
        repo.name === currentRepo || repo.source_repo_id === currentRepo,
    ) || (repos.length === 1 ? repos[0] : undefined);

  return selected?.name ? [selected.name] : [];
}

export function normalizeGitHubRepoUrl(value: string): string {
  const text = value.trim();
  if (!text) return "";
  if (/^https:\/\/github\.com\/[^/]+\/[^/]+(?:\.git)?$/i.test(text)) {
    return text;
  }
  const sshMatch = text.match(
    /^git@github\.com:([A-Za-z0-9_.-]+)\/([A-Za-z0-9_.-]+)$/i,
  );
  if (sshMatch?.[1] && sshMatch[2]) {
    const repo = sshMatch[2].replace(/\.git$/i, "");
    return `https://github.com/${sshMatch[1]}/${repo}.git`;
  }
  if (/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(text)) {
    return `https://github.com/${text}.git`;
  }
  return "";
}
