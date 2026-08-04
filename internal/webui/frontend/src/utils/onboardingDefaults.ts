export const ONBOARDING_REPO_OWNER = "octocat";
export const ONBOARDING_REPO_NAME = "Hello-World";
export const ONBOARDING_REPO_URL = `https://github.com/${ONBOARDING_REPO_OWNER}/${ONBOARDING_REPO_NAME}`;
export const ONBOARDING_WORKSPACE_NAME = ONBOARDING_REPO_NAME;
export const ONBOARDING_AGENT_NAME = "planner";
export const ONBOARDING_AGENT_ROLE = "plan";
export const ONBOARDING_ISSUE_TITLE = "Explore Hello-World onboarding";
export const ONBOARDING_ISSUE_DESCRIPTION = `Use the prefilled sample repo at ${ONBOARDING_REPO_URL}.

Inspect the repository and produce a concise plan for a first useful change, including files to inspect and tests to run.`;

export interface OnboardingRepoCandidate {
  name?: string;
  path?: string;
  remote?: string;
}

export function isOnboardingRepo(repo: OnboardingRepoCandidate): boolean {
  const name = repo.name?.toLowerCase() ?? "";
  const remote = repo.remote?.toLowerCase() ?? "";
  const path = repo.path?.toLowerCase() ?? "";
  const repoSlug =
    `${ONBOARDING_REPO_OWNER}/${ONBOARDING_REPO_NAME}`.toLowerCase();
  const repoName = ONBOARDING_REPO_NAME.toLowerCase();

  return (
    name === repoName ||
    remote.includes(repoSlug) ||
    path.endsWith(`/${repoName}`)
  );
}
