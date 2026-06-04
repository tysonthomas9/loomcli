/**
 * AgentsPage — full-page lead-agent view (Aether `/agents`).
 *
 * Ported from the design's AgentDetailsPanel: an agent-selector rail, a tabbed
 * main panel (Terminal / Info / Git / Diff / Files) centered on the selected
 * agent's terminal, and a right-hand Open Queue / Worker History side panel.
 *
 * Live agents drive the selector + header; terminal output, git/diff/files and
 * the queue are presentational stubs (no live per-agent transcript/queue source
 * is wired to the web UI yet).
 */

import { useEffect, useMemo, useState, lazy, Suspense } from "react";

import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { GitTab, AgentLogsTab } from "@/components/AgentDetailPanel";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { parseLoomStatus } from "@/types";

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

const CAPABILITIES = ["TypeScript", "Node.js", "Git", "README", "Testing", "Diagnostics"];
const RECENT_ACTIVITY = [
  { id: "ra1", tone: "var(--color-status-done)", time: "12m ago" },
  { id: "ra2", tone: "var(--color-status-dirty)", time: "34m ago" },
  { id: "ra3", tone: "var(--color-status-working)", time: "1h ago" },
  { id: "ra4", tone: "var(--color-status-review)", time: "3h ago" },
];

export function AgentsPage(): JSX.Element {
  const { agents, issues } = useWorkspaceViewData();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<AgentTab>("terminal");

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
  const role = parseLoomStatus(selected.status).type;

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
                  <span>{role}</span>
                  <span>·</span>
                  <span>{selected.branch ?? "lead"}</span>
                  <span>·</span>
                  <span>no epic assigned</span>
                </p>
              </div>
            </header>
            <div className={styles.realTabBody}>
              <AgentLogsTab
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
                  <p className={styles.infoSub}>{role} agent · isolated workspace runtime</p>
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
            <div className={styles.infoTwoCol}>
              <section className={styles.card}>
                <h2 className={styles.cardLabel}>Capabilities</h2>
                <div className={styles.tagRow}>
                  {CAPABILITIES.map((c) => (
                    <span key={c} className={styles.tag}>{c}</span>
                  ))}
                </div>
                <div className={styles.configBox}>
                  <p className={styles.configTitle}>Config</p>
                  <dl className={styles.configGrid}>
                    <div><dt>Runtime</dt><dd>embedded fleet-db</dd></div>
                    <div><dt>Backend</dt><dd>codex</dd></div>
                    <div><dt>Mode</dt><dd>retained</dd></div>
                  </dl>
                </div>
              </section>
              <section className={styles.card}>
                <h2 className={styles.cardLabel}>Recent Activity</h2>
                <ul className={styles.activity}>
                  {RECENT_ACTIVITY.map((a) => (
                    <li key={a.id} className={styles.activityItem}>
                      <span className={styles.activityDot} style={{ backgroundColor: a.tone }} aria-hidden="true" />
                      <span className={styles.activityText}>agent performed an action</span>
                      <time className={styles.activityTime}>{a.time}</time>
                    </li>
                  ))}
                </ul>
              </section>
            </div>
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
            openTasks.slice(0, 4).map((t) => (
              <article key={t.id} className={styles.queueCard}>
                <div className={styles.queueCardRow}>
                  <span className={styles.queueCardDot} aria-hidden="true" />
                  <code className={styles.queueCardKey}>{t.id}</code>
                  <span className={styles.p2Badge}>P{t.priority}</span>
                </div>
                <p className={styles.queueCardTitle}>{t.title}</p>
              </article>
            ))
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
