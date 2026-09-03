# Loom Glossary

This repo uses several ordinary words as Loom-specific concepts. In this
codebase, `loom` means the AI-agent orchestration platform backed by fleet-db,
not the video product or Java Project Loom.

## Core Objects

- **Workspace**: A fleet-db-scoped project container. Workspaces own repos,
  roles, agent definitions, issues, daemon profiles, and local runtime state.
- **Role**: The reusable configuration for an agent: prompt selection, backend,
  model, task filter, tool policy, and role `kind`.
- **Agent**: A named assignment that uses a role. Worker agents are supervised
  by daemon/runtime loops; interactive agents are launched in terminal tabs.
- **Lead**: The default interactive role and terminal agent. It is not the only
  possible interactive agent. Lead/orchestrator role names additionally opt
  into epic ownership and assignment delivery; `kind=interactive` alone does
  not grant those capabilities.
- **Worker**: An autonomous role or agent intended to claim and complete tasks
  under daemon supervision.

## Role Kind

`role.kind` is the primary split between interactive and worker behavior:

- `interactive`: A human-in-the-loop terminal/orchestration agent. Opening its
  terminal runs the lead runtime, usually `loom lead`, optionally with
  `--prompt <prompt_file>`. Interactive agents are not daemon-supervised
  plan/task workers; they run when their terminal is opened.
- `worker`: An autonomous task-loop agent launched by `loom agent`, `loom plan`,
  or `loom task`, and supervised by the daemon/runtime for plan/task work.

When `kind` is unset, `domain.ResolveRoleKind` preserves the legacy naming
convention: roles named `lead` or `orchestrator` resolve to `interactive`; other
names resolve to `worker`.

Role kind selects runtime placement, not product authorization. In particular,
epic-runner ownership remains deliberately name-based through
`epicrunner.IsLeadRole` until Loom has an explicit capability/policy field.

## Prompt Selection

`prompt_file` selects the prompt for a role. For interactive roles it is passed
to `loom lead --prompt <prompt_file>` when the terminal opens.

Prompt values can be:

- A workspace or absolute file path, such as `prompts/reviewer.md`.
- A built-in prompt selector, `builtin:<id>`. For example,
  `builtin:pr-review` selects the embedded PR-review terminal-agent prompt.
- Empty for the default built-in lead prompt.

## Other Overloaded Names

- **fleet / fleet-db**: The control-plane data service that stores Loom state.
- **flue**: The external agent harness framework Loom uses to build and run
  TypeScript agents and workflows. **Flue** is reserved for the framework
  itself; the Loom plane it powers is the task plane.
- **task plane**: The Loom execution plane that dispatches work as TaskRuns
  through the driver/bridge to leaf processes, versus the daemon plane's
  supervised agents. It is named by role; sessions and ids in this plane carry
  no framework name.
- **aether**: The Loom UI/design system terminology used by the web UI.
- **codex / claude**: AI backends or agent CLIs used by Loom, depending on
  context.
- **daytona / atlas**: Loom deployment or provider concepts when referenced in
  design docs or runtime configuration.
