import type { FileScopeRef } from "@/api/workspace";
import { normalizeCheckoutRef } from "@/utils/fileExplorerRefs";

export interface NormalizedFileScopeRef {
  scope: FileScopeRef["scope"];
  target: string | null;
  repo: string | null;
}

function normalizedRevision(value: string | null | undefined): string | null {
  const trimmed = value?.trim();
  return trimmed ? trimmed : null;
}

function normalizedLimit(value: number | null | undefined): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function normalizeFileScopeRef(
  ref: FileScopeRef,
): NormalizedFileScopeRef {
  const normalized = normalizeCheckoutRef(ref);
  return {
    scope: normalized.scope,
    target: normalized.target ?? null,
    repo: normalized.scope === "agent" ? (normalized.repo ?? null) : null,
  };
}

export const fileQueryKeys = {
  all: (workspaceId: string) => ["workspace", workspaceId, "files"] as const,
  checkouts: (workspaceId: string) =>
    [...fileQueryKeys.all(workspaceId), "checkouts"] as const,
  gitStatusPrefix: (workspaceId: string) =>
    [...fileQueryKeys.all(workspaceId), "git-status"] as const,
  gitStatus: (workspaceId: string, ref: FileScopeRef) =>
    [
      ...fileQueryKeys.gitStatusPrefix(workspaceId),
      normalizeFileScopeRef(ref),
    ] as const,
};

export const agentQueryKeys = {
  all: (workspaceId: string) => ["workspace", workspaceId, "agents"] as const,
  agent: (workspaceId: string, agentName: string) =>
    [...agentQueryKeys.all(workspaceId), agentName.trim()] as const,
  agentGitStatus: (workspaceId: string, agentName: string) =>
    [...agentQueryKeys.agent(workspaceId, agentName), "git", "status"] as const,
  diffStat: (workspaceId: string, agentName: string) =>
    [
      ...agentQueryKeys.agent(workspaceId, agentName),
      "git",
      "diff-stat",
    ] as const,
  diffFilesPrefix: (workspaceId: string, agentName: string) =>
    [...agentQueryKeys.agent(workspaceId, agentName), "diff", "files"] as const,
  diffFiles: (
    workspaceId: string,
    agentName: string,
    to = "HEAD",
    from?: string | null,
  ) =>
    [
      ...agentQueryKeys.diffFilesPrefix(workspaceId, agentName),
      { to: normalizedRevision(to) ?? "HEAD", from: normalizedRevision(from) },
    ] as const,
  diffCommitsPrefix: (workspaceId: string, agentName: string) =>
    [
      ...agentQueryKeys.agent(workspaceId, agentName),
      "diff",
      "commits",
    ] as const,
  diffCommits: (
    workspaceId: string,
    agentName: string,
    limit?: number | null,
  ) =>
    [
      ...agentQueryKeys.diffCommitsPrefix(workspaceId, agentName),
      { limit: normalizedLimit(limit) },
    ] as const,
};
