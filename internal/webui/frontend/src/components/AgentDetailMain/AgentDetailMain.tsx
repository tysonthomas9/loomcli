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
import { restartAgent, startAgent, stopAgent, wsUrl } from "@/hooks/api";
import { useToast } from "@/hooks/ui/useToast";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
import { isInteractiveAgent, isLeadRole } from "@/utils/agentRole";
import {
  agentDisplayRoleLabel,
  agentDisplayTitle,
  agentUsesLiteralTitle,
} from "@/utils/agentDisplay";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

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
  const terminalUnavailable = agent != null && isTerminalUnavailable(agent);
  const ephemeralWorker = agent != null && isEphemeralWorker(agent);
  const shouldResolveLeadTerminal =
    agent != null && isInteractiveAgent(agent) && terminalUnavailable;
  const terminalEmptyState =
    agent != null && terminalUnavailable
      ? terminalUnavailableEmptyState(agent)
      : null;

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
              pendingAgentName={pendingAgentName}
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
function AgentLifecycleControls({
  agent,
  onChanged,
}: {
  agent: LoomAgentStatus | undefined;
  onChanged: () => void;
}): JSX.Element | null {
  const { showToast } = useToast();
  const [busy, setBusy] = useState(false);

  if (!agent || isEphemeralWorker(agent)) return null;
  const ws = (agent.workspace ?? "").trim();
  if (ws === "") return null;

  const stopped = isTerminalUnavailable(agent);

  const runControl = async (
    label: string,
    action: () => Promise<void>,
  ): Promise<void> => {
    setBusy(true);
    try {
      await action();
      showToast(`${label} requested for ${agent.name}`, { type: "success" });
      // Optimistic refresh; the status poll then reflects the settled state.
      onChanged();
    } catch (err) {
      showToast(`${label} failed: ${(err as Error).message}`, {
        type: "error",
      });
    } finally {
      setBusy(false);
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
            void runControl("Start", () => startAgent(ws, agent.name))
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
            void runControl("Stop", () => stopAgent(ws, agent.name))
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
          void runControl("Restart", () => restartAgent(ws, agent.name))
        }
      >
        Restart
      </button>
    </div>
  );
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
