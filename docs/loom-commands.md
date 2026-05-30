# Loom Command Reference

This document describes the `loom` commands registered in this checkout.
It is based on the Cobra command definitions under `internal/cli` and the
generated root help from `go run ./cmd/loom --help`.

## Global Flags

These flags are available on every command:

| Flag | Meaning |
| --- | --- |
| `--backend <name>` | AI backend CLI to use. Supported values in this branch include `claude`, `codex`, and `opencode`. Can also be set with `LOOM_BACKEND`. |
| `--log-format text\|json` | Log format. Defaults to `text`. |
| `--log-output stderr\|<path>` | Log destination. Defaults to `stderr`. |
| `-w, --worktrees <path>` | Override the worktrees directory. Takes precedence over `LOOM_WORKTREES_DIR`. |
| `-h, --help` | Show help. |

The root command also supports `-v, --version`.

## Agent Commands

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom plan [worktree|workspace]` | Run a planning agent. Picks a task, researches it, writes a design, and moves it to review. | `-a, --auto`; `-i, --interval`; `-m, --max-tasks`; `-t, --idle-timeout`; `--parent`; hidden `--daemon-mode`. |
| `loom task [worktree|workspace]` | Run an implementation agent. Picks ready work, implements it, tests, commits, pushes, and closes the task. | `-a, --auto`; `-i, --interval`; `-m, --max-tasks`; `-t, --idle-timeout`; `--parent`; hidden `--daemon-mode`. |
| `loom agent <worktree> --prompt <path>` | Run a custom agent with a prompt template. | `-p, --prompt`; `-f, --task-filter`; `-a, --auto`; `-i, --interval`; `-m, --max-tasks`; `-t, --idle-timeout`; `--parent`; hidden `--daemon-mode`. |
| `loom lead` | Start an interactive project-management AI agent for backlog and plan review. | Uses global backend/log/worktree flags. |
| `loom list` | List configured agents/worktrees and their status. | Uses global flags. |
| `loom monitor` | Display a live agent and task dashboard. | `-b, --branch`; `-n, --no-watch`; `-i, --interval`. |
| `loom recover <worktree>` | Recover an agent from an error state by clearing stale locks and/or resetting tasks. | `--no-analyze`; `--force`. |
| `loom daemon` | Start the agent supervisor daemon in the foreground. | `--dry-run`. |
| `loom daemon status` | Show daemon and supervised-agent status. | Uses global flags. |
| `loom daemon stop` | Stop the running agent supervisor daemon. | Uses global flags. |
| `loom epic-run [epic-id]` | Run an epic end-to-end with an AI orchestrator. | `--worktree`; `--resume`. |
| `loom pipeline generate <epic-id>` | Generate an Agentflow pipeline from an epic task DAG. | `-o, --output`; `--worktree`. |
| `loom task-run` | Run one non-interactive agent for one task, mainly for pipelines. | `--task`; `--role`; `--worktree`; `--parent`; `--json`; `--style`. |
| `loom usage` | Display token usage and cost summaries. | `--agent`; `--backend`; `--epic`; `--since`; `--until`; `--today`; `--week`; `--format`; `--verbose`. |
| `loom worker` | Run a remote worker that connects to a control plane. | `--control-plane`; `--workspace`; `--agent`; `--backend`; `-i, --interval`; `-m, --max-tasks`; `-t, --idle-timeout`; `--parent`. |

## Git Operations

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom push [worktree] [target]` | Push one worktree branch to a target branch. | `-a, --all`; `-W, --workspace`. |
| `loom pull [worktree] [branch]` | Pull a branch into one worktree. | `-a, --all`; `-W, --workspace`. |
| `loom sync` | Push completed work, then pull latest into worktrees. | `--push-only`; `--pull-only`; `-W, --workspace`. |
| `loom reset <worktree> [branch]` | Hard reset a worktree to a branch. | `-a, --all`; `-p, --push`; `-f, --force`. |
| `loom pr [worktree] [target]` | Create a GitHub PR from a worktree branch. | `-a, --all`; `-W, --workspace`. |

