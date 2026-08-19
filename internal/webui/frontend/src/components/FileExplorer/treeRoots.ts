import type {
  FileCheckout,
  FileScopeRef,
  RepoInfo,
  SkillCatalogGroup,
  WorkspaceAgentInfo,
} from "@/api/workspace";
import { checkoutRefKey, type CheckoutRef } from "@/utils/fileExplorerRefs";
import {
  explorerRefKey,
  skillsExplorerRef,
  type ExplorerRef,
  type SkillsExplorerRef,
} from "@/utils/explorerRefs";

import { checkoutChangeCount } from "./checkoutAvailability";

// "skills" is the dedicated Skills section: the same browser, showing only the
// skills roots. It is a filter on which sections are emitted, not a second
// implementation — the tree, editor, tabs and dialogs are shared with the
// Files section, so the two can never drift apart.
//
// Skills live in exactly one place. "workspace" (the Files section) and "agent"
// (an agent's files) emit git checkouts and nothing else; "skills" emits skill
// roots and nothing else. Nothing emits both.
export type FileBrowserMode = "workspace" | "agent" | "skills";

// The one capability that separates the sections: a section either sits on git
// checkouts or it does not. "workspace" and "agent" browse a checkout; "skills"
// browses the catalog and has nothing checked out behind it, so everything
// checkout-shaped is off there — the Files/Changes lens (there is no second
// lens to switch to), the checkout listing, branch diffs, git status,
// search/replace and Quick Open indexing.
//
// The browser derives this once and branches on the capability, never on the
// mode, so a future section without checkouts needs no new conditionals.
export function modeHasCheckouts(mode: FileBrowserMode): boolean {
  return mode !== "skills";
}

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

export interface SkillsTreeRoot {
  id: string;
  kind: "skills";
  ref: SkillsExplorerRef;
  label: string;
  secondary?: string | undefined;
  skillCount: number;
}

export type GitTreeRoot = CheckoutTreeRoot | AgentTreeRoot;
export type FileTreeRoot = GitTreeRoot | SkillsTreeRoot;

export interface FileTreeSection {
  id: "agents" | "skills" | "repos" | "workspace";
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
  skills?: SkillCatalogGroup[] | undefined;
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

// One root per skill scope: the workspace scope plus every role that either has
// a skill in the catalog or an agent wearing it — so a role with a skill but no
// agent (and vice versa) still gets a root.
function skillsRoots(
  agents: WorkspaceAgentInfo[],
  skills: SkillCatalogGroup[],
): SkillsTreeRoot[] {
  const catalogRoles = skills
    .filter((group) => group.scope === "role" && group.role)
    .map((group) => group.role!);
  const agentRoles = agents
    .map((agent) => agent.role_name)
    .filter((role): role is string => Boolean(role));
  const roleNames = [...new Set([...catalogRoles, ...agentRoles])].sort();
  const groups = [
    { kind: "workspace" as const },
    ...roleNames.map((role) => ({ kind: "role" as const, role })),
  ];
  return groups.map((group) => {
    const count =
      skills.find((candidate) =>
        group.kind === "workspace"
          ? candidate.scope === "workspace"
          : candidate.scope === "role" && candidate.role === group.role,
      )?.skills.length ?? 0;
    const ref = skillsExplorerRef(group);
    return {
      id: explorerRefKey(ref),
      kind: "skills",
      ref,
      label: group.kind === "workspace" ? "Workspace" : group.role,
      secondary: `${count} ${count === 1 ? "skill" : "skills"}`,
      skillCount: count,
    };
  });
}

export function buildFileTreeSections({
  mode,
  agentName,
  agents,
  repos,
  checkouts,
  skills = [],
}: BuildFileTreeSectionsInput): FileTreeSection[] {
  // The Skills section is the only place skills appear. It shows skills and
  // nothing else — no agents, no repos, no workspace root — so it short-circuits
  // before any checkout work happens.
  if (mode === "skills") {
    return [
      { id: "skills", title: "Skills", roots: skillsRoots(agents, skills) },
    ];
  }

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

export function existingExplorerRefs(
  sections: FileTreeSection[],
): ExplorerRef[] {
  const refs: ExplorerRef[] = [];
  for (const section of sections) {
    for (const root of section.roots) {
      if (root.kind === "checkout" && root.exists) {
        refs.push({ kind: "checkout", checkout: root.ref });
      } else if (root.kind === "agent") {
        if (root.flattenedRef && root.exists) {
          refs.push({ kind: "checkout", checkout: root.flattenedRef });
        }
        for (const child of root.children) {
          if (child.exists) {
            refs.push({ kind: "checkout", checkout: child.ref });
          }
        }
      } else if (root.kind === "skills") {
        refs.push(root.ref);
      }
    }
  }
  const seen = new Set<string>();
  return refs.filter((ref) => {
    const key = explorerRefKey(ref);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function gitStatusRefs(sections: FileTreeSection[]): CheckoutRef[] {
  return existingExplorerRefs(sections).flatMap((ref) =>
    ref.kind === "checkout" ? [ref.checkout] : [],
  );
}
