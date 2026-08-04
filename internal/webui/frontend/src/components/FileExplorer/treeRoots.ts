import type {
  FileCheckout,
  FileScopeRef,
  RepoInfo,
  WorkspaceAgentInfo,
} from "@/api/workspace";
import { checkoutRefKey, type CheckoutRef } from "@/utils/fileExplorerRefs";

import { checkoutChangeCount } from "./checkoutAvailability";

export type FileBrowserMode = "workspace" | "agent";

export interface CheckoutTreeRoot {
  id: string;
  kind: "checkout";
  ref: CheckoutRef;
  label: string;
  secondary?: string | undefined;
  icon: "agent" | "repo" | "workspace";
  exists: boolean;
  changeCount: number;
  gitStatusUnavailable: boolean;
  dimmed?: boolean | undefined;
}

export interface AgentTreeRoot {
  id: string;
  kind: "agent";
  agentName: string;
  label: string;
  secondary?: string | undefined;
  exists: boolean;
  changeCount: number;
  gitStatusUnavailable: boolean;
  flattenedRef?: CheckoutRef | undefined;
  children: CheckoutTreeRoot[];
}

export type FileTreeRoot = CheckoutTreeRoot | AgentTreeRoot;

export interface FileTreeSection {
  id: "agents" | "repos" | "workspace";
  title: string;
  dimmed?: boolean | undefined;
  roots: FileTreeRoot[];
}

export interface BuildFileTreeSectionsInput {
  mode: FileBrowserMode;
  agentName?: string | undefined;
  agents: WorkspaceAgentInfo[];
  repos: RepoInfo[];
  checkouts: FileCheckout[];
}

function checkoutKey(
  kind: "agent" | "repo",
  repo: string,
  agent?: string,
): string {
  return kind === "agent" ? `agent:${agent ?? ""}:${repo}` : `repo::${repo}`;
}

function checkoutMap(checkouts: FileCheckout[]): Map<string, FileCheckout> {
  const map = new Map<string, FileCheckout>();
  for (const checkout of checkouts) {
    map.set(
      checkoutKey(checkout.kind, checkout.repo, checkout.agent),
      checkout,
    );
  }
  return map;
}

export function agentAllowedRepoNames(
  agent: WorkspaceAgentInfo,
  repos: RepoInfo[],
): string[] {
  if (agent.repos.length === 0 && agent.repo_groups.length === 0) {
    return repos.map((repo) => repo.name);
  }
  const allowed = new Set(agent.repos);
  for (const group of agent.repo_groups) {
    for (const repo of repos) {
      if (repo.groups?.includes(group)) {
        allowed.add(repo.name);
      }
    }
  }
  return repos.map((repo) => repo.name).filter((name) => allowed.has(name));
}

function checkoutForAgent(
  checkouts: Map<string, FileCheckout>,
  agent: string,
  repo: string,
): FileCheckout | undefined {
  return checkouts.get(checkoutKey("agent", repo, agent));
}

function checkoutForRepo(
  checkouts: Map<string, FileCheckout>,
  repo: string,
): FileCheckout | undefined {
  return checkouts.get(checkoutKey("repo", repo));
}

function checkoutRoot(
  ref: FileScopeRef,
  label: string,
  icon: CheckoutTreeRoot["icon"],
  checkout: FileCheckout | undefined,
  options?: {
    secondary?: string | undefined;
    dimmed?: boolean | undefined;
    existsFallback?: boolean | undefined;
  },
): CheckoutTreeRoot {
  const normalized: CheckoutRef =
    ref.scope === "agent" && ref.repo
      ? { scope: "agent", target: ref.target, repo: ref.repo }
      : ref.scope === "agent"
        ? { scope: "agent", target: ref.target }
        : ref.scope === "repo"
          ? { scope: "repo", target: ref.target }
          : { scope: "workspace" };
  return {
    id: checkoutRefKey(normalized),
    kind: "checkout",
    ref: normalized,
    label,
    secondary: options?.secondary,
    icon,
    exists: checkout?.exists ?? options?.existsFallback ?? true,
    changeCount: checkoutChangeCount(checkout),
    gitStatusUnavailable: checkout?.status_error === true,
    dimmed: options?.dimmed,
  };
}

export function buildFileTreeSections({
  mode,
  agentName,
  agents,
  repos,
  checkouts,
}: BuildFileTreeSectionsInput): FileTreeSection[] {
  const visibleAgents =
    mode === "agent" && agentName
      ? agents.filter((agent) => agent.name === agentName)
      : agents;
  const sharedRepos = repos.filter((repo) => !repo.is_linked_worktree);
  const checkoutsByKey = checkoutMap(checkouts);

  const agentRoots: AgentTreeRoot[] = visibleAgents.map((agent) => {
    const allowedRepos = agentAllowedRepoNames(agent, sharedRepos);
    const children = allowedRepos.map((repo) => {
      const checkout = checkoutForAgent(checkoutsByKey, agent.name, repo);
      return checkoutRoot(
        { scope: "agent", target: agent.name, repo },
        repo,
        "repo",
        checkout,
        { existsFallback: true },
      );
    });
    const changeCount = children.reduce(
      (sum, child) => sum + child.changeCount,
      0,
    );
    const exists =
      children.length === 0 || children.some((child) => child.exists);
    if (children.length <= 1) {
      const repo = allowedRepos[0];
      const checkout = repo
        ? checkoutForAgent(checkoutsByKey, agent.name, repo)
        : undefined;
      return {
        id: `agent:${agent.name}`,
        kind: "agent",
        agentName: agent.name,
        label: agent.name,
        secondary: repo,
        exists: checkout?.exists ?? exists,
        changeCount,
        gitStatusUnavailable: checkout?.status_error === true,
        flattenedRef: repo
          ? { scope: "agent", target: agent.name, repo }
          : { scope: "agent", target: agent.name },
        children: [],
      };
    }
    return {
      id: `agent:${agent.name}`,
      kind: "agent",
      agentName: agent.name,
      label: agent.name,
      exists,
      changeCount,
      gitStatusUnavailable: false,
      children,
    };
  });

  const repoRoots =
    mode === "workspace"
      ? sharedRepos.map((repo) =>
          checkoutRoot(
            { scope: "repo", target: repo.name },
            repo.name,
            "repo",
            checkoutForRepo(checkoutsByKey, repo.name),
            { existsFallback: true },
          ),
        )
      : [];

  const sections: FileTreeSection[] = [
    { id: "agents", title: "Agents", roots: agentRoots },
  ];
  if (mode === "workspace") {
    sections.push({ id: "repos", title: "Repos", roots: repoRoots });
    sections.push({
      id: "workspace",
      title: "Workspace files",
      dimmed: true,
      roots: [
        checkoutRoot(
          { scope: "workspace" },
          "Workspace files",
          "workspace",
          undefined,
          { dimmed: true },
        ),
      ],
    });
  }
  return sections;
}

export function existingCheckoutRefs(
  sections: FileTreeSection[],
): CheckoutRef[] {
  const refs: CheckoutRef[] = [];
  for (const section of sections) {
    for (const root of section.roots) {
      if (root.kind === "checkout" && root.exists) {
        refs.push(root.ref);
      } else if (root.kind === "agent") {
        if (root.flattenedRef && root.exists) refs.push(root.flattenedRef);
        for (const child of root.children) {
          if (child.exists) refs.push(child.ref);
        }
      }
    }
  }
  const seen = new Set<string>();
  return refs.filter((ref) => {
    const key = checkoutRefKey(ref);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