## Configuration Commands

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom config` | Parent command for configuration management. | Use one of the subcommands below. |
| `loom config init` | Create a new loom config file. | `--force`; `--workspace`. |
| `loom config show` | Display current loom configuration. | Uses global flags. |
| `loom config add-repo <workspace> <repo-name>` | Add a repository to a workspace. | `--path`; `--branch`; `--remote`. |
| `loom config remove-repo <workspace> <repo-name>` | Remove a repository from a workspace. | Uses global flags. |
| `loom config migrate [path]` | Migrate config files to the current version. | `--project`. |
| `loom backend` | Parent command for AI backend management. | Use one of the subcommands below. |
| `loom backend list` | List registered AI backends. | `--json`. |
| `loom backend health` | Check health status for all backends. | `--json`. |
| `loom backend info <name>` | Show detailed information for one backend. | `--json`. |

## Workspace And System Commands

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom workspace` | Parent command for multi-repo workspaces. | Use one of the subcommands below. |
| `loom workspace create <name>` | Create a workspace with git worktrees. | `--repos`; `--path`; `--default`; `--branch`. |
| `loom workspace list` | List configured workspaces. | `--json`. |
| `loom workspace remove <name>` | Remove a workspace and its worktrees. | `--force`; `--keep-worktrees`. |
| `loom status` | Show a system overview. | `--json`; `-b, --branch`. |
| `loom doctor` | Check installation and configuration health. | `--json`. |
| `loom init` | Initialize loom with guided setup. | `-y, --yes`; `--worktrees-dir`; `--names`; `--workspace`. |
| `loom setup` | Run the comprehensive setup wizard. | `-y, --yes`; `--backend`. |
| `loom serve` | Start the HTTP API and web UI server. | `-p, --port`; `--bind`; `--cors`; `--webui-port`; `--webui-socket`; `--no-webui`; `--no-daemon`; `--redis-addr`; `--redis-password`; `--api-key`; `--fleet-api-key`; `--auth`; `--hsts`; `--auth-url`; `--auth-issuer`; `--auth-audience`; `--auth-allow-insecure`; `--dev`; `--dev-frontend-dir`; `--sentry-dsn`. |

## Session And Automation Commands

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom sessions` | Parent command for agent sessions. | Use one of the subcommands below. |
| `loom sessions clean` | Remove old session data. | `--older-than`. |
| `loom claim <task-id>` | Update the local agent lock file with the task being worked on. | Uses global flags. |
| `loom complete` | Signal task completion to auto mode. | Uses global flags. |

## Lifecycle Hooks

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom hooks` | Parent command for lifecycle hooks. | Use one of the subcommands below. |
| `loom hooks install [worktree-path]` | Install loom hooks into Claude Code settings. | Uses global flags. |
| `loom hooks uninstall [worktree-path]` | Remove loom hooks from Claude Code settings. | Uses global flags. |
| `loom hooks status [worktree-path]` | Show hook installation status. | Uses global flags. |
| `loom hooks claude-code` | Parent command for Claude Code hook handlers. | Usually called by Claude Code settings. |
| `loom hooks claude-code session-start` | Handle Claude Code `SessionStart`. | Internal hook entrypoint. |
| `loom hooks claude-code user-prompt-submit` | Handle Claude Code `UserPromptSubmit`. | Internal hook entrypoint. |
| `loom hooks claude-code stop` | Handle Claude Code `Stop`. | Internal hook entrypoint. |
| `loom hooks claude-code session-end` | Handle Claude Code `SessionEnd`. | Internal hook entrypoint. |
| `loom hooks claude-code pre-task` | Handle Claude Code `PreToolUse[Task]`. | Internal hook entrypoint. |
| `loom hooks claude-code post-task` | Handle Claude Code `PostToolUse[Task]`. | Internal hook entrypoint. |

## Completion And Help Commands

| Command | Purpose |
| --- | --- |
| `loom completion bash` | Generate shell completion for Bash. |
| `loom completion fish` | Generate shell completion for Fish. |
| `loom completion powershell` | Generate shell completion for PowerShell. |
| `loom completion zsh` | Generate shell completion for Zsh. |
| `loom help [command]` | Show help for a command. |

## Hidden Internal Commands

| Command | Purpose | Important flags |
| --- | --- | --- |
| `loom log-router` | Route stdin to agent and task log files. Hidden from normal help output and intended for internal use. | `--agent`; `--base-dir`; `--lock-path`; `--max-log-size`; `--workspace-id`. |

## Notes

- `loom daemon` is the command that starts the loom agent supervisor process.
- `loom serve` auto-starts the underlying `bd` issue backend daemon unless `--no-daemon` is set; that is separate from the loom agent supervisor daemon.
- `plan`, `task`, and `agent` have hidden `--daemon-mode` flags used by daemon/tmux automation. Users should normally use `--auto` or `loom daemon` instead.
- Command behavior depends on the configured issue backend, active workspace, worktree layout, and selected AI backend.
