/**
 * AgentDetailMain — Direction J middle panel.
 *
 * Header with agent identity (avatar, name, status, branch, role) followed by
 * a metadata card (current task, repo, worktree path) and quick-access buttons
 * to existing surfaces (Terminal for live logs, full-page issue detail for the
 * agent's current task). The Aether wireframe shows a chat-style middle pane;
 * the live chat surface is a richer follow-up. This panel ships the
 * "selected agent context" value without inventing a new chat component.
 */

import { useMemo } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks";
import { type LoomAgentStatus, parseLoomStatus } from "@/types";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

interface AgentDetailMainProps {
  agentName: string | undefined;
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

export function AgentDetailMain({ agentName }: AgentDetailMainProps): JSX.Element {
  const { workspaceId = "" } = useParams<{ workspaceId: string }>();
  const navigate = useNavigate();
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);

  const agent = useMemo<LoomAgentStatus | undefined>(
    () => agents.find((a) => a.name === agentName),
    [agents, agentName],
  );

  if (!agentName) {
    return <EmptyState message="Select an agent" detail="Pick an agent from the rail to view their work." />;
  }

  if (!agent) {
    return (
      <EmptyState
        message="Agent not found"
        detail={`No agent named "${agentName}" exists in this workspace. It may have been deleted, or the URL is stale.`}
      />
    );
  }

  const parsed = parseLoomStatus(agent.status ?? "");
  const dotColor = STATUS_DOT_COLOR[parsed.type] ?? "var(--color-status-idle, #888)";
  const initial = (agent.name?.[0] ?? "?").toUpperCase();
  const avatarBg = getAvatarColor(agent.name ?? "");
  const avatarFg = shouldUseWhiteText(avatarBg) ? "#fff" : "#1a1a1a";

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
        initial={initial}
        avatarBg={avatarBg}
        avatarFg={avatarFg}
        statusType={parsed.type}
        statusDotColor={dotColor}
      />

      <div
        style={{
          flex: 1,
          padding: 20,
          overflow: "auto",
          display: "flex",
          flexDirection: "column",
          gap: 16,
        }}
      >
        <MetadataCard agent={agent} parsedTaskId={parsed.taskId} />

        <ActionsCard
          agent={agent}
          parsedTaskId={parsed.taskId}
          onGoToTask={(taskId) =>
            navigate(`/ws/${workspaceId}/issues/${encodeURIComponent(taskId)}`)
          }
          onOpenTerminal={() => navigate(`/ws/${workspaceId}/terminal`)}
        />

        <ChatPlaceholder agentName={agent.name} />
      </div>
    </div>
  );
}

