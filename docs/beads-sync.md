# Beads Sync Setup

How to configure beads (`bd`) for automatic sync via a dedicated git branch.

## Overview

Beads data lives on a dedicated `beads-sync` branch, not `main`. The daemon
auto-commits and auto-pushes changes to this branch on every mutation. Fresh
clones hydrate from `origin/beads-sync` via `bd init`.

## Setup

### 1. Remove JSONL data files from main branch

These files are redundant since data lives on `beads-sync`:

```bash
git rm --cached --sparse .beads/issues.jsonl .beads/interactions.jsonl
```

### 2. Update `.beads/.gitignore`

Add these lines to prevent the JSONL files from being re-tracked on main:

```gitignore
# JSONL data files (synced via beads-sync branch, not main)
issues.jsonl
interactions.jsonl
```

### 3. Update `.beads/config.yaml`

Add daemon and sync settings. The YAML keys must be nested (not flat) because
beads uses viper which maps dot-separated keys to nested YAML:

```yaml
# Daemon settings
daemon:
  # Auto-sync: auto-commit + auto-push + auto-pull on every change
  auto-sync: true

# Sync settings
sync:
  # Mode: realtime exports JSONL on every database mutation
  mode: "realtime"
  # Trigger export/import on every change (not just push/pull)
  export_on: "change"
  import_on: "change"
```

The repo must already have `sync-branch: "beads-sync"` set (top-level key).

### 4. Restart the daemon

**Important:** `bd daemons restart` does NOT read `daemon.auto-sync` from
config.yaml. You must kill the daemon and start it fresh:

```bash
kill $(cat .beads/daemon.pid)
sleep 1
bd daemon start --auto-commit --auto-push
```

Alternatively, `bd daemon start` without flags should pick up
`daemon.auto-sync: true` from config.yaml, but passing explicit flags is
more reliable.

### 5. Verify

```bash
# Check sync mode and triggers
bd sync --status
# Expected:
#   Sync mode: realtime (JSONL exported on every change)
#   Export on: change, Import on: change

# Check daemon flags
grep "auto-commit" .beads/daemon.log | tail -1
# Expected: auto-commit: true, auto-push: true
```

### 6. Commit and push

```bash
git add .beads/.gitignore .beads/config.yaml
git commit -m "Configure beads auto-sync via beads-sync branch"
git push
```

## How it works

| Component | Role |
|-----------|------|
| `sync-branch: "beads-sync"` | Data lives on this branch, not main |
| `sync.mode: "realtime"` | Export JSONL on every DB mutation |
| `sync.export_on: "change"` | Trigger export on every change (not just push) |
| `sync.import_on: "change"` | Trigger import on every change (not just pull) |
| `daemon.auto-sync: true` | Daemon auto-commits + pushes to beads-sync |

The flow on every mutation:

1. DB mutation happens (e.g. `bd create`, `bd close`)
2. Daemon exports to `.git/beads-worktrees/beads-sync/.beads/issues.jsonl`
3. Daemon commits to the local `beads-sync` branch
4. Daemon pushes `beads-sync` to origin

The daemon runs on a ~5 second interval.

## Fresh clones

On a fresh clone, the data is pulled from `origin/beads-sync` automatically:

```bash
git clone <repo>
cd <repo>
bd init   # auto-detects sync-branch from config.yaml, imports from origin/beads-sync
```

The `post-merge` git hook (installed by `bd init`) handles subsequent imports
after `git pull`.

## Gotchas

- **`bd daemons restart` vs `bd daemon start`**: The restart command does not
  read config.yaml for auto-sync settings. Always use kill + `bd daemon start`.
- **YAML nesting**: `sync.mode` must be written as nested YAML (`sync:` +
  `mode:`), not as a flat key like `sync-mode:`.
- **`bd sync` is still available**: You can run it manually at any time. With
  auto-sync enabled, it's just not required.
- **No `bd sync` needed in session close protocol**: Once auto-sync is
  configured, the daemon handles it. You still need `git push` for code changes
  on main.
