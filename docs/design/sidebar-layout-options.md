# Sidebar Layout Options

Design exploration for the left panel of the Loom WebUI. Documented 2026-04-09.

## Context

The sidebar manages workspace navigation, agent visibility, and task monitoring. Key concepts:

- **Workspaces** — isolated project environments, each with repos and agents
- **Static agents** — persistent, named worktrees (leads, reviewers, monitors). You interact with them.
- **Ephemeral agents** — spin up per task, push code, die. Visible only as task status.
- **Repos** — git repositories within a workspace
- **Epics/Tasks** — work items tracked by the bd daemon

Agent scopes:
- Single-repo: works on one repo, has a branch (`loomcli · feature/auth`)
- Workspace-level: works across all repos (`workspace`)

---

## Option A: Flat Sections (original)

Sections are independent peers. No visual hierarchy between workspace and its contents.

```
┌──────────────────────────────┐
│ Workspaces                   │
│   demo                       │
│   test-ws                    │
│   Paperclip  <-- current     │
├──────────────────────────────┤
│ loomcli                  2   │
│   foxtrot            Ready   │
│   nexus              Ready   │
├──────────────────────────────┤
│ Epics & Tasks                │
│   V2 Evolution               │
│     Set session perms        │
├──────────────────────────────┤
│ Work Queue                   │
│   Open 174  Review 4         │
└──────────────────────────────┘
```

**Pros:** Simple, each section independent.
**Cons:** No relationship between workspace and contents. Switching workspace reloads everything below but nothing visually connects them.

---

## Option B: Nested Tree (workspace -> repo -> agent)

Full hierarchy visible across all workspaces simultaneously.

```
┌──────────────────────────────┐
│ > demo                       │
│   > loomcli                  │
│       foxtrot        Ready   │
│       nexus          Ready   │
│ < test-ws                    │
│ > Paperclip  <-- current     │
│   > loomcli                  │
│       foxtrot        Ready   │
│       nexus          Ready   │
├──────────────────────────────┤
│ Epics & Tasks                │
│   V2 Evolution               │
│     Set session perms        │
├──────────────────────────────┤
│ Work Queue                   │
│   Open 174  Review 4         │
└──────────────────────────────┘
```

**Pros:** See agents across ALL workspaces. Collapsed workspaces hide their tree.
**Cons:** Gets tall fast with many agents/repos. Epics stay separate (scoped to current workspace).

---

## Option C: Workspace as Context

Current workspace is THE context. Everything in the panel belongs to it. Other workspaces demoted to bottom.

```
┌──────────────────────────────┐
│ Paperclip                 >  │  <-- dropdown to switch
├──────────────────────────────┤
│ > loomcli                2   │
│     foxtrot          Ready   │
│     nexus            Ready   │
│                              │
│ > fleet-db               1   │
│     alpha          Working   │
├──────────────────────────────┤
│ V2 Evolution                 │
│   Set session perms          │
│ Better Auth                  │
│   Frontend: Tests            │
├──────────────────────────────┤
│ Work Queue                   │
│   Open 174  Review 4         │
├──────────────────────────────┤
│ Other workspaces             │
│   demo                       │
│   test-ws                    │
└──────────────────────────────┘
```

**Pros:** Cleanest hierarchy. Workspace switching is a context change.
**Cons:** Can't see other workspaces' agents without switching.

---

## Option C2: Flat Agents with Task Linking

