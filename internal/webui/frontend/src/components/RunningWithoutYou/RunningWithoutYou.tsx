import { useMemo } from "react";

import { useElapsedTime } from "@/hooks/common";
import type { Issue, LoomAgentStatus } from "@/types";
import { effectiveAgentStatus, isAgentActive, parseLoomStatus } from "@/types";
import { getStatusDotColor, getStatusLabel } from "@/utils/agent";
import { getCompactAvatarInitials } from "@/utils/compactAvatarInitials";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import {
  agentCompactAvatarLabel,
  agentDisplayRoleLabel,
  agentDisplayTitle,
} from "@/utils/agentDisplay";
import { plural } from "@/utils/plural";

import styles from "./RunningWithoutYou.module.css";

function durationToMilliseconds(duration: string | undefined): number | null {
  if (!duration) return null;
  const matches = [...duration.matchAll(/(\d+)(h|m|s)/g)];
  if (matches.length === 0) return null;

  const milliseconds = matches.reduce((total, match) => {
    const value = Number(match[1]);
    const unit = match[2];
    if (!Number.isFinite(value)) return total;
    if (unit === "h") return total + value * 3_600_000;
    if (unit === "m") return total + value * 60_000;
    return total + value * 1_000;
  }, 0);
  return milliseconds > 0 ? milliseconds : null;
}

function Avatar({
  agent,
  compact = false,
}: {
  agent: LoomAgentStatus;
  compact?: boolean;
}): JSX.Element {
  const color = getAvatarColor(agent.name);
  const label =
    agentCompactAvatarLabel(agent) || getCompactAvatarInitials(agent.name);
  const parsed = parseLoomStatus(effectiveAgentStatus(agent));

  return (
    <span
      className={compact ? styles.miniAvatar : styles.avatar}
      style={{
        backgroundColor: color,
        // Text sits on the agent's own colour, not the theme surface, so it
        // must not follow the theme's inverse token (which is dark in dark).
        color: shouldUseWhiteText(color) ? "#ffffff" : "#1a1a1a",
      }}
    >
      {label}
      {!compact && (
        <i
          className={styles.statusDot}
          style={{ backgroundColor: getStatusDotColor(parsed.type) }}
          title={getStatusLabel(parsed)}
        />
      )}
    </span>
  );
}

function AgentElapsed({
  duration,
}: {
  duration: string | undefined;
}): JSX.Element | null {
  const startedAt = useMemo(() => {
    const milliseconds = durationToMilliseconds(duration);
    return milliseconds ? Date.now() - milliseconds : null;
  }, [duration]);
  const elapsed = useElapsedTime(startedAt);

  return elapsed ? <>{elapsed} elapsed</> : null;
}

function taskForAgent(
  agent: LoomAgentStatus,
  issueById: ReadonlyMap<string, Issue>,
): Issue | undefined {
  const parsed = parseLoomStatus(effectiveAgentStatus(agent));
  const taskId = agent.active_task_id ?? agent.current_task_id ?? parsed.taskId;
  return taskId ? issueById.get(taskId) : undefined;
}

function LiveAgentRow({
  agent,
  issueById,
  onWatch,
}: {
  agent: LoomAgentStatus;
  issueById: ReadonlyMap<string, Issue>;
  onWatch: (name: string) => void;
}): JSX.Element {
  const parsed = parseLoomStatus(effectiveAgentStatus(agent));
  const task = taskForAgent(agent, issueById);
  const taskId = agent.active_task_id ?? agent.current_task_id ?? parsed.taskId;
  const details = [
    agent.branch ? `branch ${agent.branch}` : "",
    agent.changes?.length
      ? `${agent.changes.length} ${plural(agent.changes.length, "file", "files")} dirty`
      : "",
  ].filter(Boolean);

  return (
    <div
      className={styles.liveAgent}
      data-agent={agent.name}
      data-testid="live-agent"
    >
      <Avatar agent={agent} />
      <span className={styles.agentInfo}>
        <span className={styles.agentName}>{agentDisplayTitle(agent)}</span>
        <span className={styles.agentRole}>{agentDisplayRoleLabel(agent)}</span>
      </span>
      <span className={styles.agentTask}>
        <span className={styles.taskTitle}>
          {taskId
            ? task
              ? `${taskId} · ${task.title}`
              : taskId
            : getStatusLabel(parsed)}
        </span>
        {details.length > 0 && (
          <span className={styles.taskMeta}>{details.join(" · ")}</span>
        )}
      </span>
      <span className={styles.sessionPill}>
        {agent.backend ?? "agent"}
        {parsed.duration && (
          <>
            {" · "}
            <AgentElapsed duration={parsed.duration} />
          </>
        )}
      </span>
      <button
        className={styles.watchButton}
        type="button"
        onClick={() => onWatch(agent.name)}
      >
        Watch
      </button>
    </div>
  );
}

export interface RunningWithoutYouProps {
  agents: readonly LoomAgentStatus[];
  issues: readonly Issue[];
  onWatch: (agentName: string) => void;
}

export function RunningWithoutYou({
  agents,
  issues,
  onWatch,
}: RunningWithoutYouProps): JSX.Element {
  const activeAgents = agents.filter(isAgentActive);
  const idleAgents = agents.filter((agent) => !isAgentActive(agent));
  const issueById = useMemo(
    () => new Map(issues.map((issue) => [issue.id, issue])),
    [issues],
  );

  return (
    <section className={styles.strip} data-testid="running-without-you">
      <header className={styles.head}>
        <h3>Running without you</h3>
        <span>
          {activeAgents.length}{" "}
          {plural(activeAgents.length, "session", "sessions")} live
          {" · "}
          {idleAgents.length} {plural(idleAgents.length, "agent", "agents")}{" "}
          idle
        </span>
      </header>
      {activeAgents.map((agent) => (
        <LiveAgentRow
          agent={agent}
          issueById={issueById}
          key={agent.name}
          onWatch={onWatch}
        />
      ))}
      {idleAgents.length > 0 && (
        <div className={styles.idlePills}>
          {idleAgents.map((agent) => {
            const parsed = parseLoomStatus(effectiveAgentStatus(agent));
            return (
              <span
                className={styles.idlePill}
                data-testid="idle-agent-pill"
                key={agent.name}
              >
                <Avatar agent={agent} compact />
                {agentDisplayTitle(agent)} ·{" "}
                {parsed.duration ? `idle ${parsed.duration}` : "idle"} ·{" "}
                {agent.role === "plan"
                  ? "designs only"
                  : agentDisplayRoleLabel(agent)}
              </span>
            );
          })}
        </div>
      )}
      {agents.length === 0 && (
        <p className={styles.empty}>No agents are currently registered.</p>
      )}
    </section>
  );
}
