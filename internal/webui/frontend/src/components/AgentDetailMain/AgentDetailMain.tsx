/**
 * AgentDetailMain — Direction J middle panel (live terminal).
 *
 * Renders a slim agent header (avatar, name, status, branch, role) plus an
 * embedded TerminalView that auto-focuses the selected agent's tmux session
 * via pendingAgentName. When the user switches agents in the rail, the
 * pendingAgentName change tells TerminalView to switch tabs to that agent.
 *
 * The App-level TerminalView is conditionally skipped when activeView ===
 * "agents" (see App.tsx) so only one TerminalView instance is mounted at a
 * time. Switching between /terminal and /agents causes one reconnect, but
 * within the agents view itself the terminal stays mounted across rail
 * selection changes (only pendingAgentName updates).
 */

import {
  Fragment,
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";
import { useStore } from "zustand";

import { LoadingSkeleton } from "@/components";
import type {
  TerminalInputRequest,
  TerminalSplitControls,
} from "@/components/TerminalView";
import { useAgentStoreInstance } from "@/hooks";
import {
  getAgentLifecycleCommand,
  restartAgent,
  startAgent,
  stopAgent,
  wsUrl,
  type AgentLifecycleCommandResult,
  type AgentLifecycleCommandStatus,
  type AgentLifecycleRequestResult,
} from "@/hooks/api";
import { useToast } from "@/hooks/ui/useToast";
import { ApiError, type LoomAgentStatus, parseLoomStatus } from "@/types";
import { isInteractiveAgent, isLeadRole } from "@/utils/agentRole";
import {
  agentDisplayRoleLabel,
  agentDisplayTitle,
  agentUsesLiteralTitle,
} from "@/utils/agentDisplay";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

import {
  acquireAgentLifecycleSubmission,
  clearPendingAgentLifecycleCommand,
  isAgentLifecycleSubmissionLocked,
  loadPendingAgentLifecycleCommand,
  markPendingAgentLifecycleWarningShown,
  releaseAgentLifecycleSubmission,
  savePendingAgentLifecycleCommand,
  subscribeAgentLifecyclePending,
  type AgentLifecycleAction,
  type PendingAgentLifecycleCommand,
} from "./agentLifecyclePending";

const TerminalView = lazy(() =>
  import("@/components/TerminalView").then((m) => ({
    default: m.TerminalView,
  })),
);

interface AgentDetailMainProps {
  agentName: string | undefined;
  pendingTerminalInput?: TerminalInputRequest | undefined;
  onTerminalInputConsumed?: (() => void) | undefined;
  onTerminalSplitControlsChange?: (
    controls: TerminalSplitControls | null,
  ) => void;
}

const STATUS_DOT_COLOR: Record<string, string> = {
  ready: "var(--color-status-ready, #aab)",
  working: "var(--color-status-working, #d99700)",
  planning: "var(--color-status-planning, #d99700)",
  review: "var(--color-status-review, #4477aa)",
  done: "var(--color-status-done, #3aa76d)",
  idle: "var(--color-status-idle, #888)",
  error: "var(--color-status-error, #d14545)",
  dirty: "var(--color-status-warn, #c96442)",
  changes: "var(--color-status-warn, #c96442)",
};

export function AgentDetailMain({
  agentName,
  pendingTerminalInput,
  onTerminalInputConsumed,
  onTerminalSplitControlsChange,
}: AgentDetailMainProps): JSX.Element {
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  const agent = useMemo<LoomAgentStatus | undefined>(
    () => agents.find((a) => a.name === agentName),
    [agents, agentName],
  );

  // Drive TerminalView's pendingAgentName from rail selection. Cleared via the
  // onAgentNameConsumed callback so TerminalView only switches tabs once per
  // selection change.
  const [pendingAgentName, setPendingAgentName] = useState<string | undefined>(
    agentName,
  );
  useEffect(() => {
    if (agentName) setPendingAgentName(agentName);
  }, [agentName]);
  const handleAgentNameConsumed = useCallback(
    () => setPendingAgentName(undefined),
    [],
  );
  const interactiveAgent = agent != null && isInteractiveAgent(agent);
  const daemonSupervisedWorker = agent != null && !interactiveAgent;
  const terminalUnavailable = agent != null && isTerminalUnavailable(agent);
  const ephemeralWorker = agent != null && isEphemeralWorker(agent);
  const shouldResolveLeadTerminal = interactiveAgent && terminalUnavailable;
  const terminalEmptyState =
    agent != null && terminalUnavailable
      ? terminalUnavailableEmptyState(agent)
      : null;
  const terminalAgentName =
    !daemonSupervisedWorker && pendingAgentName === agentName
      ? pendingAgentName
      : undefined;

  if (!agentName) {
    return (
      <EmptyState
        message="Select an agent"
        detail="Pick an agent from the rail to attach to their terminal session."
      />
    );
  }

  return (
    <div
      style={{
        flex: 1,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        borderRight: "1px solid var(--color-border, #ddd)",
        background: "var(--color-bg, #fdfcf8)",
        minHeight: 0,
      }}
    >
      <Header
        agent={agent}
        agentName={agentName}
        onRefresh={() => {
          void agentStore.getState().fetchData();
        }}
      />
      <div
        style={{
          flex: 1,
          minHeight: 0,
          minWidth: 0,
          display: "flex",
          flexDirection: "column",
          position: "relative",
        }}
      >
        {ephemeralWorker ? (
          <EphemeralWorkerSummary agent={agent} />
        ) : daemonSupervisedWorker ? (
          <EmptyState
            message="Worker terminal unavailable"
            detail="This worker is daemon-supervised. Use worker logs or task session history instead of starting a second terminal process."
          />
        ) : terminalUnavailable && !shouldResolveLeadTerminal ? (
          <EmptyState
            message={terminalEmptyState?.message ?? "Agent is stopped"}
            detail={
              terminalEmptyState?.detail ??
              "This agent does not have a live terminal session. Start the agent before attaching to its PTY."
            }
          />
        ) : (
          <Suspense fallback={<LoadingSkeleton.Terminal />}>
            <TerminalView
              isActive={true}
              pendingAgentName={terminalAgentName}
              onAgentNameConsumed={handleAgentNameConsumed}
              pendingTerminalInput={pendingTerminalInput}
              onTerminalInputConsumed={onTerminalInputConsumed}
              hideTabs
              {...(onTerminalSplitControlsChange != null && {
                onSplitControlsChange: onTerminalSplitControlsChange,
              })}
            />
          </Suspense>
        )}
      </div>
    </div>
  );
}

function isTerminalUnavailable(agent: LoomAgentStatus): boolean {
  const state = (agent.state ?? "").toLowerCase();
  const desiredState = (agent.desired_state ?? "").toLowerCase();
  return state === "stopped" || state === "dead" || desiredState === "stopped";
}

function terminalUnavailableEmptyState(_agent: LoomAgentStatus): {
  message: string;
  detail: string;
} {
  return {
    message: "Agent is stopped",
    detail:
      "This agent does not have a live terminal session. Start the agent before attaching to its PTY.",
  };
}

function isEphemeralWorker(agent: LoomAgentStatus): boolean {
  return agent.mode === "ephemeral" && !isInteractiveAgent(agent);
}

function EphemeralWorkerSummary({
  agent,
}: {
  agent: LoomAgentStatus;
}): JSX.Element {
  const taskId =
    agent.task_id || parseLoomStatus(agent.status ?? "").taskId || "unknown";
  const workspace = agent.workspace || "";
  const sessionId = agent.session_id || "";
  const running = !isTerminalUnavailable(agent);
  const stateLabel =
    agent.state ||
    agent.desired_state ||
    parseLoomStatus(agent.status ?? "").type ||
    "unknown";
  const hasTaskSession = taskId !== "unknown" && sessionId !== "";
  const logsHref =
    workspace !== ""
      ? wsUrl(workspace, `/agents/${encodeURIComponent(agent.name)}/logs`)
      : "";
  const transcriptHref =
    workspace !== "" && hasTaskSession
      ? wsUrl(
          workspace,
          `/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/transcript`,
        )
      : "";
  const diffHref =
    workspace !== "" && hasTaskSession
      ? wsUrl(
          workspace,
          `/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/diff`,
        )
      : "";
  const openHref = (href: string) => {
    if (!href) return;
    window.open(href, "_blank", "noopener,noreferrer");
  };
  const actionButtonStyle: CSSProperties = {
    minHeight: 30,
    padding: "0 10px",
    border: "1px solid var(--color-border, #ddd)",
    borderRadius: 4,
    background: "var(--color-bg, #fdfcf8)",
    color: "var(--color-text-primary, #333)",
    fontSize: 12,
    fontWeight: 600,
    cursor: "pointer",
  };
  const disabledActionButtonStyle: CSSProperties = {
    ...actionButtonStyle,
    opacity: 0.55,
    cursor: "not-allowed",
  };
  return (
    <div
      style={{
        flex: 1,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        padding: 24,
        color: "var(--color-text-primary, #333)",
      }}
    >
      <div
        style={{
          width: "min(520px, 100%)",
          border: "1px solid var(--color-border, #ddd)",
          borderRadius: 6,
          background: "var(--color-bg-card, #fff)",
          padding: 18,
          display: "flex",
          flexDirection: "column",
          gap: 10,
        }}
      >
        <div
          style={{
            fontSize: 11,
            fontWeight: 700,
            letterSpacing: 0.4,
            textTransform: "uppercase",
            color: "var(--color-text-muted, #666)",
          }}
        >
          Ephemeral worker attempt
        </div>
        <div
          style={{
            fontSize: 18,
            fontWeight: 700,
            textTransform: "capitalize",
          }}
        >
          {agent.name}
        </div>
        <div
          style={{
            display: "grid",
            gridTemplateColumns: "auto minmax(0, 1fr)",
            gap: "6px 12px",
            fontSize: 12,
          }}
        >
          <span style={{ color: "var(--color-text-muted, #666)" }}>Task</span>
          <code>{taskId}</code>
          <span style={{ color: "var(--color-text-muted, #666)" }}>Epic</span>
          <code>{agent.parent || "unknown"}</code>
          <span style={{ color: "var(--color-text-muted, #666)" }}>
            Session
          </span>
          <code>{agent.session_id || "not recorded"}</code>
          <span style={{ color: "var(--color-text-muted, #666)" }}>State</span>
          <span>{stateLabel}</span>
        </div>
        <div
          style={{
            fontSize: 12,
            lineHeight: 1.5,
            color: "var(--color-text-secondary, #555)",
          }}
        >
          {running
            ? "This daemon-owned ephemeral worker is already running under Loom. Live terminal attach is disabled so the UI does not launch a duplicate worker; use logs while it runs."
            : "This daemon-owned ephemeral worker has stopped. Live terminal attach is disabled after the ephemeral run stops; use logs and task session artifacts for review."}
        </div>
        <div
          style={{
            display: "flex",
            flexWrap: "wrap",
            gap: 8,
            paddingTop: 2,
          }}
        >
          <button
            type="button"
            style={logsHref ? actionButtonStyle : disabledActionButtonStyle}
            disabled={!logsHref}
            title={logsHref ? "Open worker logs" : "Workspace is not recorded"}
            onClick={() => openHref(logsHref)}
          >
            Open logs
          </button>
          <button
            type="button"
            style={
              transcriptHref ? actionButtonStyle : disabledActionButtonStyle
            }
            disabled={!transcriptHref}
            title={
              transcriptHref
                ? "Open session transcript"
                : "Task session is not recorded"
            }
            onClick={() => openHref(transcriptHref)}
          >
            Open transcript
          </button>
          <button
            type="button"
            style={diffHref ? actionButtonStyle : disabledActionButtonStyle}
            disabled={!diffHref}
            title={
              diffHref ? "Open session diff" : "Task session is not recorded"
            }
            onClick={() => openHref(diffHref)}
          >
            Open diff
          </button>
          <button
            type="button"
            style={disabledActionButtonStyle}
            disabled
            title="Worktree cleanup is unavailable until retention metadata is published"
          >
            Delete worktree
          </button>
          <button
            type="button"
            style={disabledActionButtonStyle}
            disabled
            title="Artifact archive is unavailable until artifact storage is published"
          >
            Archive artifacts
          </button>
          <button
            type="button"
            style={disabledActionButtonStyle}
            disabled
            title="Rerun creates a new attempt once retry controls are available"
          >
            Rerun task
          </button>
        </div>
      </div>
    </div>
  );
}

function Header({
  agent,
  agentName,
  onRefresh,
}: {
  agent: LoomAgentStatus | undefined;
  agentName: string;
  onRefresh: () => void;
}): JSX.Element {
  const parsed = useMemo(
    () => parseLoomStatus(agent?.status ?? ""),
    [agent?.status],
  );
  const dotColor =
    STATUS_DOT_COLOR[parsed.type] ?? "var(--color-status-idle, #888)";
  const initial = getCompactAvatarInitials(agentName);
  const avatarBg = getAvatarColor(agentName);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";
  const branch = displayBranch(agent?.branch);
  const role = (agent?.role ?? "").trim();
  const assignedEpic = (agent?.parent ?? "").trim();
  const isLead = isLeadRole(agent?.role);
  const deliveryLabel = isLead
    ? leadDeliveryStateLabel(agent?.delivery_state)
    : "";
  const queuedInbox = Math.max(0, Number(agent?.inbox_queued_count ?? 0));
  const failedInbox = Math.max(0, Number(agent?.inbox_failed_count ?? 0));
  const inboxLabel =
    queuedInbox > 0
      ? `${queuedInbox} queued message${queuedInbox === 1 ? "" : "s"}`
      : failedInbox > 0
        ? `${failedInbox} failed message${failedInbox === 1 ? "" : "s"}`
        : "";
  const hideIdleLeadStatus = isLead && parsed.type === "idle";
  const titleAgent = agent ?? {
    name: agentName,
    branch: "",
    status: "",
    ahead: 0,
    behind: 0,
  };
  const roleLabel = role ? agentDisplayRoleLabel(titleAgent) : "";
  const title = agentDisplayTitle(titleAgent);
  const literalTitle = agentUsesLiteralTitle(titleAgent);
  const metaSegments: Array<{ key: string; node: ReactNode }> = [];

  if (!hideIdleLeadStatus) {
    metaSegments.push({
      key: "status",
      node: (
        <>
          <span
            aria-hidden="true"
            style={{
              width: 7,
              height: 7,
              borderRadius: "50%",
              background: dotColor,
              display: "inline-block",
            }}
          />
          <span>{parsed.type || "unknown"}</span>
        </>
      ),
    });
  }

  if (branch) {
    metaSegments.push({
      key: "branch",
      node: (
        <code
          style={{
            fontFamily: "var(--font-mono, ui-monospace, monospace)",
          }}
        >
          {branch}
        </code>
      ),
    });
  }

  if (roleLabel) {
    metaSegments.push({
      key: "role",
      node: <span>{roleLabel}</span>,
    });
  }

  if (assignedEpic) {
    metaSegments.push({
      key: "epic-label",
      node: <span>{isLead ? "Assigned epic" : "assigned epic"}</span>,
    });
    metaSegments.push({
      key: "epic-id",
      node: (
        <code
          style={{
            fontFamily: "var(--font-mono, ui-monospace, monospace)",
          }}
        >
          {assignedEpic}
        </code>
      ),
    });
    if (deliveryLabel) {
      metaSegments.push({
        key: "delivery",
        node: <span>{deliveryLabel}</span>,
      });
    }
  } else if (isLead) {
    metaSegments.push({
      key: "no-epic",
      node: <span>No epic assigned</span>,
    });
  }

  if (parsed.taskId) {
    metaSegments.push({
      key: "task",
      node: (
        <code
          style={{
            fontFamily: "var(--font-mono, ui-monospace, monospace)",
          }}
        >
          {parsed.taskId}
        </code>
      ),
    });
  }

  if (inboxLabel) {
    metaSegments.push({
      key: "inbox",
      node: (
        <span title={agent?.inbox_latest_message || inboxLabel}>
          {inboxLabel}
        </span>
      ),
    });
  }

  return (
    <div
      style={{
        padding: "10px 16px",
        borderBottom: "1px solid var(--color-border, #ddd)",
        display: "flex",
        alignItems: "center",
        gap: 10,
        flexShrink: 0,
      }}
    >
      <span
        aria-hidden="true"
        style={{
          width: 32,
          height: 32,
          borderRadius: "50%",
          background: avatarBg,
          color: avatarFg,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontWeight: 700,
          fontSize: initial.length > 1 ? 10 : 14,
          letterSpacing: initial.length > 1 ? "-0.02em" : undefined,
          flexShrink: 0,
          border: "1px solid rgba(0,0,0,0.18)",
        }}
      >
        {initial}
      </span>
      <div
        style={{
          display: "flex",
          flexDirection: "column",
          flex: 1,
          minWidth: 0,
        }}
      >
        <div
          style={{
            fontSize: 14,
            fontWeight: 700,
            textTransform: literalTitle ? "none" : "capitalize",
          }}
        >
          {title}
        </div>
        <div
          style={{
            fontSize: 11,
            color: "var(--color-text-muted, #666)",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
          {metaSegments.map((segment, index) => (
            <Fragment key={segment.key}>
              {index > 0 ? <span>·</span> : null}
              {segment.node}
            </Fragment>
          ))}
        </div>
      </div>
      <AgentLifecycleControls agent={agent} onChanged={onRefresh} />
    </div>
  );
}

/**
 * Stop / Start / Restart controls for a Go role agent, shown in the agent
 * header (parity with the workflow-agent detail's Enable/Disable bar). Backed
 * by the agentcontrol HTTP surface. Hidden for daemon-owned ephemeral workers
 * (rendered read-only elsewhere) and when the workspace key is unknown.
 */
export function AgentLifecycleControls({
  agent,
  onChanged,
}: {
  agent: LoomAgentStatus | undefined;
  onChanged: () => void;
}): JSX.Element | null {
  const { showToast } = useToast();
  const [requestInFlight, setRequestInFlight] = useState(false);
  const workspace = (agent?.workspace ?? "").trim();
  const agentName = agent?.name ?? "";
  const identity = lifecyclePendingIdentity(workspace, agentName);
  const [submissionLocked, setSubmissionLocked] = useState(() =>
    identity === ""
      ? false
      : isAgentLifecycleSubmissionLocked(workspace, agentName),
  );
  const [pendingState, setPendingState] = useState<{
    identity: string;
    pending: PendingAgentLifecycleCommand | null;
  }>(() => ({
    identity,
    pending:
      identity === ""
        ? null
        : loadPendingAgentLifecycleCommand(workspace, agentName),
  }));
  const restoredPending = useMemo(
    () =>
      identity === ""
        ? null
        : loadPendingAgentLifecycleCommand(workspace, agentName),
    [agentName, identity, workspace],
  );
  const pending =
    pendingState.identity === identity ? pendingState.pending : restoredPending;
  const lifecycleLocked = useRef(false);
  const onChangedRef = useRef(onChanged);
  const showToastRef = useRef(showToast);
  const mountedRef = useRef(true);

  useEffect(() => {
    onChangedRef.current = onChanged;
    showToastRef.current = showToast;
  }, [onChanged, showToast]);

  useEffect(() => {
    if (pendingState.identity !== identity) {
      setPendingState({ identity, pending: restoredPending });
      setSubmissionLocked(
        identity === ""
          ? false
          : isAgentLifecycleSubmissionLocked(workspace, agentName),
      );
    }
  }, [agentName, identity, pendingState.identity, restoredPending, workspace]);

  useEffect(() => {
    if (identity === "") return;
    const synchronize = () => {
      const stored = loadPendingAgentLifecycleCommand(workspace, agentName);
      setPendingState((currentState) => {
        if (currentState.identity !== identity) {
          return { identity, pending: stored };
        }
        if (
          currentState.pending?.commandId === stored?.commandId &&
          currentState.pending?.warningShown === stored?.warningShown
        ) {
          return currentState;
        }
        return { identity, pending: stored };
      });
      setSubmissionLocked(
        isAgentLifecycleSubmissionLocked(workspace, agentName),
      );
    };
    synchronize();
    return subscribeAgentLifecyclePending(workspace, agentName, synchronize);
  }, [agentName, identity, workspace]);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    if (pending == null || pending.warningShown) return;
    const warningDelay = Math.max(
      0,
      pending.acceptedAt + lifecyclePendingWarningAfterMs - Date.now(),
    );
    const timeoutID = window.setTimeout(() => {
      const warned = markPendingAgentLifecycleWarningShown(
        pending.workspace,
        pending.agent,
        pending.commandId,
      );
      const localWarned =
        warned ??
        (loadPendingAgentLifecycleCommand(pending.workspace, pending.agent) ==
        null
          ? { ...pending, warningShown: true }
          : null);
      if (localWarned == null) return;
      setPendingState((current) =>
        current.identity === identity &&
        current.pending?.commandId === pending.commandId
          ? { identity, pending: localWarned }
          : current,
      );
      showToastRef.current(
        `${lifecycleActionLabel(
          pending.action,
        )} is still pending for ${pending.agent}; controls remain locked while Loom confirms the command`,
        { type: "warning" },
      );
    }, warningDelay);

    return () => {
      window.clearTimeout(timeoutID);
    };
  }, [identity, pending]);

  useEffect(() => {
    if (pending == null) return;

    let disposed = false;
    let pollID: number | undefined;
    let controller: AbortController | null = null;

    const clearTrackedCommand = (): boolean => {
      const cleared = clearPendingAgentLifecycleCommand(
        pending.workspace,
        pending.agent,
        pending.commandId,
      );
      if (
        !cleared &&
        loadPendingAgentLifecycleCommand(pending.workspace, pending.agent) !=
          null
      ) {
        return false;
      }
      if (mountedRef.current) {
        setPendingState((current) =>
          current.identity === identity &&
          current.pending?.commandId === pending.commandId
            ? { identity, pending: null }
            : current,
        );
      }
      return true;
    };

    const settle = (command: AgentLifecycleCommandResult) => {
      if (!clearTrackedCommand()) return;
      onChangedRef.current();
      showLifecycleTerminalToast(showToastRef.current, pending, command);
    };

    const schedulePoll = () => {
      if (!disposed) {
        pollID = window.setTimeout(
          () => void poll(),
          lifecycleCommandPollIntervalMs,
        );
      }
    };

    const poll = async () => {
      controller = new AbortController();
      try {
        const command = await getAgentLifecycleCommand(
          pending.workspace,
          pending.agent,
          pending.commandId,
          { signal: controller.signal },
        );
        if (disposed) return;
        if (
          command.command_id !== pending.commandId ||
          !lifecycleCommandActionMatches(pending.action, command.action)
        ) {
          schedulePoll();
          return;
        }
        if (isTerminalLifecycleCommandStatus(command.status)) {
          settle(command);
          return;
        }
        schedulePoll();
      } catch (err) {
        if (disposed || isAbortError(err)) return;
        if (err instanceof ApiError && err.status === 404) {
          if (!clearTrackedCommand()) return;
          onChangedRef.current();
          showToastRef.current(
            `${lifecycleActionLabel(
              pending.action,
            )} command ${pending.commandId} is no longer available; refreshed the current agent state`,
            { type: "warning" },
          );
          return;
        }
        // Network errors, timeouts, and server errors are not evidence that
        // the durable command ended. Retain the lock and retry.
        schedulePoll();
      }
    };

    void poll();
    return () => {
      disposed = true;
      if (pollID != null) window.clearTimeout(pollID);
      controller?.abort();
    };
  }, [identity, pending]);

  if (!agent || isEphemeralWorker(agent)) return null;
  if (workspace === "") return null;

  const stopped = isTerminalUnavailable(agent);
  const busy = requestInFlight || submissionLocked || pending != null;
  lifecycleLocked.current = busy;

  const runControl = async (
    actionName: AgentLifecycleAction,
    label: string,
    action: () => Promise<AgentLifecycleRequestResult>,
  ): Promise<void> => {
    if (lifecycleLocked.current) return;
    const submissionToken = acquireAgentLifecycleSubmission(
      workspace,
      agent.name,
    );
    if (submissionToken == null) {
      setSubmissionLocked(true);
      return;
    }
    const requestedAt = Date.now();
    let submissionReleased = false;
    lifecycleLocked.current = true;
    setRequestInFlight(true);
    try {
      const response = await action();
      if (response.pending) {
        const command: PendingAgentLifecycleCommand = {
          action: actionName,
          workspace,
          agent: agent.name,
          commandId: response.command_id!,
          acceptedAt: requestedAt,
          warningShown: false,
        };
        const responseStatus = response.status;
        if (
          responseStatus != null &&
          isTerminalLifecycleCommandStatus(responseStatus)
        ) {
          showLifecycleTerminalToast(showToast, command, {
            command_id: command.commandId,
            action: actionName,
            status: responseStatus,
          });
        } else {
          const persisted = savePendingAgentLifecycleCommand(command);
          releaseAgentLifecycleSubmission(
            workspace,
            agent.name,
            submissionToken,
          );
          submissionReleased = true;
          const tracked =
            loadPendingAgentLifecycleCommand(workspace, agent.name) ?? command;
          if (mountedRef.current) {
            setPendingState({
              identity: lifecyclePendingIdentity(workspace, agent.name),
              pending: tracked,
            });
          }
          showToast(
            persisted
              ? `${label} requested for ${agent.name}`
              : `${label} was accepted for ${agent.name}, but this browser could not persist the pending command; keep this view open`,
            { type: persisted ? "success" : "warning" },
          );
        }
      } else {
        showSynchronousLifecycleToast(
          showToast,
          agent.name,
          actionName,
          response.status,
        );
      }
      if (mountedRef.current) {
        onChanged();
      }
    } catch (err) {
      if (mountedRef.current) {
        showToast(`${label} failed: ${(err as Error).message}`, {
          type: "error",
        });
      }
    } finally {
      if (!submissionReleased) {
        releaseAgentLifecycleSubmission(workspace, agent.name, submissionToken);
      }
      if (mountedRef.current) {
        setRequestInFlight(false);
      }
    }
  };

  const buttonStyle: CSSProperties = {
    minHeight: 28,
    padding: "0 12px",
    border: "1px solid var(--color-border, #ddd)",
    borderRadius: 4,
    background: "var(--color-bg, #fdfcf8)",
    color: "var(--color-text-primary, #333)",
    fontSize: 12,
    fontWeight: 600,
    cursor: busy ? "not-allowed" : "pointer",
    opacity: busy ? 0.6 : 1,
  };

  return (
    <div
      style={{ display: "flex", gap: 6, flexShrink: 0, marginLeft: "auto" }}
      data-testid="agent-lifecycle-controls"
    >
      {stopped ? (
        <button
          type="button"
          style={buttonStyle}
          disabled={busy}
          data-testid="agent-start-button"
          onClick={() =>
            void runControl("start", "Start", () =>
              startAgent(workspace, agent.name),
            )
          }
        >
          Start
        </button>
      ) : (
        <button
          type="button"
          style={buttonStyle}
          disabled={busy}
          data-testid="agent-stop-button"
          onClick={() =>
            void runControl("stop", "Stop", () =>
              stopAgent(workspace, agent.name),
            )
          }
        >
          Stop
        </button>
      )}
      <button
        type="button"
        style={buttonStyle}
        disabled={busy || stopped}
        data-testid="agent-restart-button"
        onClick={() =>
          void runControl("restart", "Restart", () =>
            restartAgent(workspace, agent.name),
          )
        }
      >
        Restart
      </button>
    </div>
  );
}

const lifecycleCommandPollIntervalMs = 1_000;
const lifecyclePendingWarningAfterMs = 15_000;

function lifecyclePendingIdentity(workspace: string, agent: string): string {
  return workspace === "" || agent === ""
    ? ""
    : `${encodeURIComponent(workspace)}\x00${encodeURIComponent(agent)}`;
}

function lifecycleActionLabel(action: AgentLifecycleAction): string {
  return action.charAt(0).toUpperCase() + action.slice(1);
}

function lifecycleCommandActionMatches(
  pendingAction: AgentLifecycleAction,
  commandAction: AgentLifecycleCommandResult["action"],
): boolean {
  return (
    commandAction === pendingAction ||
    (pendingAction === "stop" && commandAction === "yield")
  );
}

function isTerminalLifecycleCommandStatus(
  status: AgentLifecycleCommandStatus,
): status is "succeeded" | "failed" | "cancelled" {
  return (
    status === "succeeded" || status === "failed" || status === "cancelled"
  );
}

function isAbortError(err: unknown): boolean {
  return (
    (err instanceof DOMException && err.name === "AbortError") ||
    (err instanceof Error && err.name === "AbortError")
  );
}

function showLifecycleTerminalToast(
  showToast: ReturnType<typeof useToast>["showToast"],
  pending: PendingAgentLifecycleCommand,
  command: AgentLifecycleCommandResult,
): void {
  const label = lifecycleActionLabel(pending.action);
  if (command.status === "succeeded") {
    showToast(`${label} completed for ${pending.agent}`, { type: "success" });
    return;
  }
  const detail =
    command.result?.trim() ||
    command.error_class?.trim() ||
    `command ${command.status}`;
  showToast(`${label} ${command.status} for ${pending.agent}: ${detail}`, {
    type: "error",
  });
}

function showSynchronousLifecycleToast(
  showToast: ReturnType<typeof useToast>["showToast"],
  agent: string,
  action: AgentLifecycleAction,
  status: AgentLifecycleCommandStatus | undefined,
): void {
  const pending: PendingAgentLifecycleCommand = {
    workspace: "",
    agent,
    action,
    commandId: "",
    acceptedAt: Date.now(),
    warningShown: false,
  };
  if (status != null && isTerminalLifecycleCommandStatus(status)) {
    showLifecycleTerminalToast(showToast, pending, {
      command_id: "",
      action,
      status,
    });
    return;
  }
  showToast(`${lifecycleActionLabel(action)} completed for ${agent}`, {
    type: "success",
  });
}

export function leadDeliveryStateLabel(
  deliveryState: string | undefined,
): string {
  switch ((deliveryState ?? "").trim().toLowerCase()) {
    case "pending":
      return "context pending";
    case "delivered":
      return "context sent";
    case "acknowledged":
      return "lead acknowledged";
    default:
      return "";
  }
}

function displayBranch(branch: string | undefined): string {
  const value = (branch ?? "").trim();
  if (value === "" || value.toLowerCase() === "unknown") {
    return "";
  }
  return value;
}

function EmptyState({
  message,
  detail,
}: {
  message: string;
  detail: string;
}): JSX.Element {
  return (
    <div
      style={{
        flex: 1,
        minWidth: 0,
        display: "flex",
        flexDirection: "column",
        alignItems: "center",
        justifyContent: "center",
        color: "var(--color-text-muted, #888)",
        padding: 32,
        borderRight: "1px solid var(--color-border, #ddd)",
        background: "var(--color-bg, #fdfcf8)",
      }}
    >
      <div style={{ fontSize: 14, fontWeight: 600 }}>{message}</div>
      <div
        style={{
          fontSize: 12,
          marginTop: 6,
          textAlign: "center",
          maxWidth: 360,
        }}
      >
        {detail}
      </div>
    </div>
  );
}
