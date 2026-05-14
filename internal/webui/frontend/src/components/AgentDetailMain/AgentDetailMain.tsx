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
  lazy,
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useState,
  type CSSProperties,
} from "react";
import { useStore } from "zustand";

import { LoadingSkeleton } from "@/components";
import type { TerminalInputRequest } from "@/components/TerminalView";
import { useAgentStoreInstance } from "@/hooks";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
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
  const completedEphemeralWorker =
    agent != null && isCompletedEphemeralWorker(agent);

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
      <Header agent={agent} agentName={agentName} />
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
        {completedEphemeralWorker ? (
          <CompletedWorkerSummary agent={agent} />
        ) : terminalUnavailable ? (
          <EmptyState
            message="Agent is stopped"
            detail="This agent does not have a live terminal session. Start the agent before attaching to its PTY."
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

function isCompletedEphemeralWorker(agent: LoomAgentStatus): boolean {
  return agent.mode === "ephemeral" && isTerminalUnavailable(agent);
}

function CompletedWorkerSummary({
  agent,
}: {
  agent: LoomAgentStatus;
}): JSX.Element {
  const taskId =
    agent.task_id || parseLoomStatus(agent.status ?? "").taskId || "unknown";
  const workspace = agent.workspace || "";
  const sessionId = agent.session_id || "";
  const hasTaskSession = taskId !== "unknown" && sessionId !== "";
  const logsHref =
    workspace !== ""
      ? `/api/workspaces/${encodeURIComponent(workspace)}/agents/${encodeURIComponent(agent.name)}/logs`
      : "";
  const transcriptHref =
    workspace !== "" && hasTaskSession
      ? `/api/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/transcript`
      : "";
  const diffHref =
    workspace !== "" && hasTaskSession
      ? `/api/workspaces/${encodeURIComponent(workspace)}/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/diff`
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
        <div style={{ fontSize: 18, fontWeight: 700 }}>{agent.name}</div>
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
          <span>{agent.desired_state || agent.status || "stopped"}</span>
        </div>
        <div
          style={{
            fontSize: 12,
            lineHeight: 1.5,
            color: "var(--color-text-secondary, #555)",
          }}
        >
          This worker is retained as task attempt history. Live terminal attach
          is disabled after the ephemeral run stops.
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
}: {
  agent: LoomAgentStatus | undefined;
  agentName: string;
}): JSX.Element {
  const parsed = useMemo(
    () => parseLoomStatus(agent?.status ?? ""),
    [agent?.status],
  );
  const dotColor =
    STATUS_DOT_COLOR[parsed.type] ?? "var(--color-status-idle, #888)";
  const initial = (agentName[0] ?? "?").toUpperCase();
  const avatarBg = getAvatarColor(agentName);
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";

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
          fontSize: 14,
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
        <div style={{ fontSize: 14, fontWeight: 700 }}>{agentName}</div>
        <div
          style={{
            fontSize: 11,
            color: "var(--color-text-muted, #666)",
            display: "flex",
            alignItems: "center",
            gap: 6,
          }}
        >
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
          {agent?.branch ? (
            <>
              <span>·</span>
              <code
                style={{
                  fontFamily: "var(--font-mono, ui-monospace, monospace)",
                }}
              >
                {agent.branch}
              </code>
            </>
          ) : null}
          {agent?.role ? (
            <>
              <span>·</span>
              <span>{agent.role}</span>
            </>
          ) : null}
          {parsed.taskId ? (
            <>
              <span>·</span>
              <code
                style={{
                  fontFamily: "var(--font-mono, ui-monospace, monospace)",
                }}
              >
                {parsed.taskId}
              </code>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
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
