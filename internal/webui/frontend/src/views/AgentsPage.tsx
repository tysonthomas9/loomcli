/**
 * AgentsPage — full-page agent workspace (Aether `/agents`).
 *
 * Layout (driven by App.tsx):
 *   [WorkspaceTree — same sidebar as kanban]
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
 *   restores the work panel. Per-agent task selection is restored from
 *   scoped localStorage when switching back to an agent.
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
import { AgentConfigModal } from "@/components/AgentConfigModal";
import { WorkflowAgentDetail } from "@/components/WorkflowAgentDetail";
import { GitTab } from "@/components/AgentDetailPanel";
import { AgentWorkPanel } from "@/components/AgentWorkPanel/AgentWorkPanel";
import { PanelWidthResizeHandle } from "@/components/AgentWorkPanel/PanelWidthResizeHandle";
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
import {
  useWorkspaceViewActions,
  useWorkspaceViewData,
} from "@/contexts/WorkspaceViewContext";
import { useAgentStoreInstance } from "@/hooks";
import {
  useAutomations,
  useLocalSettings,
  useWorkspaceContext,
} from "@/hooks/workspace";
import {
  OPEN_QUEUE_PANEL_MAX_WIDTH,
  OPEN_QUEUE_PANEL_MIN_WIDTH,
  useOpenQueuePanelWidth,
} from "@/hooks/ui/useOpenQueuePanelWidth";
import { useToast } from "@/hooks/ui/useToast";
import { useWorkflowRunStreams } from "@/hooks/workflows/useWorkflowRunStreams";
import { parseLoomStatus } from "@/types";
import type { Issue } from "@/types";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import {
  epicRunnerRuntimePayload,
  issueRepoName,
} from "@/utils/epicRunnerPayload";
import { isCustomRole } from "@/utils/agentRole";
import { formatStatusLabel } from "@/utils/issue";
import type { TerminalInputRequest } from "@/components/TerminalView/TerminalView";

import {
  loadAgentWorkPanelView,
  saveAgentWorkPanelView,
} from "@/utils/agentWorkPanelStorage";

import { AgentEditorGroups, type AgentEditorTab } from "./AgentEditorGroups";
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
  const { issues, issueDetails, isLoadingDetails, detailError } =
    useWorkspaceViewData();
  const {
    fetchIssue,
    clearIssue,
    updateIssueDetails,
    handleApprove,
    handleReject,
    handleCopyLink,
  } = useWorkspaceViewActions();
  const { repos } = useWorkspaceContext();
  const { settings: localSettings } = useLocalSettings();
  const { showToast } = useToast();
  const {
    width: openQueueWidth,
    applyDelta: applyOpenQueueWidthDelta,
    resetWidth: resetOpenQueueWidth,
  } = useOpenQueuePanelWidth(workspaceId);

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

  // Route resolution (Decision: agent-store name first, then binding id). When
  // the URL segment is not a role agent, it may be a trigger-binding "agent";
  // resolve it from the automations list and render the workflow-agent detail.
  const {
    bindings,
    initialized: bindingsInitialized,
    setEnabled: setBindingEnabled,
    runWorkflow,
  } = useAutomations(workspaceId, !!workspaceId);
  const selectedBinding = useMemo(
    () =>
      agentName && !selected
        ? bindings.find((b) => b.binding_id === agentName)
        : undefined,
    [agentName, selected, bindings],
  );
  // While the URL points at an unknown name and bindings have not yet loaded,
  // hold the shell (don't flash the role terminal for a name that is a binding).
  const resolvingBinding =
    !!agentName && !selected && !selectedBinding && !bindingsInitialized;

  // Inline task-detail selection, restored per agent from scoped storage.
  const [selectedTask, setSelectedTask] = useState<Issue | null>(null);
  const [showAgentConfig, setShowAgentConfig] = useState(false);
  const [pendingTerminalInput, setPendingTerminalInput] = useState<
    TerminalInputRequest | undefined
  >(undefined);
  const [epicRunnerRuns, setEpicRunnerRuns] = useState<
    Record<string, WorkflowRun>
  >({});

  const persistSelectedTaskId = useCallback(
    (taskId: string | null) => {
      if (!agentName) return;
      saveAgentWorkPanelView(workspaceId, agentName, {
        selectedTaskId: taskId,
      });
    },
    [agentName, workspaceId],
  );

  useEffect(() => {
    clearIssue();
    if (!agentName) {
      setSelectedTask(null);
      return;
    }
    const { selectedTaskId } = loadAgentWorkPanelView(workspaceId, agentName);
    if (!selectedTaskId) {
      setSelectedTask(null);
      return;
    }
    const match = issues.find((issue) => issue.id === selectedTaskId);
    setSelectedTask(match ?? null);
  }, [agentName, workspaceId, issues, clearIssue]);
  useEffect(() => {
    if (!selectedTask) return;
    void fetchIssue(selectedTask.id);
  }, [selectedTask, fetchIssue]);
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
        const epic = issues.find((issue) => issue.id === epicId);
        const runtimePayload = epicRunnerRuntimePayload({
          localSettings,
          repos,
          currentRepo: issueRepoName(epic),
        });
        const run = await startWorkflowRun(
          workspaceId,
          EPIC_RUNNER_WORKFLOW_NAME,
          {
            epicId,
            leadName: agentName,
            requestedBy: "ui",
            ...runtimePayload,
          },
        );
        setEpicRunnerRuns((prev) => ({ ...prev, [epicId]: run }));
        await agentStore.getState().fetchData();
        showToast(`Epic runner queued for ${agentName}: ${run.run_id}`, {
          type: "success",
        });
      } catch (err) {
        // Surface server-side failures, including the fail-closed preflight
        // rejection (e.g. backend CLI/auth missing) that startWorkflowRun
        // raises as an ApiError carrying the server's error message. Never
        // swallow it and proceed as if the run was queued.
        showToast(`Epic runner failed: ${(err as Error).message}`, {
          type: "error",
        });
      }
    },
    [
      agentName,
      workspaceId,
      issues,
      localSettings,
      repos,
      showToast,
      agentStore,
    ],
  );

  const handleAgentClick = useCallback(
    (name: string) => {
      navigate(`/ws/${workspaceId}/agents/${encodeURIComponent(name)}`);
    },
    [navigate, workspaceId],
  );

  const handleCloseInlineDetail = useCallback(() => {
    setSelectedTask(null);
    clearIssue();
    persistSelectedTaskId(null);
  }, [clearIssue, persistSelectedTaskId]);

  const handleInlineTaskNavigate = useCallback(
    (issue: Issue) => {
      setSelectedTask(issue);
      persistSelectedTaskId(issue.id);
    },
    [persistSelectedTaskId],
  );

  const handleTaskClick = useCallback(
    (task: Issue) => {
      setSelectedTask(task);
      persistSelectedTaskId(task.id);
    },
    [persistSelectedTaskId],
  );

  const inlinePanelIssue = useMemo(() => {
    if (!selectedTask) return null;
    if (issueDetails?.id === selectedTask.id) return issueDetails;
    return selectedTask;
  }, [selectedTask, issueDetails]);

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

  const infoStats = useMemo(
    () => [
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
    ],
    [counts],
  );

  const statusType = parseLoomStatus(selected?.status ?? "").type;
  const roleName = selected?.role ?? statusType;
  // The agent's actual role (no status fallback) — gates the Phase B edit surface.
  const selectedRole = (selected?.role ?? "").trim();
  const canEditConfig = isCustomRole(selectedRole);
  const selColor = getAvatarColor(selected?.name ?? "agent");
  const selText = shouldUseWhiteText(selColor) ? "#fff" : "#171717";

  const renderAgentPane = useCallback(
    (tab: AgentEditorTab, isActive: boolean) => {
      switch (tab) {
        case "terminal":
          return (
            <div className={styles.realTabBody}>
              <AgentDetailMain
                agentName={agentName}
                pendingTerminalInput={pendingTerminalInput}
                onTerminalInputConsumed={() =>
                  setPendingTerminalInput(undefined)
                }
              />
            </div>
          );
        case "info":
          if (!selected) {
            return (
              <div className={styles.tabFallback}>
                Select an agent to view info.
              </div>
            );
          }
          return (
            <div className={styles.scrollPanel}>
              <section className={styles.card}>
                <div className={styles.infoHead}>
                  <span
                    className={styles.infoAvatar}
                    style={{ backgroundColor: selColor, color: selText }}
                  >
                    {getCompactAvatarInitials(selected.name)}
                  </span>
                  <div>
                    <h1 className={styles.agentName}>{selected.name}</h1>
                    <p className={styles.infoSub}>
                      {formatStatusLabel(roleName)} agent
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
                      {selected.cross_repo
                        ? "All repos"
                        : (selected.repo ?? "—")}
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
              {canEditConfig && (
                <section className={styles.card}>
                  <button
                    type="button"
                    className={styles.configButton}
                    data-testid="agents-page-edit-config"
                    onClick={() => setShowAgentConfig(true)}
                  >
                    Edit configuration
                  </button>
                </section>
              )}
            </div>
          );
        case "git":
          if (!selected) {
            return (
              <div className={styles.tabFallback}>
                Select an agent to view git.
              </div>
            );
          }
          return (
            <div
              className={`${styles.realTabBody} ${styles.realTabBodyScroll}`}
            >
              <GitTab agent={selected} isActive={isActive} />
            </div>
          );
        case "diff":
          if (!selected) {
            return (
              <div className={styles.tabFallback}>
                Select an agent to view diff.
              </div>
            );
          }
          return (
            <div className={styles.realTabBody}>
              <Suspense
                fallback={
                  <div className={styles.tabFallback}>Loading diff…</div>
                }
              >
                <DiffTab agent={selected} isActive={isActive} />
              </Suspense>
            </div>
          );
        case "files":
          if (!selected) {
            return (
              <div className={styles.tabFallback}>
                Select an agent to browse files.
              </div>
            );
          }
          return (
            <div className={styles.realTabBody}>
              <Suspense
                fallback={
                  <div className={styles.tabFallback}>Loading files…</div>
                }
              >
                <FileEditorPanel
                  agentName={selected.name}
                  agentRole={selected.role}
                  agentRepo={selected.repo}
                  isActive={isActive}
                />
              </Suspense>
            </div>
          );
        default:
          return null;
      }
    },
    [
      agentName,
      pendingTerminalInput,
      selected,
      selColor,
      selText,
      roleName,
      infoStats,
      statusType,
      canEditConfig,
    ],
  );

  // Workflow-plane agent (trigger binding): same page shell, capability-based
  // content (runs + config, no worktree). Role-agent rendering below is
  // unchanged. Resolution order already preferred a role agent when the name
  // matched one, so this branch only fires for a genuine binding id.
  if (selectedBinding) {
    return (
      <div className={styles.page} data-testid="agents-page">
        <section className={styles.main} aria-label="Agent details">
          <WorkflowAgentDetail
            workspaceId={workspaceId}
            binding={selectedBinding}
            onSetEnabled={setBindingEnabled}
            onRunWorkflow={runWorkflow}
          />
        </section>
      </div>
    );
  }

  if (resolvingBinding) {
    return (
      <div className={styles.page} data-testid="agents-page">
        <section className={styles.main} aria-label="Agent details">
          <div className={styles.tabFallback}>Loading agent…</div>
        </section>
      </div>
    );
  }

  return (
    <div className={styles.page} data-testid="agents-page">
      {/* Main panel: Aether tab strip over the live agent surfaces */}
      <section className={styles.main} aria-label="Agent details">
        <AgentEditorGroups resetKey={agentName} renderPane={renderAgentPane} />
      </section>

      {/* Right column: epic-runner Open Queue or inline task detail */}
      {selectedTask ? (
        <div className={styles.inlineDetail} style={{ width: openQueueWidth }}>
          <PanelWidthResizeHandle
            width={openQueueWidth}
            onDelta={applyOpenQueueWidthDelta}
            onReset={resetOpenQueueWidth}
            minWidth={OPEN_QUEUE_PANEL_MIN_WIDTH}
            maxWidth={OPEN_QUEUE_PANEL_MAX_WIDTH}
          />
          <div className={styles.inlineDetailContent}>
            <IssueDetailPanel
              inline
              isOpen={true}
              issue={inlinePanelIssue}
              isLoading={isLoadingDetails}
              error={detailError}
              onClose={handleCloseInlineDetail}
              onApprove={handleApprove}
              onReject={handleReject}
              onIssueUpdate={updateIssueDetails}
              onCopyLink={handleCopyLink}
              onNavigateToIssue={handleInlineTaskNavigate}
            />
          </div>
        </div>
      ) : (
        <AgentWorkPanel
          agentName={agentName}
          panelWidth={openQueueWidth}
          onPanelWidthDelta={applyOpenQueueWidthDelta}
          onPanelWidthReset={resetOpenQueueWidth}
          epicRunnerRuns={epicRunnerRuns}
          onTaskClick={handleTaskClick}
          onRunEpic={handleRunEpic}
          onAgentClick={handleAgentClick}
        />
      )}
      {selected && canEditConfig && (
        <AgentConfigModal
          isOpen={showAgentConfig}
          workspaceId={workspaceId}
          agentName={selected.name}
          roleName={selectedRole}
          onClose={() => setShowAgentConfig(false)}
          onDeleted={() => {
            setShowAgentConfig(false);
            navigate(`/ws/${workspaceId}/agents`);
          }}
        />
      )}
    </div>
  );
}
