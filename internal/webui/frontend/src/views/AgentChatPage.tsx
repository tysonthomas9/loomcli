import {
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { getAgent, type Agent } from "@/api/agentsv1";
import type { WorkspaceAgentInfo } from "@/api/workspace";
import { AgentChat } from "@/components/AgentChat";
import { GitTab } from "@/components/AgentDetailPanel";
import { ExtraBrowserAgent } from "@/components/FileExplorer/browserAgents";
import { useRosterAgent } from "@/hooks/agents";
import type { LoomAgentStatus } from "@/types";
import { AgentEditorGroups, type AgentEditorTab } from "./AgentEditorGroups";
import styles from "./AgentsPage.module.css";

// Heavy tabs (CodeMirror/diff) are code-split, as on the v5 agent page.
const DiffTab = lazy(() =>
  import("@/components/AgentDetailPanel").then((m) => ({ default: m.DiffTab })),
);
const WorkspaceFileBrowser = lazy(() =>
  import("@/components/FileExplorer").then((m) => ({
    default: m.WorkspaceFileBrowser,
  })),
);

/** Agent API agents have Chat where v5 agents have Terminal. */
export const AGENT_API_TABS: AgentEditorTab[] = [
  "chat",
  "info",
  "git",
  "diff",
  "files",
];

const isTab = (t: string | null): t is AgentEditorTab =>
  AGENT_API_TABS.includes(t as AgentEditorTab);

const repoName = (repo: string) => repo.split("/").filter(Boolean).pop() ?? "";

/**
 * The agent page for Agent API agents, the same for every harness: Chat |
 * Info | Git | Diff | Files over the agent's own worktree. The open tab is
 * kept in ?tab= so a reload stays on it.
 */
export function AgentChatPage(): JSX.Element {
  const { workspaceId = "", agentId = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const tab = params.get("tab");
  const agent = useAgent(workspaceId, agentId);

  const onTabChange = useCallback(
    (t: AgentEditorTab) =>
      setParams(
        (p) => {
          const next = new URLSearchParams(p);
          if (t === "chat") next.delete("tab");
          else next.set("tab", t);
          return next;
        },
        { replace: true },
      ),
    [setParams],
  );

  // The v5 Git and Diff tabs read an agent by name; an Agent API agent's
  // name there is its ID.
  const branch = agent?.branch ?? "";
  const gitAgent = useMemo<LoomAgentStatus>(
    () => ({ name: agentId, branch, status: "", ahead: 0, behind: 0 }),
    [agentId, branch],
  );
  const repo = agent ? repoName(agent.repo) : "";
  const filesAgent = useMemo<WorkspaceAgentInfo>(
    () => ({
      name: agentId,
      repos: repo ? [repo] : [],
      repo_groups: [],
      cross_repo: false,
    }),
    [agentId, repo],
  );

  const renderPane = useCallback(
    (t: AgentEditorTab, isActive: boolean) => {
      switch (t) {
        case "chat":
          return (
            <div className={styles.realTabBody}>
              <AgentChat
                key={agentId}
                workspaceId={workspaceId}
                agentId={agentId}
              />
            </div>
          );
        case "info":
          return agent ? (
            <AgentInfo workspaceId={workspaceId} agent={agent} />
          ) : (
            <div className={styles.tabFallback}>Loading agent…</div>
          );
        case "git":
          return (
            <div
              className={`${styles.realTabBody} ${styles.realTabBodyScroll}`}
            >
              <GitTab agent={gitAgent} isActive={isActive} />
            </div>
          );
        case "diff":
          return (
            <div className={styles.realTabBody}>
              <Suspense
                fallback={
                  <div className={styles.tabFallback}>Loading diff…</div>
                }
              >
                <DiffTab agent={gitAgent} isActive={isActive} />
              </Suspense>
            </div>
          );
        case "files":
          return agent ? (
            <div className={styles.realTabBody}>
              <Suspense
                fallback={
                  <div className={styles.tabFallback}>Loading files…</div>
                }
              >
                <ExtraBrowserAgent.Provider value={filesAgent}>
                  <WorkspaceFileBrowser
                    mode="agent"
                    agentName={agentId}
                    isActive={isActive}
                  />
                </ExtraBrowserAgent.Provider>
              </Suspense>
            </div>
          ) : (
            <div className={styles.tabFallback}>Loading agent…</div>
          );
        default:
          return null;
      }
    },
    [agent, agentId, filesAgent, gitAgent, workspaceId],
  );

  return (
    <section
      className={styles.main}
      style={{ height: "100%", minHeight: 0 }}
      aria-label="Agent details"
      data-testid="agent-api-page"
    >
      <AgentEditorGroups
        resetKey={agentId}
        renderPane={renderPane}
        tabs={AGENT_API_TABS}
        initialTab={isTab(tab) ? tab : undefined}
        onTabChange={onTabChange}
      />
    </section>
  );
}

/** The agent from the sidebar's live roster, else read once. */
function useAgent(workspaceId: string, agentId: string): Agent | undefined {
  const listed = useRosterAgent(agentId);
  const [read, setRead] = useState<Agent>();
  useEffect(() => {
    if (listed) return;
    let live = true;
    getAgent(workspaceId, agentId)
      .then((a) => live && setRead(a))
      .catch(() => undefined);
    return () => {
      live = false;
    };
  }, [listed, workspaceId, agentId]);
  return listed ?? (read?.agent_id === agentId ? read : undefined);
}

function AgentInfo({
  workspaceId,
  agent,
}: {
  workspaceId: string;
  agent: Agent;
}): JSX.Element {
  const ws = encodeURIComponent(workspaceId);
  const rows: [string, React.ReactNode][] = [
    ["Harness", agent.harness],
    ["State", agent.state],
    [
      "Parent",
      agent.parent_agent_id ? (
        <Link
          to={`/ws/${ws}/chat/${encodeURIComponent(agent.parent_agent_id)}`}
        >
          {agent.parent_agent_id}
        </Link>
      ) : (
        "—"
      ),
    ],
    ["Repo", agent.repo || "—"],
    ["Branch", agent.branch ?? "—"],
    ["Worktree", agent.worktree_path ?? "—"],
  ];
  return (
    <div className={styles.scrollPanel}>
      <section className={styles.card}>
        <h2 className={styles.cardLabel}>{agent.name}</h2>
        <dl className={styles.configGrid}>
          {rows.map(([label, value]) => (
            <div key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  );
}