Agents flat (not grouped by repo). Repo is metadata on the card. Agent-task relationship visible in both directions.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Agents                       │
│ f  foxtrot       Working     │
│    Set session perms         │  <-- current task inline
│    loomcli                   │  <-- repo chip
│                              │
│ n  nexus             Ready   │
│    loomcli                   │
│                              │
│ w  orchestrator      Ready   │
│    all repos                 │  <-- cross-repo
├──────────────────────────────┤
│ Epics                        │
│ > V2 Evolution               │
│     Set session perms    Y   │  <-- dot = agent working
│   + Add task                 │
│ > Better Auth                │
│     Frontend: Tests          │
├──────────────────────────────┤
│ Work Queue                   │
│   Open 174  Review 4         │
├──────────────────────────────┤
│ Other workspaces             │
│   demo  test-ws              │
└──────────────────────────────┘
```

**Pros:** Handles cross-repo agents naturally. Shows agent-task relationship.
**Cons:** Long list with many agents. Repo grouping lost.

---

## Option D: Two-Panel (workspace rail + detail)

Narrow workspace rail on the left, detail pane on the right. Always see all workspaces.

```
┌──────┬───────────────────────┐
│ demo │ loomcli            2  │
│ t-ws │   foxtrot      Ready  │
│ >Ppr │   nexus        Ready  │
│      │                       │
│      │ V2 Evolution          │
│      │   Set perms           │
│      │                       │
│      │ Work Queue            │
│      │  Open 174  Review 4   │
└──────┴───────────────────────┘
```

**Pros:** Always see all workspaces. Clicking one loads its contents.
**Cons:** Takes more horizontal space. Like Slack's workspace switcher.

---

## Direction 1: Activity-First

Groups agents by what they're DOING, not where they live.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Active                       │
│ f foxtrot > Set session perms│
│   loomcli  working 5m       │
│                              │
│ Idle                         │
│ n nexus  loomcli             │
│ w orchestrator  all repos    │
├──────────────────────────────┤
│ Epics                        │
│ ...                          │
└──────────────────────────────┘
```

**Pros:** Focus on what's happening now.
**Cons:** Hides idle agent details. Agents jump between groups as status changes.

---

## Direction 2: Task-Centric (epics own everything)

Agents live UNDER the tasks they're working on. No separate agent section.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ > V2 Evolution               │
│     Set session perms        │
│       f foxtrot  Working     │
│     Add migration tool       │
│       (unassigned)           │
│                              │
│ > Better Auth                │
│     Frontend: Tests          │
│       n nexus    Ready       │
│                              │
│ Unassigned agents            │
│   w orchestrator             │
├──────────────────────────────┤
│ Work Queue                   │
│   Open 174  Review 4         │
└──────────────────────────────┘
```

**Pros:** Work structure and assignment in one tree. Work matters more than workers.
**Cons:** Agents without tasks are orphaned. Agent list is fragmented.

---

## Direction 3: Kanban Lanes (vertical)

Agent status lanes like a mini kanban.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Working                    1 │
│ foxtrot                      │
│ Set session perms  5m        │
│                              │
│ Ready                      2 │
│   nexus  orchestrator        │
│                              │
│ Review                     0 │
│ Error                      0 │
├──────────────────────────────┤
│ Epics ...                    │
└──────────────────────────────┘
```

**Pros:** Pipeline view — how many working, ready, in review, errored.
**Cons:** Doesn't show task context. Agents jump lanes constantly.

---

## Direction 4: Unified Timeline

No sections — just a feed of what happened.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Now                          │
│ f foxtrot started            │
│   Set session perms          │
│ 2m ago                       │
│ n nexus completed            │
│   Frontend: Tests            │
│ 5m ago                       │
│ w orchestrator claimed       │
│   Migration tool             │
├──────────────────────────────┤
│ Agents 3 active  0 idle      │
│ Epics  3 open    1 done      │
│ Queue  174 open  4 review    │
└──────────────────────────────┘
```

**Pros:** "What did I miss" at a glance.
**Cons:** Hard to find a specific agent. Historical events push current state down.

---

## Direction 5: Two-Tier Linked

Agents top, tasks bottom, visual links between them.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ f foxtrot ---+  Ready        │
│ n nexus      |  Ready        │
│ w orch ------+  Working      │
├──────────────+───────────────┤
│ V2 Evol      |               │
│   perms  <---+  <-- linked   │
│   migrate                    │
│ Auth                         │
│   tests                      │
├──────────────────────────────┤
│ Open 174  Review 4           │
└──────────────────────────────┘
```

