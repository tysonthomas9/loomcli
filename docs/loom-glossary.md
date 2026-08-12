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

## Repository and Source Control

- **Repository Reference**: The stable, Workspace-scoped identity of a
  registered repository. Filesystem paths and remote URLs are locations that
  may change; they are not repository identity.
- **Source Control**: The capability that owns repository working state,
  working-tree file browsing and mutation, checkout materialization, branch
  operations, diffs, stack lineage and publication, and pull-request
  operations. It is one capability presented through cohesive Browse, Mutate,
  and Checkout ports. It receives Repository References from Workspace and
  does not own the Workspace catalog or connector credentials.
- **Read Projection**: An immutable query view that may combine information
  from multiple capabilities. It owns no product state and cannot mutate a
  participating capability's records.
- **Repository Admission**: The recoverable application workflow that adds one
  or more repositories while creating a Workspace or afterward. It coordinates
  Workspace catalog identity with Source Control materialization. FleetDB is
  authoritative for process status and fencing; a machine-local journal may
  retain only materialization and cleanup facts needed for crash recovery.
- **Transcript Evidence**: The canonical, authorized Read Projection of an
  Execution or Interaction transcript within a Run Capture. The lifecycle
  owner records the durable Artifact reference; Artifacts owns evidence policy
  and the durable content, while private platform adapters implement backend-
  specific parsing and mechanical redaction. Live runtime output is ephemeral
  observation rather than durable evidence.
- **Run Capture**: The immutable evidence associated with one Execution run or
  Interaction session, including its prompt, transcript, diff, logs, and
  reports. The lifecycle owner owns the run or session, Artifacts owns the
  evidence content, and the Run Capture owns no product state.
- **Run Capture Archive**: The queryable collection of Run Captures. It is a
  Read Projection rather than a separate lifecycle or storage authority.
- **Workflow Distribution**: The packaging module that locates workflow source,
  validates its layout, builds, stages, promotes, and verifies an immutable
  content-addressed bundle, and reports digest, trust, and source provenance.
  Workflow Authoring requests this work; Workflow Catalog owns the resulting
  durable version and its bundle-availability lifecycle.
- **Workflow Bundle Availability**: The Workflow Catalog invariant that records
  whether a pending immutable version's digest-addressed content is executable.
  Only an `available` version may be approved, activated, or dispatched;
  missing, drifted, or terminally invalid content fails closed.

## Other Overloaded Names

- **fleet / fleet-db**: The control-plane data service that stores Loom state.
- **flue**: The external agent harness framework Loom uses to build and run
  TypeScript agents and workflows.
- **aether**: The Loom UI/design system terminology used by the web UI.
- **codex / claude**: AI backends or agent CLIs used by Loom, depending on
  context.
- **daytona / atlas**: Loom deployment or provider concepts when referenced in
  design docs or runtime configuration.
