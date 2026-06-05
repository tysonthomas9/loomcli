/**
 * AgentsPage — full-page lead-agent view (Aether `/agents`).
 *
 * Ported from the design's AgentDetailsPanel: an agent-selector rail, a tabbed
 * main panel (Terminal / Info / Git / Diff / Files) centered on the selected
 * agent's live terminal, and a right-hand Open Queue / Worker History side panel.
 *
 * Fully data-backed — no stubs:
 *   - Terminal: real wterm pane over the agent PTY WebSocket relay (AgentTerminal)
 *   - Git/Diff/Files: loom's real GitTab / DiffTab / FileEditorPanel
 *   - Info: stat cards + Agent Info derived from real workspace issues + agent
 *   - Queue: open tasks, worker history, counts from real issues + agents
 */

import { useEffect, useMemo, useRef, useState, lazy, Suspense } from "react";

import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import { updateIssue } from "@/api";
import { GitTab } from "@/components/AgentDetailPanel";
import {
  TerminalInstance,
  TerminalConnectionOverlay,
  ReconnectingOverlay,
  type TerminalInstanceHandle,
  type ConnectionState,
  type ReconnectOverlayState,
} from "@/components/TerminalView";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { parseLoomStatus } from "@/types";

import styles from "./AgentsPage.module.css";

/**
 * Real interactive lead-agent terminal: a wterm pane bound to the agent's PTY
 * over the live WebSocket relay (same connection loom's Monitor view uses),
 * with the standard connection / reconnect overlays. No stub.
 */
