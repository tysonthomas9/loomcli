/**
 * AgentsPage — full-page agent workspace (Aether `/agents`).
 *
 * Layout (driven by App.tsx):
 *   [AgentIconRail (App-level sidebar on /agents only)]
 *   [tabbed main panel — Terminal / Info / Git / Diff / Files]
 *   [right column — either AgentWorkPanel OR inline IssueDetailPanel]
 *
 * The tabbed main panel comes from the Aether V3 design (feat/updated-UI);
 * the right column keeps the epic-runner Open Queue (AgentWorkPanel) with
 * its workflow-run streams, Run buttons, and worker history.
 *
 * Selection model:
 * - Agent: URL-driven via :agentName (clicking an avatar in the rail
 *   navigates to /agents/<name>). Bare /agents auto-redirects to the first
 *   agent. A legacy ?agent=<name> query param is honored as a redirect.
 * - Task: local state (selectedTask). When set, the right column hides the
 *   work panel and renders the IssueDetailPanel inline. Closing the panel
 *   restores the work panel. Switching agent clears the task selection.
 */

import {
  Suspense,
  lazy,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { useStore } from "zustand";

import { ErrorBoundary, LoadingSkeleton } from "@/components";
import { AgentDetailMain } from "@/components/AgentDetailMain/AgentDetailMain";
import { GitTab } from "@/components/AgentDetailPanel";
import { AgentWorkPanel } from "@/components/AgentWorkPanel/AgentWorkPanel";
import {
  isLiveAgentRailVisible,
  orderAgentsForEpicRunner,
} from "@/components/AgentIconRail/AgentIconRail";
import { IssueDetailPanel } from "@/components/IssueDetailPanel/IssueDetailPanel";
import {
  EPIC_RUNNER_WORKFLOW_NAME,
  isTerminalWorkflowRunStatus,
  startWorkflowRun,
  type WorkflowRun,
} from "@/api";
import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { useAgentStoreInstance } from "@/hooks";
import { useToast } from "@/hooks/ui/useToast";
import { useWorkflowRunStreams } from "@/hooks/workflows/useWorkflowRunStreams";
import { parseLoomStatus } from "@/types";
import type { Issue } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { formatStatusLabel } from "@/utils/issue";
import type { TerminalInputRequest } from "@/components/TerminalView/TerminalView";

import styles from "./AgentsPage.module.css";

// Heavy tabs (CodeMirror/diff) are code-split, mirroring AgentDetailPanel.
const DiffTab = lazy(() =>
  import("@/components/AgentDetailPanel").then((m) => ({ default: m.DiffTab })),
);
const FileEditorPanel = lazy(() =>
  import("@/components/FileEditorPanel").then((m) => ({
    default: m.FileEditorPanel,
  })),
);

type AgentTab = "terminal" | "info" | "git" | "diff" | "files";

const TABS: { id: AgentTab; label: string }[] = [
  { id: "terminal", label: "Terminal" },
  { id: "info", label: "Info" },
  { id: "git", label: "Git" },
  { id: "diff", label: "Diff" },
  { id: "files", label: "Files" },
];

export function AgentsPage(): JSX.Element {
  return (
    <ErrorBoundary>
      <Suspense fallback={<LoadingSkeleton.Monitor />}>
        <AgentsPageInner />
      </Suspense>
    </ErrorBoundary>
  );
}

function AgentsPageInner(): JSX.Element {
  const { workspaceId = "", agentName } = useParams<{
    workspaceId: string;
    agentName?: string;
  }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const { issues } = useWorkspaceViewData();
  const { showToast } = useToast();

  // Auto-select an agent when URL is bare /agents: honor a legacy
  // ?agent=<name> deep link first, else fall back to the first rail agent.
  const queryAgent = searchParams.get("agent");
  const firstAgentName = useMemo(
    () => orderAgentsForEpicRunner(agents).find(isLiveAgentRailVisible)?.name,
    [agents],
  );
  useEffect(() => {
    if (agentName) return;
    const target = queryAgent ?? firstAgentName;
    if (target) {
      navigate(`/ws/${workspaceId}/agents/${encodeURIComponent(target)}`, {
        replace: true,
      });
    }
  }, [agentName, queryAgent, firstAgentName, navigate, workspaceId]);

  const selected = useMemo(
    () => (agentName ? agents.find((a) => a.name === agentName) : undefined),
    [agents, agentName],
  );

  const [activeTab, setActiveTab] = useState<AgentTab>("terminal");
  useEffect(() => {
    setActiveTab("terminal");
  }, [agentName]);

  // Inline task-detail selection. Cleared when the user switches agents so
  // the right column starts fresh on every agent change.
  const [selectedTask, setSelectedTask] = useState<Issue | null>(null);
  const [pendingTerminalInput, setPendingTerminalInput] = useState<
    TerminalInputRequest | undefined
  >(undefined);
  const [epicRunnerRuns, setEpicRunnerRuns] = useState<
    Record<string, WorkflowRun>
  >({});
  useEffect(() => {
    setSelectedTask(null);
  }, [agentName]);
  useEffect(() => {
    setEpicRunnerRuns({});
  }, [workspaceId]);

  // Live run-status updates: one SSE stream per active epic-runner run
  // (with slow-poll fallback inside the hook).
  const handleRunUpdate = useCallback(
    (epicId: string, run: WorkflowRun) => {
      setEpicRunnerRuns((prev) => {
        const current = prev[epicId];
        if (
          current &&
          current.status === run.status &&
          current.updated_at === run.updated_at &&
          current.last_heartbeat === run.last_heartbeat
        ) {
          return prev;
        }
        return { ...prev, [epicId]: run };
      });
      if (isTerminalWorkflowRunStatus(run.status)) {
        // Final refresh so workers spawned by the run settle into their
        // end-of-run state once the periodic refresh below stops.
        void agentStore.getState().fetchData();
      }
    },
    [agentStore],
  );
  useWorkflowRunStreams({
    workspaceId,
    runs: epicRunnerRuns,
    onRunUpdate: handleRunUpdate,
  });

  // Workers appear while a run is active, so keep refreshing the agent list
  // on a slow cadence independent of run-status updates. Ticks are skipped
  // while a previous fetch is still in flight, and the interval stops as
  // soon as no run is active.
  const hasActiveRuns = useMemo(
    () =>
      Object.values(epicRunnerRuns).some(
        (run) => run.run_id && !isTerminalWorkflowRunStatus(run.status),
      ),
    [epicRunnerRuns],
  );
  useEffect(() => {
    if (!workspaceId || !hasActiveRuns) return;
    let inFlight = false;
    const interval = window.setInterval(() => {
      if (inFlight) return;
      inFlight = true;
      void Promise.resolve(agentStore.getState().fetchData()).finally(() => {
        inFlight = false;
      });
    }, 5000);
    return () => window.clearInterval(interval);
  }, [agentStore, hasActiveRuns, workspaceId]);

  // Queue the built-in epic runner workflow for the selected lead. The
  // workflow owns lead validation, binding, and child-task reconciliation.
  const handleRunEpic = useCallback(
    async (epicId: string) => {
      if (!agentName) return;
      try {
        const run = await startWorkflowRun(
          workspaceId,
          EPIC_RUNNER_WORKFLOW_NAME,
          {
            epicId,
            leadName: agentName,
            requestedBy: "ui",
          },
        );
        setEpicRunnerRuns((prev) => ({ ...prev, [epicId]: run }));
        await agentStore.getState().fetchData();
        showToast(`Epic runner queued for ${agentName}: ${run.run_id}`, {
          type: "success",
        });
      } catch (err) {
        showToast(`Epic runner failed: ${(err as Error).message}`, {
          type: "error",
        });
      }
    },
    [agentName, workspaceId, showToast, agentStore],
  );

  const handleAgentClick = useCallback(
    (name: string) => {
      navigate(`/ws/${workspaceId}/agents/${encodeURIComponent(name)}`);
    },
    [navigate, workspaceId],
  );

  // Workspace-wide counts for the Info tab stat cards.
  const counts = useMemo(() => {
    let done = 0;
    let inProgress = 0;
    let review = 0;
    let blocked = 0;
    let queued = 0;
    for (const i of issues) {
      const s = i.status ?? "open";
      if (s === "closed") done += 1;
      else if (s === "in_progress") inProgress += 1;
      else if (s === "review") review += 1;
      else if (s === "blocked" || i.is_blocked) blocked += 1;
      else if (s === "open" || s === "deferred") queued += 1;
    }
    return { done, inProgress, review, blocked, queued };
  }, [issues]);

  const infoStats = [
    {
      id: "completed",
      label: "Tasks Completed",
      value: counts.done,
      tone: "success",
    },
    {
      id: "progress",
      label: "In Progress",
      value: counts.inProgress,
      tone: "warning",
    },
    { id: "blocked", label: "Blocked", value: counts.blocked, tone: "danger" },
    { id: "queued", label: "Queued", value: counts.queued, tone: "info" },
  ];

  const statusType = parseLoomStatus(selected?.status ?? "").type;
  const roleName = selected?.role ?? statusType;
  const selColor = getAvatarColor(selected?.name ?? "agent");
  const selText = shouldUseWhiteText(selColor) ? "#fff" : "#171717";

  return (
    <div className={styles.page} data-testid="agents-page">
      {/* Main panel: Aether tab strip over the live agent surfaces */}
      <section className={styles.main} aria-label="Agent details">
        <nav className={styles.tabBar} aria-label="Agent detail tabs">
          {TABS.map((tab) => (
            <button
              key={tab.id}
              type="button"
              className={styles.tab}
              data-active={activeTab === tab.id || undefined}
              onClick={() => setActiveTab(tab.id)}
              aria-current={activeTab === tab.id ? "page" : undefined}
            >
              {tab.label}
            </button>
          ))}
        </nav>

        {/* Terminal stays mounted across tab switches so the PTY websocket
            and xterm scrollback survive; other tabs mount on demand. */}
        <div
          className={styles.realTabBody}
          style={activeTab === "terminal" ? undefined : { display: "none" }}
        >
          <AgentDetailMain
            agentName={agentName}
            pendingTerminalInput={pendingTerminalInput}
            onTerminalInputConsumed={() => setPendingTerminalInput(undefined)}
          />
        </div>

        {activeTab === "info" && selected && (
          <div className={styles.scrollPanel}>
            <section className={styles.card}>
              <div className={styles.infoHead}>
                <span
                  className={styles.infoAvatar}
                  style={{ backgroundColor: selColor, color: selText }}
                >
                  {selected.name.charAt(0).toUpperCase()}
                </span>
                <div>
                  <h1 className={styles.agentName}>{selected.name}</h1>
                  <p className={styles.infoSub}>
                    {formatStatusLabel(roleName)} agent · isolated workspace
                    runtime
                  </p>
                </div>
              </div>
              <dl className={styles.statGrid}>
                {infoStats.map((s) => (
                  <div key={s.id} className={styles.statCard}>
                    <dt className={styles.statLabel}>{s.label}</dt>
                    <dd className={styles.statValue} data-tone={s.tone}>
                      {s.value}
                    </dd>
                  </div>
                ))}
              </dl>
            </section>
            <section className={styles.card}>
              <h2 className={styles.cardLabel}>Agent Info</h2>
              <dl className={styles.configGrid}>
                <div>
                  <dt>Status</dt>
                  <dd>{formatStatusLabel(statusType)}</dd>
                </div>
                <div>
                  <dt>Role</dt>
                  <dd>{formatStatusLabel(roleName)}</dd>
                </div>
                <div>
                  <dt>Branch</dt>
                  <dd>{selected.branch ?? "—"}</dd>
                </div>
                <div>
                  <dt>Scope</dt>
                  <dd>
                    {selected.cross_repo ? "All repos" : (selected.repo ?? "—")}
                  </dd>
                </div>
                {selected.worktree_path ? (
                  <div>
                    <dt>Worktree</dt>
                    <dd>{selected.worktree_path}</dd>
                  </div>
                ) : null}
                {selected.workspace ? (
                  <div>
                    <dt>Workspace</dt>
                    <dd>{selected.workspace}</dd>
                  </div>
                ) : null}
              </dl>
            </section>
          </div>
        )}

        {activeTab === "git" && selected && (
          <div className={styles.realTabBody}>
            <GitTab agent={selected} isActive={activeTab === "git"} />
          </div>
        )}

        {activeTab === "diff" && selected && (
          <div className={styles.realTabBody}>
            <Suspense
              fallback={<div className={styles.tabFallback}>Loading diff…</div>}
            >
              <DiffTab agent={selected} isActive={activeTab === "diff"} />
            </Suspense>
          </div>
        )}

        {activeTab === "files" && selected && (
          <div className={styles.realTabBody}>
            <Suspense
              fallback={
                <div className={styles.tabFallback}>Loading files…</div>
              }
            >
              <FileEditorPanel
                agentName={selected.name}
                isActive={activeTab === "files"}
              />
            </Suspense>
          </div>
        )}
      </section>

      {/* Right column: epic-runner Open Queue or inline task detail */}
      {selectedTask ? (
        <div className={styles.inlineDetail}>
          <IssueDetailPanel
            inline
            isOpen={true}
            issue={selectedTask}
            onClose={() => setSelectedTask(null)}
          />
        </div>
      ) : (
        <AgentWorkPanel
          agentName={agentName}
          epicRunnerRuns={epicRunnerRuns}
          onTaskClick={(task) => setSelectedTask(task)}
          onRunEpic={handleRunEpic}
          onAgentClick={handleAgentClick}
        />
      )}
    </div>
  );
}