**Pros:** Shows agent-task mapping directly.
**Cons:** Visual clutter at scale. Lines get messy with many agents.

---

## Chosen: Static/Ephemeral Split with Running Tasks

Separates static agents (persistent, interactive) from ephemeral agents (per-task, transient). Ephemeral agents appear as task status, not as agent cards.

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Agents                       │
│   f  foxtrot     Planning    │
│      loomcli · feature/auth  │
│   n  nexus          Idle     │
│      fleet-db · main         │
│   w  sentinel       Idle     │
│      workspace               │
│                              │
│ + Add agent                  │
├──────────────────────────────┤
│ Running                      │
│ V2 Evolution             1/4 │
│   @ Frontend: Tests     12m  │
│                              │
│ Better Auth              0/3 │
│   @ OAuth flow           4m  │
│   @ Token refresh        1m  │
├──────────────────────────────┤
│ Repos                        │
│   loomcli            main    │
│   fleet-db           main    │
│   superset         develop   │
├──────────────────────────────┤
│ Queued 2 · Done 12 · Failed 0│
├──────────────────────────────┤
│ :: demo                    … │
│ :: test-ws                 … │
└──────────────────────────────┘
```

When nothing running, the Running section disappears entirely:

```
┌──────────────────────────────┐
│ Paperclip                 >  │
├──────────────────────────────┤
│ Agents                       │
│   f  foxtrot        Idle     │
│      loomcli · main          │
│   n  nexus          Idle     │
│      fleet-db · main         │
│                              │
│ + Add agent                  │
├──────────────────────────────┤
│ Repos                        │
│   loomcli            main    │
│   fleet-db           main    │
│   superset         develop   │
├──────────────────────────────┤
│ Queued 0 · Done 16 · Failed 0│
├──────────────────────────────┤
│ :: demo                    … │
│ :: test-ws                 … │
└──────────────────────────────┘
```

### Why this layout

- **Static agents**: always visible, stable list, clickable to interact
- **Ephemeral agents**: visible only as running task indicators, no card clutter
- **Running section**: real-time pulse of what's happening, disappears when idle
- **Repos**: workspace's code inventory, independent of agents
- **Other workspaces**: always accessible at bottom, compact, draggable

### Agent second line shows scope

- `loomcli · feature/auth` — single-repo agent with branch
- `workspace` — workspace-level agent, works across all repos

### Key decisions

- No duplicate agent displays (was showing same agents twice)
- No radio buttons for repo filtering
- No full epic tree in sidebar (lives in main content area)
- Running section shows only epics with active ephemeral tasks
- Epic progress shown as fraction (1/4, 0/3)
- Queue stats as compact single line

---

## Comparison Matrix

| Layout | Best for | Agent scale | Task visibility | Complexity |
|--------|----------|-------------|-----------------|------------|
| A: Flat sections | Simple projects | Low | Full tree | Low |
| B: Nested tree | Multi-workspace overview | Medium | Separate | Medium |
| C: Workspace context | Single-workspace focus | Medium | Full tree | Low |
| C2: Flat + linking | Multi-repo, few agents | Low | Linked | Medium |
| D: Two-panel | Many workspaces | Medium | Full tree | High |
| Dir 1: Activity-first | Monitoring | Any | Minimal | Low |
| Dir 2: Task-centric | Work-driven teams | Low | Inline | Medium |
| Dir 3: Kanban lanes | Pipeline monitoring | Medium | Minimal | Low |
| Dir 4: Timeline | Catch-up view | Any | Inline | Medium |
| Dir 5: Two-tier linked | Small teams | Low | Linked | High |
| **Chosen: Static/Ephemeral** | **Multi-agent orchestration** | **Any** | **Running only** | **Medium** |
