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
import { useSearchParams } from "react-router-dom";

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
import type { Issue } from "@/types";
import { formatStatusLabel } from "@/utils/issue";

import styles from "./AgentsPage.module.css";

/** A queue task is "running" when an agent is on it (assigned or in progress). */
function isRunningTask(t: Issue): boolean {
  return Boolean(t.assignee) || t.status === "in_progress";
}

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
      {/* Exactly one overlay at a time: stacking both leaves the reconnect
          button underneath the other overlay's backdrop (unclickable). */}
      {reconnectState ? (
        <ReconnectingOverlay state={reconnectState} onReconnect={reconnect} />
      ) : (
        <TerminalConnectionOverlay
          connectionState={connectionState}
          hasConnected={hasConnected}
          onReconnect={reconnect}
        />
      )}
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
  const { refetch, showToast, handleIssueClick } = useWorkspaceViewActions();
  // Selection is deep-linkable: ?agent=<name> (set when an agent is clicked
  // anywhere in the app, e.g. the sidebar roster) seeds and tracks it.
  const [searchParams, setSearchParams] = useSearchParams();
  const agentParam = searchParams.get("agent");
  const [selectedId, setSelectedId] = useState<string | null>(agentParam);

  useEffect(() => {
    if (agentParam) setSelectedId(agentParam);
  }, [agentParam]);

  const selectAgent = (name: string): void => {
    setSelectedId(name);
    setSearchParams({ agent: name }, { replace: true });
  };
  const [activeTab, setActiveTab] = useState<AgentTab>("terminal");
  // Open Queue → assign an unclaimed task to an agent (Aether V3 flow), wired
  // to the real issue-assignment API.
  const [assignMenuId, setAssignMenuId] = useState<string | null>(null);
  const [assigningId, setAssigningId] = useState<string | null>(null);
  // Open Queue run-state filter (design: All / Running / Not running).
  const [queueFilter, setQueueFilter] = useState<"all" | "running" | "not_running">(
    "all",
  );
  // Per-epic expand/collapse in the queue (design: epics start collapsed
  // behind their count; the ungrouped section is always open).
  const [expandedEpics, setExpandedEpics] = useState<Record<string, boolean>>(
    {},
  );
  const toggleEpic = (key: string): void =>
    setExpandedEpics((prev) => ({ ...prev, [key]: !prev[key] }));

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

  // Open queue = every non-closed task (design: status !== Done). Epic issues
  // are lane headers (children group under parent_title), never queue cards.
  const openTasks = useMemo(
    () =>
      issues.filter(
        (i) => i.issue_type !== "epic" && (i.status ?? "open") !== "closed",
      ),
    [issues],
  );

  // An agent is "busy" when it's already assigned a live task — the design's
  // one-agent-one-job rule for the assign picker.
  const busyAgentTask = useMemo(() => {
    const m = new Map<string, string>();
    for (const t of openTasks) {
      if (t.assignee && !m.has(t.assignee)) m.set(t.assignee, t.id);
    }
    return m;
  }, [openTasks]);

  // Run-state filter + epic grouping for the Open Queue (design's structure).
  const runningCount = useMemo(
    () => openTasks.filter(isRunningTask).length,
    [openTasks],
  );
  const filteredOpen = useMemo(() => {
    if (queueFilter === "running") return openTasks.filter(isRunningTask);
    if (queueFilter === "not_running")
      return openTasks.filter((t) => !isRunningTask(t));
    return openTasks;
  }, [openTasks, queueFilter]);
  const queueGroups = useMemo(() => {
    const m = new Map<string, Issue[]>();
    for (const t of filteredOpen) {
      const k = t.parent_title || "No epic";
      const bucket = m.get(k);
      if (bucket) bucket.push(t);
      else m.set(k, [t]);
    }
    return [...m.entries()];
  }, [filteredOpen]);
  // Status-distribution meter for the queue header.
  const meter = useMemo(
    () =>
      [
        { k: "in_progress", v: counts.inProgress },
        { k: "open", v: counts.queued },
        { k: "review", v: counts.review },
        { k: "blocked", v: counts.blocked },
        { k: "closed", v: counts.done },
      ].filter((s) => s.v > 0),
    [counts],
  );
  const meterTotal = meter.reduce((s, x) => s + x.v, 0);
  const queueFilterTabs: { k: "all" | "running" | "not_running"; label: string; n: number }[] =
    [
      { k: "all", label: "All", n: openTasks.length },
      { k: "running", label: "Running", n: runningCount },
      { k: "not_running", label: "Not running", n: openTasks.length - runningCount },
    ];

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
              onClick={() => selectAgent(agent.name)}
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
                  <span>{formatStatusLabel(statusType)}</span>
                  <span>·</span>
                  <span>{formatStatusLabel(roleName)}</span>
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
                  <p className={styles.infoSub}>{formatStatusLabel(roleName)} agent · isolated workspace runtime</p>
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
            OPEN QUEUE · {openTasks.length} OPEN
          </h2>
          {/* Status-distribution meter (replaces the static count pills). */}
          {meterTotal > 0 && (
            <div
              className={styles.meter}
              role="img"
              aria-label="Workspace task distribution"
            >
              {meter.map((s) => (
                <span
                  key={s.k}
                  className={styles.meterSeg}
                  data-k={s.k}
                  style={{ width: `${(s.v / meterTotal) * 100}%` }}
                  title={`${formatStatusLabel(s.k)}: ${s.v}`}
                />
              ))}
            </div>
          )}
          <p className={styles.meterLine}>
            {counts.inProgress} in progress · {counts.queued} open ·{" "}
            {counts.review} review · {counts.blocked} blocked
          </p>
          {/* Run-state filter */}
          <div
            className={styles.queueFilter}
            role="group"
            aria-label="Filter open queue"
          >
            {queueFilterTabs.map((f) => (
              <button
                key={f.k}
                type="button"
                className={styles.queueFilterBtn}
                data-active={queueFilter === f.k || undefined}
                aria-pressed={queueFilter === f.k}
                onClick={() => setQueueFilter(f.k)}
              >
                {f.label} <span className={styles.queueFilterN}>{f.n}</span>
              </button>
            ))}
          </div>
        </section>

        {/* Open tasks grouped by epic (design's queue structure). */}
        {queueGroups.length === 0 ? (
          <section className={styles.queueSection}>
            <p className={styles.epicClaim}>No open tasks.</p>
          </section>
        ) : (
          queueGroups.map(([epic, tasks]) => {
            const isEpicGroup = epic !== "No epic";
            // Epics collapse behind their count (design); ungrouped is open.
            const isExpanded = !isEpicGroup || (expandedEpics[epic] ?? false);
            const freeAgents = agents.filter((a) => !busyAgentTask.has(a.name));
            return (
              <section key={epic} className={styles.queueSection}>
                <div className={styles.queueSubRow}>
                  {isEpicGroup ? (
                    <button
                      type="button"
                      className={styles.epicToggle}
                      onClick={() => toggleEpic(epic)}
                      aria-expanded={isExpanded}
                    >
                      <span className={styles.epicBadge}>EPIC</span>
                      <strong className={styles.epicTitle}>{epic}</strong>
                      <span className={styles.epicChevron} aria-hidden="true">
                        {isExpanded ? "▾" : "▸"}
                      </span>
                    </button>
                  ) : (
                    <p className={styles.epicTitleRow}>
                      <span className={styles.epicBadge}>OPEN</span>
                      <strong className={styles.epicTitle}>{epic}</strong>
                    </p>
                  )}
                  <span className={styles.epicCount}>{tasks.length}</span>
                </div>
                {isExpanded &&
                  tasks.map((t) => {
                    // "Claimed" = an agent is assigned to work it (assignee).
                    // owner is just the creator, so it doesn't count.
                    const assignee = t.assignee;
                    return (
                      <article key={t.id} className={styles.queueCard}>
                        {/* Card body opens the issue detail (design's
                            oq-card-btn); Assign stays a sibling control. */}
                        <button
                          type="button"
                          className={styles.queueCardBtn}
                          onClick={() => handleIssueClick(t)}
                        >
                          <div className={styles.queueCardRow}>
                            <span
                              className={styles.queueCardDot}
                              aria-hidden="true"
                            />
                            <code className={styles.queueCardKey}>{t.id}</code>
                          </div>
                          <p className={styles.queueCardTitle}>{t.title}</p>
                        </button>
                        {assignee ? (
                          <p className={styles.assignedTo}>
                            <span
                              className={styles.runningDot}
                              aria-hidden="true"
                            />
                            assigned to {assignee}
                          </p>
                        ) : (
                          <div className={styles.assignWrap}>
                            <button
                              type="button"
                              className={styles.assignBtn}
                              disabled={
                                assigningId === t.id || agents.length === 0
                              }
                              aria-haspopup="menu"
                              aria-expanded={assignMenuId === t.id}
                              onClick={() =>
                                setAssignMenuId((cur) =>
                                  cur === t.id ? null : t.id,
                                )
                              }
                            >
                              {assigningId === t.id ? "Assigning…" : "Assign"}
                            </button>
                            {assignMenuId === t.id && (
                              <div className={styles.assignMenu} role="menu">
                                <div className={styles.assignMenuHead}>
                                  ASSIGN TO AGENT
                                </div>
                                {agents.map((a) => {
                                  const c = getAvatarColor(a.name);
                                  const busyOn = busyAgentTask.get(a.name);
                                  return (
                                    <button
                                      key={a.name}
                                      type="button"
                                      role="menuitem"
                                      className={styles.assignOption}
                                      data-busy={busyOn ? "true" : undefined}
                                      disabled={Boolean(busyOn)}
                                      title={
                                        busyOn
                                          ? `Already on ${busyOn}`
                                          : `Assign to ${a.name}`
                                      }
                                      onClick={() =>
                                        void assignTask(t.id, a.name)
                                      }
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
                                      {busyOn && (
                                        <span className={styles.assignBusy}>
                                          on task
                                        </span>
                                      )}
                                    </button>
                                  );
                                })}
                                {freeAgents.length === 0 && (
                                  <p className={styles.assignEmpty}>
                                    All agents are busy. Free one up or add an
                                    agent.
                                  </p>
                                )}
                              </div>
                            )}
                          </div>
                        )}
                      </article>
                    );
                  })}
              </section>
            );
          })
        )}

        <section className={styles.queueSection}>
          <div className={styles.queueSubRow}>
            <h2 className={styles.queueSub}>WORKER HISTORY</h2>
            <span className={styles.queueCount}>{agents.length}</span>
          </div>
          {agents.slice(0, 4).map((a) => (
            <article key={a.name} className={styles.queueCard}>
              <div className={styles.queueCardRow}>
                <strong className={styles.workerName}>{a.name}</strong>
                <span className={styles.workerTag}>{formatStatusLabel(parseLoomStatus(a.status).type)}</span>
                <span className={styles.workerTag2}>{a.branch ? "active" : "idle"}</span>
              </div>
              <p className={styles.workerMeta}>{a.branch ?? a.name}</p>
            </article>
          ))}
        </section>

      </aside>
    </div>
  );
}
