import type { LocalSettingsData } from "@/api/common";
import type { RepoInfo, WorkspaceAgentInfo } from "@/api/workspace/workspace";
import {
  createWorkspaceAgent,
  deleteWorkspaceAgent,
  EPIC_RUNNER_WORKFLOW_NAME,
  startWorkflowRun,
  type WorkflowRun,
} from "@/api";
import type { Issue } from "@/types";
import {
  epicRunnerRuntimePayload,
  issueRepoName,
  leadAgentRepoNames,
} from "@/utils/epicRunnerPayload";

const MAX_EPIC_LEAD_NAME_SLUG_LENGTH = 48;

export function epicLeadNameSlug(epicId: string): string {
  const slug = epicId
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, MAX_EPIC_LEAD_NAME_SLUG_LENGTH)
    .replace(/-+$/g, "");

  return slug || "epic";
}

export function nextEpicLeadName(
  epicId: string,
  existingNames: Iterable<string>,
): string {
  const existing = new Set(
    Array.from(existingNames, (name) => name.toLowerCase()),
  );
  const base = `lead-${epicLeadNameSlug(epicId)}`;
  if (!existing.has(base.toLowerCase())) return base;

  for (let i = 2; ; i += 1) {
    const candidate = `${base}-${i}`;
    if (!existing.has(candidate.toLowerCase())) return candidate;
  }
}

function isAgentNameConflict(error: unknown): boolean {
  const message =
    error instanceof Error
      ? error.message
      : typeof error === "string"
        ? error
        : "";
  const normalized = message.toLowerCase();
  return (
    normalized.includes("already") ||
    normalized.includes("conflict") ||
    normalized.includes("exists")
  );
}

export interface StartEpicRunnerForIssueParams {
  workspaceId: string;
  issue: Pick<Issue, "id" | "issue_type" | "repo" | "source_repo" | "labels">;
  repos: Pick<
    RepoInfo,
    "name" | "source_repo_id" | "remote" | "remote_url" | "default_branch"
  >[];
  agents: Iterable<EpicLeadCandidate>;
  localSettings: LocalSettingsData | null | undefined;
  upsertAgent?: ((agent: WorkspaceAgentInfo) => void) | undefined;
}

export interface EpicLeadCandidate {
  name: string;
  parent?: string;
  role?: string;
  role_name?: string;
}

export interface StartEpicRunnerForIssueResult {
  run: WorkflowRun;
  leadAgentName: string;
}

export async function startEpicRunnerForIssue({
  workspaceId,
  issue,
  repos,
  agents,
  localSettings,
  upsertAgent,
}: StartEpicRunnerForIssueParams): Promise<StartEpicRunnerForIssueResult> {
  if (issue.issue_type !== "epic") {
    throw new Error("Only epic issues can start the epic runner workflow");
  }

  const currentRepo = issueRepoName(issue);
  const runtimePayload = epicRunnerRuntimePayload({
    localSettings,
    repos,
    currentRepo,
  });
  const repoNames = leadAgentRepoNames(repos, currentRepo);
  const knownAgents = Array.from(agents);
  const existingLead = knownAgents.find((agent) => {
    const role = (agent.role_name ?? agent.role ?? "").trim().toLowerCase();
    return (
      agent.parent === issue.id && (role === "lead" || role === "orchestrator")
    );
  });
  const leadNames = new Set<string>(knownAgents.map((agent) => agent.name));
  let leadAgentName = "";
  let createdLeadAgentName = "";
  let lastCreateError: unknown = null;

  if (existingLead) {
    leadAgentName = existingLead.name;
  }

  for (let attempt = 0; !leadAgentName && attempt < 5; attempt += 1) {
    const candidate = nextEpicLeadName(issue.id, leadNames);
    leadNames.add(candidate);

    try {
      const leadAgent = await createWorkspaceAgent(workspaceId, {
        name: candidate,
        role_name: "lead",
        auto: false,
        cross_repo: repoNames.length === 0,
        repos: repoNames,
      });
      upsertAgent?.(leadAgent);
      leadAgentName = leadAgent.name;
      createdLeadAgentName = leadAgent.name;
      break;
    } catch (err) {
      lastCreateError = err;
      if (!isAgentNameConflict(err)) throw err;
    }
  }

  if (!leadAgentName) {
    throw lastCreateError instanceof Error
      ? lastCreateError
      : new Error("Unable to create lead agent");
  }

  try {
    const run = await startWorkflowRun(workspaceId, EPIC_RUNNER_WORKFLOW_NAME, {
      epicId: issue.id,
      leadName: leadAgentName,
      requestedBy: "ui",
      ...runtimePayload,
    });
    return { run, leadAgentName };
  } catch (err) {
    if (createdLeadAgentName) {
      deleteWorkspaceAgent(workspaceId, createdLeadAgentName).catch(
        (cleanupErr: unknown) => {
          console.warn(
            "failed to delete epic-runner lead after workflow start failed",
            cleanupErr,
          );
        },
      );
    }
    throw err;
  }
}
