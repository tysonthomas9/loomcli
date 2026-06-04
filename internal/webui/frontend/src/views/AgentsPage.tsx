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

import { useEffect, useMemo, useState } from "react";

import { useWorkspaceViewData } from "@/contexts/WorkspaceViewContext";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";
import { parseLoomStatus } from "@/types";

import styles from "./AgentsPage.module.css";

type AgentTab = "terminal" | "info" | "git" | "diff" | "files";

const TABS: { id: AgentTab; label: string }[] = [
  { id: "terminal", label: "Terminal" },
  { id: "info", label: "Info" },
  { id: "git", label: "Git" },
  { id: "diff", label: "Diff" },
  { id: "files", label: "Files" },
];

const TERMINAL_BLOCKS = [
  {
    id: "repo-list",
    command: "repo list --json",
    lines:
      '└ time=2026-05-19T05:13:14.923Z level=INFO msg="opened existing embedded fleet-db client"\n  url=http://127.0.0.1:36039\n  [\n    … +9 lines (ctrl + t to view transcript)\n  }\n]',
  },
  {
    id: "diagnose",
    command: "workspace ops diagnose --json",
    lines:
      '└ time=2026-05-19T05:13:14.975Z level=INFO msg="opened existing embedded fleet-db client"\n  url=http://127.0.0.1:36039\n  {\n    … +124 lines (ctrl + t)\n  ]\n}',
  },
  {
    id: "role-list",
    command: "role list",
    lines:
      "└ lead                 Lead orchestrator terminal\n  plan                 Planning agent\n  task                 Task implementation agent",
  },
];

const CAPABILITIES = ["TypeScript", "Node.js", "Git", "README", "Testing", "Diagnostics"];
const RECENT_ACTIVITY = [
  { id: "ra1", tone: "var(--color-status-done)", time: "12m ago" },
  { id: "ra2", tone: "var(--color-status-dirty)", time: "34m ago" },
  { id: "ra3", tone: "var(--color-status-working)", time: "1h ago" },
  { id: "ra4", tone: "var(--color-status-review)", time: "3h ago" },
];
const GIT_COMMITS = [
  { id: "9a4c2f1", message: "feat: add README runner note", author: "hello-world-agent", date: "May 19, 2026 07:13", tone: "var(--color-warning)" },
  { id: "61bd930", message: "fix: update workspace diagnostics", author: "demo", date: "May 19, 2026 06:45", tone: "var(--color-info)" },
  { id: "a18f04b", message: "chore: remove planner agent definition", author: "test2", date: "May 19, 2026 05:23", tone: "var(--color-success)" },
];
const DIFF_FILES = [
  { id: "readme", badge: "M", name: "README.md" },
  { id: "config", badge: "A", name: ".loom/config.json" },
  { id: "package", badge: "M", name: "package.json" },
];
const DIFF_LINES = [
  { id: "d1", kind: "context", o: "1", n: "1", text: "# Hello World" },
  { id: "d3", kind: "context", o: "3", n: "3", text: "A sample hello-world repository." },
  { id: "d4", kind: "removed", o: "5", n: "", text: "Run tests with npm test" },
  { id: "d5", kind: "added", o: "", n: "5", text: "## Runner Execution" },
  { id: "d7", kind: "added", o: "", n: "7", text: "`loom run --watch hello-world`" },
  { id: "d8", kind: "context", o: "7", n: "10", text: "## Getting Started" },
];
const BROWSER_FILES = ["index.ts", "app.ts", "helpers.ts", "logger.ts", "index.test.ts", "README.md", "package.json", "tsconfig.json"];

