# Loom Command Reference

Use JSON output for inspection and concise human output for mutation confirmation.

## Issue reads

```sh
loom data show <id> --output json
loom data list --status=open --output json
loom data list --status=review --output json
loom data list --status=in_progress --output json
loom data list --status=open --type=epic --output json
loom data blocked --output json
loom data list --limit=10 --output json
```

## Issue mutations

Always obtain approval first.

```sh
loom data create --title "<title>" --type task --priority 2 \
  --parent <epic-id> --source-repo <repo>

loom data update <id> --status <status>
loom data update <id> --assignee <name>
loom data update <id> --add-label <label>
loom data update <id> --remove-label <label>
loom data comment <id> "<focused context or feedback>"
loom data close <id> --reason="<reason>"
```

Types are `task`, `bug`, `feature`, and `epic`. Priorities run from `0` (critical) to `4` (backlog).

For dependencies, confirm the local CLI's `loom data update --help` syntax before changing an existing edge. After rewiring, re-read every affected ticket and compare its dependency IDs.

## Repository and role inspection

```sh
loom repo list --json
loom role list
loom role show <role>
loom agentdef list --json
loom backend health --json
```

## Agent definitions

```sh
loom agentdef add <name> --role <plan|task> --auto \
  --repos <repo> --parent <epic-id> --task-filter <filter> \
  --backend <backend>

loom agentdef start <name>
loom agentdef stop <name>
loom agentdef stop <name> --force
loom agentdef remove <name>
```

`start` and `stop` change desired state. They do not start the desktop daemon process.

## Runtime and monitoring

```sh
loom workspace ops diagnose --json
loom workspace ops ensure-runtime --json
loom workspace ops status --json
loom daemon queue <agent>
```

Prefer `ensure-runtime` for runtime startup or repair. Do not use foreground or `nohup` daemon commands in lead mode.

## Plan review loop

```sh
loom data show <id>
loom data update <id> --status open --remove-label needs-revision
```

For requested changes:

```sh
loom data comment <id> "FEEDBACK: <specific required changes>"
loom data update <id> --status open
```

## Status report shape

Report only high-signal fields:

```text
Review: <count> (<ids>)
Blocked: <count> (<ids and reasons>)
In progress: <id> -> <agent>/<phase>
Planner: <backend>, <state>/<desired>, <active task or idle>
Runner: <backend>, <state>/<desired>, <active task or idle>
Runtime: <ready or exact blocker>
Next approval needed: <mutation or none>
```