function Header({
  agent,
  initial,
  avatarBg,
  avatarFg,
  statusType,
  statusDotColor,
}: {
  agent: LoomAgentStatus;
  initial: string;
  avatarBg: string;
  avatarFg: string;
  statusType: string;
  statusDotColor: string;
}): JSX.Element {
  return (
    <div
      style={{
        padding: "12px 18px",
        borderBottom: "1px solid var(--color-border, #ddd)",
        display: "flex",
        alignItems: "center",
        gap: 12,
      }}
    >
      <span
        aria-hidden="true"
        style={{
          width: 36,
          height: 36,
          borderRadius: "50%",
          background: avatarBg,
          color: avatarFg,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          fontWeight: 700,
          fontSize: 16,
          flexShrink: 0,
          border: "1px solid rgba(0,0,0,0.18)",
        }}
      >
        {initial}
      </span>
      <div style={{ display: "flex", flexDirection: "column", flex: 1, minWidth: 0 }}>
        <div style={{ fontSize: 15, fontWeight: 700 }}>{agent.name}</div>
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
              width: 8,
              height: 8,
              borderRadius: "50%",
              background: statusDotColor,
              display: "inline-block",
            }}
          />
          <span>{statusType}</span>
          {agent.branch ? (
            <>
              <span>·</span>
              <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>
                {agent.branch}
              </code>
            </>
          ) : null}
          {agent.role ? (
            <>
              <span>·</span>
              <span>{agent.role}</span>
            </>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function MetadataCard({
  agent,
  parsedTaskId,
}: {
  agent: LoomAgentStatus;
  parsedTaskId: string | undefined;
}): JSX.Element {
  return (
    <Card label="Agent">
      <Row label="Role" value={agent.role ?? "—"} />
      <Row label="Repo" value={agent.repo ?? "—"} />
      <Row
        label="Current task"
        value={parsedTaskId ?? "(none)"}
        mono={Boolean(parsedTaskId)}
      />
      {agent.cross_repo ? <Row label="Cross-repo" value="yes" /> : null}
      {agent.worktree_path ? (
        <Row label="Worktree" value={agent.worktree_path} mono />
      ) : null}
    </Card>
  );
}

function ActionsCard({
  agent,
  parsedTaskId,
  onGoToTask,
  onOpenTerminal,
}: {
  agent: LoomAgentStatus;
  parsedTaskId: string | undefined;
  onGoToTask: (taskId: string) => void;
  onOpenTerminal: () => void;
}): JSX.Element {
  return (
    <Card label="Quick actions">
      <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
        {parsedTaskId ? (
          <ActionButton onClick={() => onGoToTask(parsedTaskId)}>
            Open current task ↗
          </ActionButton>
        ) : null}
        <ActionButton onClick={onOpenTerminal}>Open Terminal ↗</ActionButton>
        {/* Diff/Logs/Files surfaces live in the existing AgentDetailPanel slide-out;
            we leave room for those affordances in a follow-up that wires them up. */}
      </div>
      <div
        style={{
          fontSize: 11,
          color: "var(--color-text-muted, #888)",
          marginTop: 8,
        }}
      >
        Selected agent: <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>{agent.name}</code>
      </div>
    </Card>
  );
}

function ChatPlaceholder({ agentName }: { agentName: string }): JSX.Element {
  return (
    <Card label="Chat (preview)">
      <div style={{ fontSize: 12, color: "var(--color-text-muted, #888)" }}>
        Live chat with <strong>{agentName}</strong> lands in a follow-up. For
        now, use <em>Open Terminal</em> above to attach to the agent's session,
        or open the agent's current task to see what it's working on.
      </div>
      <div
        style={{
          fontSize: 11,
          color: "var(--color-text-muted, #888)",
          marginTop: 8,
          fontStyle: "italic",
        }}
      >
        Coming next: orchestrator-lineage chip ("spawned by lead-…") and live
        epic-run progress, once the monitor API surfaces{" "}
        <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>mode</code>{" "}
        and{" "}
        <code style={{ fontFamily: "var(--font-mono, ui-monospace, monospace)" }}>orchestrator_session_id</code>{" "}
        on agent status (the Go fields exist; OpenAPI extension is the
        remaining hook-up).
      </div>
    </Card>
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
      <div style={{ fontSize: 12, marginTop: 6, textAlign: "center", maxWidth: 360 }}>
        {detail}
      </div>
    </div>
  );
}

function Card({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}): JSX.Element {
  return (
    <div
      style={{
        border: "1px solid var(--color-border, #ddd)",
        borderRadius: 6,
        padding: "10px 14px",
        background: "var(--color-bg-soft, #faf8f3)",
      }}
    >
      <div
        style={{
          fontSize: 11,
          fontWeight: 700,
          letterSpacing: 0.4,
          textTransform: "uppercase",
          color: "var(--color-text-muted, #666)",
          marginBottom: 8,
        }}
      >
        {label}
      </div>
      <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
        {children}
      </div>
    </div>
  );
}

function Row({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}): JSX.Element {
  return (
    <div style={{ display: "flex", gap: 12, fontSize: 12 }}>
      <span
        style={{
          width: 110,
          flexShrink: 0,
          color: "var(--color-text-muted, #666)",
        }}
      >
        {label}
      </span>
      <span
        style={{
          flex: 1,
          minWidth: 0,
          fontFamily: mono ? "var(--font-mono, ui-monospace, monospace)" : "inherit",
          overflow: "hidden",
          textOverflow: "ellipsis",
          whiteSpace: "nowrap",
        }}
      >
        {value}
      </span>
    </div>
  );
}

function ActionButton({
  onClick,
  children,
}: {
  onClick: () => void;
  children: React.ReactNode;
}): JSX.Element {
  return (
    <button
      type="button"
      onClick={onClick}
      style={{
        padding: "6px 10px",
        fontSize: 12,
        border: "1px solid var(--color-border, #888)",
        borderRadius: 4,
        background: "var(--color-bg, #fff)",
        cursor: "pointer",
      }}
    >
      {children}
    </button>
  );
}