export function AgentsPage(): JSX.Element {
  const { agents, issues } = useWorkspaceViewData();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<AgentTab>("terminal");
  const [diffMode, setDiffMode] = useState<"unified" | "split">("unified");

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
            <div className={styles.terminalOutput}>
              {TERMINAL_BLOCKS.map((block) => (
                <section key={block.id} className={styles.termBlock}>
                  <p className={styles.termRan}>
                    <span className={styles.termDot} aria-hidden="true" />
                    <span>Ran</span>
                    <code className={styles.termCmd}>loom {block.command}</code>
                  </p>
                  <pre className={styles.termPre}>{block.lines}</pre>
                </section>
              ))}
            </div>
            <section className={styles.terminalInput} aria-label="Agent terminal input">
              <label className={styles.inputRow}>
                <span className={styles.prompt}>&gt;</span>
                <input className={styles.cmdInput} placeholder="Explain this codebase" type="text" />
              </label>
              <label className={styles.notesRow}>
                <input className={styles.notesInput} placeholder="Add notes..." type="text" />
              </label>
            </section>
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
          <div className={styles.scrollPanel}>
            <section className={styles.card} style={{ padding: 0 }}>
              <header className={styles.gitHead}>Git history</header>
              <ul className={styles.gitList}>
                {GIT_COMMITS.map((c) => (
                  <li key={c.id} className={styles.gitItem}>
                    <span className={styles.gitDot} style={{ backgroundColor: c.tone }} aria-hidden="true" />
                    <span className={styles.gitBody}>
                      <strong className={styles.gitMsg}>{c.message}</strong>
                      <span className={styles.gitMeta}>{c.author} · {c.date}</span>
                    </span>
                    <code className={styles.gitHash}>{c.id}</code>
                  </li>
                ))}
              </ul>
            </section>
          </div>
        )}

        {activeTab === "diff" && (
          <div className={styles.splitPanel}>
            <aside className={styles.diffSidebar} aria-label="Changed files">
              <div className={styles.diffModes}>
                <button type="button" className={styles.diffModeBtn} data-active={diffMode === "unified" || undefined} onClick={() => setDiffMode("unified")}>Unified</button>
                <button type="button" className={styles.diffModeBtn} data-active={diffMode === "split" || undefined} onClick={() => setDiffMode("split")}>Split</button>
              </div>
              {DIFF_FILES.map((f, i) => (
                <button key={f.id} type="button" className={styles.diffFile} data-active={i === 0 || undefined}>
                  <span className={styles.diffBadge}>{f.badge}</span>
                  <span>{f.name}</span>
                </button>
              ))}
            </aside>
            <section className={styles.diffViewer} aria-label="Unified diff viewer">
              <h1 className={styles.diffTitle}>README.md</h1>
              <div className={styles.diffTable}>
                {DIFF_LINES.map((l) => (
                  <div key={l.id} className={styles.diffLine} data-kind={l.kind}>
                    <span className={styles.diffNum}>{l.o}</span>
                    <span className={styles.diffNum}>{l.n}</span>
                    <code className={styles.diffText}>{l.text || " "}</code>
                  </div>
                ))}
              </div>
            </section>
          </div>
        )}

        {activeTab === "files" && (
          <div className={styles.splitPanel}>
            <aside className={styles.filesSidebar} aria-label="File browser">
              <label className={styles.fileFilter}>
                <input placeholder="Filter files..." type="text" />
              </label>
              <div className={styles.fileList}>
                {BROWSER_FILES.map((f, i) => (
                  <button key={f} type="button" className={styles.fileItem} data-active={f === "README.md" || undefined} style={{ paddingLeft: `${8 + (i < 5 ? 16 : 8)}px` }}>
                    {f}
                  </button>
                ))}
              </div>
            </aside>
            <section className={styles.fileContent} aria-label="File content">
              <header className={styles.fileContentHead}>README.md</header>
              <pre className={styles.fileBody}>{` 1  # Hello World\n 2\n 3  A sample hello-world repository.\n 4\n 5  ## Runner Execution\n 6\n 7  This repository includes a runner test that can be observed via:\n 9  ## Getting Started\n10  Clone the repo and run npm install`}</pre>
            </section>
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