function AgentTerminal({
  agentName,
  isActive,
}: {
  agentName: string;
  isActive: boolean;
}): JSX.Element {
  const instanceRef = useRef<TerminalInstanceHandle | null>(null);
  const [connectionState, setConnectionState] =
    useState<ConnectionState>("connecting");
  const [hasConnected, setHasConnected] = useState(false);
  const [reconnectState, setReconnectState] =
    useState<ReconnectOverlayState>(null);
  const reconnect = () => instanceRef.current?.reconnect();

  return (
    <div className={styles.agentTerminal}>
      <TerminalInstance
        ref={instanceRef}
        sessionName={`agent-${agentName}`}
        agentName={agentName}
        isActive={isActive}
        autoStartStaleSession
        onConnectionStateChange={(state, connected) => {
          setConnectionState(state);
          if (connected) setHasConnected(true);
        }}
        onReconnectStateChange={setReconnectState}
      />
      <TerminalConnectionOverlay
        connectionState={connectionState}
        hasConnected={hasConnected}
        onReconnect={reconnect}
      />
      <ReconnectingOverlay state={reconnectState} onReconnect={reconnect} />
    </div>
  );
}

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
  const { agents, issues, workspaceId } = useWorkspaceViewData();
  const { refetch, showToast } = useWorkspaceViewActions();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<AgentTab>("terminal");
  // Open Queue → assign an unclaimed task to an agent (Aether V3 flow), wired
  // to the real issue-assignment API.
  const [assignMenuId, setAssignMenuId] = useState<string | null>(null);
  const [assigningId, setAssigningId] = useState<string | null>(null);

  const assignTask = async (taskId: string, agentName: string): Promise<void> => {
    if (!workspaceId) return;
    setAssignMenuId(null);
    setAssigningId(taskId);
    try {
      await updateIssue(workspaceId, taskId, { assignee: agentName });
      refetch();
    } catch (err) {
      showToast(
        err instanceof Error ? err.message : "Failed to assign task",
        { type: "error" },
      );
    } finally {
      setAssigningId(null);
    }
  };

  // Default to a lead agent, else the first agent.
  const selected = useMemo(() => {
    if (agents.length === 0) return null;
    const byId = selectedId && agents.find((a) => a.name === selectedId);
    if (byId) return byId;
    return agents[0];
  }, [agents, selectedId]);

  // Real workspace counts for the Info stats + queue pills.
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
    { id: "completed", label: "Tasks Completed", value: counts.done, tone: "success" },
    { id: "progress", label: "In Progress", value: counts.inProgress, tone: "warning" },
    { id: "blocked", label: "Blocked", value: counts.blocked, tone: "danger" },
    { id: "queued", label: "Queued", value: counts.queued, tone: "info" },
  ];

  // Open (queued) and unassigned tasks from real issues.
  const openTasks = useMemo(
    () =>
      issues.filter((i) => {
        const s = i.status ?? "open";
        return s === "open" || s === "deferred" || s === "in_progress";
      }),
    [issues],
  );
  const unassignedTasks = useMemo(
    () => issues.filter((i) => !i.assignee && !i.owner).slice(0, 3),
    [issues],
  );

  useEffect(() => {
    setActiveTab("terminal");
  }, [selected?.name]);

  if (!selected) {
    return (
      <div className={styles.emptyPage} data-testid="agents-page">
        <p>No agents yet. Create one from the sidebar to get started.</p>
      </div>
    );
  }

  const selColor = getAvatarColor(selected.name);
  const selText = shouldUseWhiteText(selColor) ? "#fff" : "#171717";
  const statusType = parseLoomStatus(selected.status).type;
  const roleName = selected.role ?? statusType;

  return (
    <div className={styles.page} data-testid="agents-page">
      {/* Agent selector rail */}
      <aside className={styles.selectorRail} aria-label="Agent selector">
        {agents.map((agent) => {
          const c = getAvatarColor(agent.name);
          return (
            <button
              key={agent.name}
              type="button"
              className={styles.selectorAvatar}
              data-active={agent.name === selected.name || undefined}
              style={{ backgroundColor: c, color: shouldUseWhiteText(c) ? "#fff" : "#171717" }}
              onClick={() => setSelectedId(agent.name)}
              aria-label={`Show ${agent.name} terminal`}
              title={agent.name}
            >
              {agent.name.charAt(0).toUpperCase()}
            </button>
          );
        })}
      </aside>

      {/* Main panel */}
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

        {activeTab === "terminal" && (
          <div className={styles.terminalWrap}>
            <header className={styles.agentHeader}>
              <span className={styles.headerAvatar} style={{ backgroundColor: selColor, color: selText }}>
                {selected.name.charAt(0).toUpperCase()}
              </span>
              <div className={styles.headerInfo}>
                <h1 className={styles.agentName}>{selected.name}</h1>
                <p className={styles.agentMeta}>
                  <span className={styles.statusDot} aria-hidden="true" />
                  <span>{statusType}</span>
                  <span>·</span>
                  <span>{roleName}</span>
                  <span>·</span>
                  <span>no epic assigned</span>
                </p>
              </div>
            </header>
            <div className={styles.realTabBody}>
              <AgentTerminal
                key={selected.name}
                agentName={selected.name}
                isActive={activeTab === "terminal"}
              />
            </div>
          </div>
        )}

        {activeTab === "info" && (
          <div className={styles.scrollPanel}>
            <section className={styles.card}>
              <div className={styles.infoHead}>
                <span className={styles.infoAvatar} style={{ backgroundColor: selColor, color: selText }}>
                  {selected.name.charAt(0).toUpperCase()}
                </span>
                <div>
                  <h1 className={styles.agentName}>{selected.name}</h1>
                  <p className={styles.infoSub}>{roleName} agent · isolated workspace runtime</p>
                </div>
              </div>
              <dl className={styles.statGrid}>
                {infoStats.map((s) => (
                  <div key={s.id} className={styles.statCard}>
                    <dt className={styles.statLabel}>{s.label}</dt>
                    <dd className={styles.statValue} data-tone={s.tone}>{s.value}</dd>
                  </div>
                ))}
              </dl>
            </section>
            <section className={styles.card}>
              <h2 className={styles.cardLabel}>Agent Info</h2>
              <dl className={styles.configGrid}>
                <div>
                  <dt>Status</dt>
                  <dd>{selected.status}</dd>
                </div>
                <div>
                  <dt>Role</dt>
                  <dd>{roleName}</dd>
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
          </div>
        )}

        {activeTab === "git" && (
          <div className={styles.realTabBody}>
            <GitTab agent={selected} isActive={activeTab === "git"} />
          </div>
        )}

        {activeTab === "diff" && (
          <div className={styles.realTabBody}>
            <Suspense
              fallback={<div className={styles.tabFallback}>Loading diff…</div>}
            >
              <DiffTab agent={selected} isActive={activeTab === "diff"} />
            </Suspense>
          </div>
        )}

        {activeTab === "files" && (
          <div className={styles.realTabBody}>
            <Suspense
              fallback={<div className={styles.tabFallback}>Loading files…</div>}
            >
              <FileEditorPanel
                agentName={selected.name}
                isActive={activeTab === "files"}
              />
            </Suspense>
          </div>
        )}
      </section>

      {/* Open queue side panel — driven by real workspace issues + agents */}
      <aside className={styles.queuePanel} aria-label="Open queue">
        <section>
          <h2 className={styles.queueHeading}>
            OPEN QUEUE · {openTasks.length} OPEN · {agents.length} WORKERS
          </h2>
          <div className={styles.pillRow}>
            <span className={styles.qpill}>{counts.done} done</span>
            <span className={styles.qpill} data-tone="success">{counts.inProgress} in progress</span>
            <span className={styles.qpill}>{counts.queued} queued</span>
            <span className={styles.qpill}>{counts.blocked} blocked</span>
          </div>
        </section>

        <section className={styles.queueSection}>
          <div className={styles.queueSubRow}>
            <p className={styles.epicTitleRow}>
              <span className={styles.epicBadge}>OPEN</span>
              <strong className={styles.epicTitle}>Open tasks</strong>
            </p>
            <span className={styles.epicCount}>{openTasks.length}</span>
          </div>
          {openTasks.length === 0 ? (
            <p className={styles.epicClaim}>No open tasks.</p>
          ) : (
            openTasks.slice(0, 4).map((t) => {
              // "Claimed" = an agent is assigned to work it (assignee). owner is
              // just the creator, so it doesn't count as claimed.
              const assignee = t.assignee;
              return (
                <article key={t.id} className={styles.queueCard}>
                  <div className={styles.queueCardRow}>
                    <span className={styles.queueCardDot} aria-hidden="true" />
                    <code className={styles.queueCardKey}>{t.id}</code>
                    <span className={styles.p2Badge}>P{t.priority}</span>
                  </div>
                  <p className={styles.queueCardTitle}>{t.title}</p>
                  {assignee ? (
                    <p className={styles.assignedTo}>
                      <span className={styles.runningDot} aria-hidden="true" />
                      assigned to {assignee} · starting…
                    </p>
                  ) : (
                    <div className={styles.assignWrap}>
                      <button
                        type="button"
                        className={styles.assignBtn}
                        disabled={assigningId === t.id || agents.length === 0}
                        aria-haspopup="menu"
                        aria-expanded={assignMenuId === t.id}
                        onClick={() =>
                          setAssignMenuId((cur) => (cur === t.id ? null : t.id))
                        }
                      >
                        {assigningId === t.id ? "Assigning…" : "Assign"}
                      </button>
                      {assignMenuId === t.id && (
                        <div className={styles.assignMenu} role="menu">
                          {agents.map((a) => {
                            const c = getAvatarColor(a.name);
                            return (
                              <button
                                key={a.name}
                                type="button"
                                role="menuitem"
                                className={styles.assignOption}
                                onClick={() => void assignTask(t.id, a.name)}
                              >
                                <span
                                  className={styles.assignAvatar}
                                  style={{
                                    backgroundColor: c,
                                    color: shouldUseWhiteText(c)
                                      ? "#fff"
                                      : "#171717",
                                  }}
                                >
                                  {a.name.charAt(0).toUpperCase()}
                                </span>
                                {a.name}
                              </button>
                            );
                          })}
                        </div>
                      )}
                    </div>
                  )}
                </article>
              );
            })
          )}
        </section>

        <section className={styles.queueSection}>
          <div className={styles.queueSubRow}>
            <h2 className={styles.queueSub}>WORKER HISTORY</h2>
            <span className={styles.queueCount}>{agents.length}</span>
          </div>
          {agents.slice(0, 4).map((a) => (
            <article key={a.name} className={styles.queueCard}>
              <div className={styles.queueCardRow}>
                <strong className={styles.workerName}>{a.name}</strong>
                <span className={styles.workerTag}>{parseLoomStatus(a.status).type}</span>
                <span className={styles.workerTag2}>{a.branch ? "active" : "idle"}</span>
              </div>
              <p className={styles.workerMeta}>{a.branch ?? a.name}</p>
            </article>
          ))}
        </section>

        {unassignedTasks.length > 0 && (
          <section className={styles.queueSection}>
            <div className={styles.queueSubRow}>
              <p className={styles.epicTitleRow}>
                <span className={styles.unassignedBadge}>UNASSIGNED</span>
                <strong className={styles.epicTitle}>Unassigned</strong>
              </p>
              <span className={styles.epicCount}>{unassignedTasks.length}</span>
            </div>
            {unassignedTasks.map((t) => (
              <article key={t.id} className={styles.queueCard}>
                <div className={styles.queueCardRow}>
                  <code className={styles.queueCardKey}>{t.id}</code>
                  <span className={styles.p2Badge}>P{t.priority}</span>
                </div>
                <p className={styles.queueCardTitle}>{t.title}</p>
              </article>
            ))}
          </section>
        )}
      </aside>
    </div>
  );
}
