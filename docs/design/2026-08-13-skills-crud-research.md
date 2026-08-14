# Skills CRUD research: first-class skills in loomcli + fleet-db

Status: research notes (feat/skills-crud-v1 worktrees; loomcli on v5, fleet-db on main).
Location note: this repo keeps design/research notes in `docs/design/` with
date-prefixed filenames (e.g. `docs/design/2026-06-07-trigger-workflow-proposal.md`,
`docs/design/2026-06-18-stack-aware-pr-publisher.md`), so this file follows that
convention rather than creating a new `docs/research/` directory.

All file paths below are repo-relative to either
`/Users/tyson/codebase/code-agents/loom-aug/.worktrees/skills-crud/fleet-db` or
`/Users/tyson/codebase/code-agents/loom-aug/.worktrees/skills-crud/loomcli`.

---

## 1. Summary of recommended approach

Make **Skill** a first-class, workspace-scoped fleet-db entity that mirrors the
existing Role entity layer-for-layer, and have loomcli **materialize** a role's
skills into the agent's worktree as Claude Code-native
`.claude/skills/<name>/SKILL.md` directories at spawn time.

1. **fleet-db**: add `Skill` following the Role stack exactly —
   `internal/models/skill.go` (name/description/content + validation mirroring
   the Agent Skills spec limits), Redis storage (`internal/storage/skill.go` +
   `marshal.go`), Postgres projection (`internal/storage/postgres/entities.go`
   generic helpers + a new `skills` table migration shaped like the `roles`
   table in `007_first_class_entities.up.sql`), event-sourced service
   (`internal/service/skill_service.go` with `ActionSkillCreate/Update/Delete`
   + projector handlers), HTTP handlers (`internal/api/skills.go`, routes
   `POST/GET/PATCH/DELETE /api/v1/{workspace}/skills[/{name}]`), `PermSkill*`
   permissions, and `api/openapi.yaml` schemas.
2. **Content model**: v1 stores at minimum `name` (Agent Skills constraints:
   lowercase/digits/hyphens, ≤64 chars), `description` (≤1024 chars, required
   by the spec), and `content` — the SKILL.md markdown body — bounded like
   `models.MaxRolePromptBytes` (100 000 bytes). Multi-file skills (scripts,
   references) are a v2 concern.
3. **Association**: roles already carry `Skills []string` end-to-end (fleet-db
   → wire → loomcli domain → daemon env `LOOM_ROLE_SKILLS`). Reuse it as the
   role→skill binding, but note it is currently used as *routing labels*
   (matched against issue labels in `internal/cli/task_router.go`), so the two
   semantics must be reconciled (open question #1).
4. **Loading into agents**: agents run in git worktrees
   (`internal/cli/workspace/init_helpers.go`) and the Claude backend is invoked
   with the worktree as `workDir` (`internal/cli/backends/backend_claude.go`).
   Claude Code discovers project skills automatically from
   `.claude/skills/` in cwd and every parent up to the repo root
   (code.claude.com skills doc), so writing
   `<worktree>/.claude/skills/<skill>/SKILL.md` before spawn requires **no new
   CLI flags** — the harness picks them up natively. The daemon supervisor
   (spawn path in `internal/cli/daemon/supervisor/spawn.go`) is the right
   place to resolve `RoleConfig.Skills` names against the skill store and
   write the files.
5. **loomcli client + CLI**: follow the Role client stack — `internal/domain/skill.go`,
   `internal/store/skill_store.go` (`SkillStore` interface + `Skills()` accessor on
   `store.Store`), `internal/infra/fleetdb/skill.go`, `internal/infra/memstore/skill.go`,
   traced wrapper in `internal/cli/cmdstore/`, and a `loom skill add|list|show|update|remove`
   noun-verb command modeled on `internal/cli/role/role_cmd.go`.
6. **Delivery**: stack it the way delivery-policy was stacked — fleet-db
   contract branch first, then loomcli client branch, then runtime branch, then
   UI (see §5).

---

## 2. Finding 1 — How agent definitions/roles are stored and loaded today

### fleet-db storage

- **Model/contract**: `internal/models/role.go` defines `Role` — a
  "workspace-scoped agent persona/role definition" with `PromptFile`, inline
  `Prompt` (≤ `MaxRolePromptBytes` = 100 000, line 57), `Model`, `Backend`,
  `Effort`, `AllowedTools`/`DeniedTools`, `InputPolicy`, budget/duration caps,
  and — already present — `Skills []string` ("agent-level capabilities/skills
  the role enables", lines 197–198). Name validation:
  `^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$` (line 13). `Validate()` at line 269.
- **Redis storage**: `internal/storage/role.go` — `CreateRole` (Lua
  named-entity script, line 60), `GetRole`/`ListRoles` (HGETALL over
  `RoleKey(ws,name)` + `RolesMetaKey(ws)` set), `UpdateRole` with per-field
  `RoleUpdate` pointers incl. `Skills *[]string` (line 34) written as a JSON
  hash field `"skills"` (line 229), `DeleteRole` which **refuses deletion while
  an agent service references the role** (`ErrInvalidTransition`, lines
  331–335). Flat-map marshalling in `internal/storage/marshal.go`
  (`marshalRole` line 647, `unmarshalRole` line 729).
- **Postgres projection**: `internal/storage/postgres/entities.go` — generic
  `insertEntity/upsertEntity/getEntity/listEntities/deleteEntity` (lines
  336–398) over JSONB `data` blobs; role methods at lines 65–176. Table from
  migration `internal/storage/postgres/migrate/migrations/postgres/007_first_class_entities.up.sql`:
  `roles (workspace TEXT REFERENCES workspaces(key) ON DELETE CASCADE, name TEXT, data JSONB, created_at, updated_at, PRIMARY KEY (workspace, name))`
  plus `idx_roles_updated` (lines 12–21). Migrations are numbered SQL files;
  latest on main is `044_issue_design_format.up.sql`.
- **Service (event-sourced)**: `internal/service/role_service.go` —
  `RoleService` validates, appends a `models.Event`
  (`ActionRoleCreate/Update/Delete`, `EntityRole`) to the event store, and
  projects inline with background catch-up (`executeRoleCommand`, lines
  75–90). Projection handlers live in `internal/projection/pg_handlers.go`
  (`handleRoleCreate/Update/Delete`, lines 50–52, 547+).
- **API**: `internal/api/roles.go` — `CreateRoleRequest` (with
  `Skills []string`, line 43) and `UpdateRoleRequest` (`Skills *[]string`,
  line 69); routes registered in `RegisterRoleRoutes` (lines 104–108):
  `POST/GET /api/v1/{workspace}/roles`, `GET/PATCH/DELETE /api/v1/{workspace}/roles/{name}`,
  each wrapped in a permission (`auth.PermRoleCreate` … `PermRoleDelete`,
  defined in `internal/auth/permissions.go` lines 201–205). Wired in
  `cmd/fleet-db/main.go` line 635 (`api.RegisterRoleRoutes(...)`). OpenAPI
  schema `Role` at `api/openapi.yaml` line 6649+ with paths at lines 6173 and
  6210; `skills` appears at lines 10680/10751/10823.

### loomcli client + runtime

- **Domain type**: `internal/domain/role.go` — `Role` mirrors fleet-db field
  for field, incl. `Skills []string` (line 186); doc comment notes built-in
  `plan`/`task` roles are auto-seeded on workspace creation (lines 165–168).
- **Store interface**: `internal/store/role_store.go` — `RoleStore`
  (Create/Get/List/Update/Delete) with `RoleCreate.Skills []string` (line 24)
  and `RoleUpdate.Skills *[]string` (line 47); exposed via `store.Store.Roles()`
  (`internal/store/store.go` line 51).
- **Backends**: `internal/infra/fleetdb/role.go` (HTTP wire type `roleWire`
  with `skills` JSON tag, line 34) and `internal/infra/memstore/role.go`
  (local mode). CLI commands go through the traced store wrapper
  `internal/cli/cmdstore/store_tracing_core_entities.go` (lines 160–199).
- **CLI CRUD**: `internal/cli/role/role_cmd.go` — noun-verb
  `loom role add|list|show|set|remove` with `--skills` flag (line 132), field
  editing via `loom role set <name> skills=a,b` (lines 397–398).
  Separately, `internal/cli/agentdef/agentdef_cmd.go` is the CRUD surface for
  *agent assignments* (the Agent entity: name + role + repos + backend), "distinct
  from `loom agent <worktree>` which runs an actual agent process" (package
  comment, lines 1–5).
- **Daemon load path**: fleet-db `Role` → `domain.Role` →
  `config.RoleConfig` via `roleConfigFromDomain`
  (`internal/cli/config/project.go` lines 309–334; also
  `internal/cli/serve/daemonwire/daemon.go` lines 308–333 — both copy
  `Skills`). The supervisor resolves the effective config in
  `internal/cli/daemon/supervisor/role.go` (`ResolveRoleConfigStatic`;
  built-in roles merge user overlays via `MergeRoleConfig`, which overlays
  `Skills`, lines 87–89; custom roles **require** a `prompt_file` that must
  exist on disk, lines 38–48).
- **Spawn**: `internal/cli/daemon/supervisor/spawn.go` — built-in roles run
  `loom <role> <worktree> --auto --daemon-mode`; custom roles run
  `loom agent <worktree> --prompt <file> --auto --daemon-mode` (lines
  92–123). Role config travels to the child **as environment variables**:
  `LOOM_ALLOWED_TOOLS`, `LOOM_DENIED_TOOLS`, `LOOM_READ_ONLY`,
  `LOOM_ROLE_INPUT_POLICY` (JSON), `LOOM_MAX_BUDGET_USD`,
  `LOOM_ROLE_EXECUTOR`, `LOOM_AGENT_EFFORT`, `LOOM_AGENT_MODEL`
  (`appendRoleEnv`, lines 126–173) and `LOOM_ROLE_SKILLS` (comma-joined),
  `LOOM_ROLE_PATH_PATTERNS`, `LOOM_ROLE_MAX_PRIORITY`,
  `LOOM_ROLE_TASK_FILTER`, `LOOM_ROLE` (`appendRoutingEnv`, lines 175–194).
- **Prompt assembly**: worker prompts are Go templates rendered in
  `internal/cli/agent/prompts.go` (`GeneratePlanningPrompt` line 273,
  `GenerateTaskPrompt` line 304, `GenerateTerminalPrompt` line 388 for prompt
  files); the harness is then invoked one-shot per turn with
  `--dangerously-skip-permissions`, `--effort`, `--model`,
  `--max-budget-usd`, `--resume` (`internal/cli/backends/backend_claude.go`,
  `buildClaudeRunTurnArgs` lines 274–296). The harness `workDir` is the
  agent's worktree; worktrees are created with `git worktree add`
  (`internal/cli/workspace/init_helpers.go` line 279).

**Key gap**: nothing today materializes any files into the agent worktree for
the harness to discover — role knowledge reaches the agent only via the
rendered prompt and env vars. `grep` found no writes of `.claude/` or skills
directories anywhere under `internal/workspace`, `internal/localworkspace`, or
the supervisor.

---

## 3. Finding 2 — Existing skill/prompt/capability concepts to build on

- **`Role.Skills []string` exists end-to-end but means "routing labels".**
  The daemon exports `LOOM_ROLE_SKILLS` (spawn.go line 177–179) and the worker
  reads it back (`internal/cli/task_router.go` lines 193–194) to *score issues*:
  `countSkillMatches` counts role skills that appear in the **issue's labels**
  (lines 281–295), +50 per match, fallback score 10 when a role declares
  skills but none match (lines 105–113). So today "skills" are task-routing
  affinity tags, not capability documents. Any skills-CRUD design must either
  keep this behavior and layer content resolution on the same names, or split
  the fields.
- **Role prompt content**: the closest thing to "capability content loaded
  into an agent" is `Role.Prompt` (inline, ≤100 KB) / `Role.PromptFile`
  (fleet-db `internal/models/role.go` lines 173–177; loomcli requires the file
  to exist locally, `supervisor/role.go` lines 38–48). fleet-db ships example
  role prompt files in `prompts/` (`fleet-agent.md`, `fleet-plan.md`,
  `fleet-task.md`, …), referenced by role `prompt_file`.
- **Both repos already dogfood the SKILL.md format for their own dev
  tooling**: loomcli has `.agent-skills/loom-pr-test/SKILL.md` (referenced
  from `CLAUDE.md`) and `.claude/skills/agent-browser/SKILL.md` +
  `.claude/skills/dogfood/` — all with standard `name`/`description` (and
  Claude Code's `allowed-tools`) frontmatter. These are development-time
  skills for humans/agents working *on* the repos, not runtime artifacts, but
  they establish the format in-house.
- **Input policy as a precedent for structured role config transport**:
  `domain.EncodeRoleInputPolicy`/`DecodeRoleInputPolicy`
  (`internal/domain/role.go` lines 217–249) show the established pattern for
  moving structured role data over the env boundary with fail-closed
  semantics — relevant if skill references (not content) are passed to the
  child process for it to materialize.
- **No existing `capability`, `plugin`, or `prompt template` entity** exists
  in fleet-db beyond the above (grep over both repos; fleet-db's only "skill"
  hits are the Role field, and `pkg/client` has no role/skill support at all —
  loomcli's `internal/infra/fleetdb` is the real client).

---

## 4. Finding 3 — Claude Code's native skills format (the load target)

Sources (official, fetched 2026-08-13):
- Claude Code skills guide: https://code.claude.com/docs/en/skills
- Agent Skills overview: https://platform.claude.com/docs/en/agents-and-tools/agent-skills/overview
  (redirect target of docs.claude.com/en/docs/agents-and-tools/agent-skills/overview)
- Agent SDK skills: https://code.claude.com/docs/en/agent-sdk/skills

Key facts:

- **Format**: every skill is a directory with a `SKILL.md` file: YAML
  frontmatter between `---` markers + markdown body. Platform-level required
  fields: `name` and `description`. Constraints (platform overview, "Skill
  structure"): `name` ≤64 chars, only lowercase letters/numbers/hyphens, no
  XML tags, no reserved words "anthropic"/"claude"; `description` non-empty,
  ≤1024 chars, no XML tags, and should say *what it does and when to use it*.
- **Discovery locations** (code.claude.com/docs/en/skills, "Where skills
  live"): personal `~/.claude/skills/<skill-name>/SKILL.md`, project
  `.claude/skills/<skill-name>/SKILL.md`, plugin
  `<plugin>/skills/<skill-name>/SKILL.md` (namespaced `plugin:skill`).
  **Project skills load from `.claude/skills/` in the starting directory and
  every parent up to the repository root**; nested `.claude/skills/` below
  cwd load lazily when Claude touches files there. Skill directories are
  **watched live** — adding/editing a skill under a watched location is
  picked up within the current session without restart. A `<skill-name>`
  entry may be a **symlink** to a directory elsewhere on disk. `--add-dir`
  also loads `.claude/skills/` inside added directories (an explicit
  exception to add-dir being access-only).
- **Progressive disclosure** (platform overview): Level 1 = frontmatter
  metadata, always in the system prompt (~100 tokens/skill); Level 2 =
  SKILL.md body, read only when triggered; Level 3 = bundled files
  (references, scripts) read/executed on demand. Claude Code follows the
  Agent Skills open standard (https://agentskills.io) and extends it; the
  spec's portable frontmatter fields are `name`, `description`, `license`,
  `compatibility`, `metadata`, `allowed-tools` — anything else fails claude.ai
  / Skills API packaging with a hard error. Claude Code adds (accepts) many
  more: `when_to_use`, `disable-model-invocation`, `user-invocable`,
  `allowed-tools`/`disallowed-tools`, `model`, `effort`, `context: fork`,
  `agent`, `hooks`, `paths`, etc. (frontmatter reference table).
- **Agent SDK** (code.claude.com/docs/en/agent-sdk/skills): skills are
  filesystem-only — "The SDK does not provide a programmatic API for
  registering Skills." They load from the locations governed by
  `settingSources`/`setting_sources` (defaults include user+project), from
  `~/.claude/skills/`, `<cwd>/.claude/skills/`, and parents of cwd up to the
  repo root; the `skills` option (`"all"` | list | `[]`) filters which are
  enabled, and the init system message lists loaded skills.
- **Implication for loom**: "loading skills into an agent" natively means
  **putting a well-formed skill directory on the filesystem where the
  harness's cwd (the agent worktree) can see it** — either
  `<worktree>/.claude/skills/<name>/` (untracked write, simplest, picked up
  automatically and even live-watched) or a shared per-agent dir passed via
  `--add-dir`. No system-prompt surgery, settings edits, or new harness flags
  are needed for the Claude backend. Note `allowed-tools` frontmatter is
  honored by Claude Code CLI but *not* via the Agent SDK.

---

## 5. Finding 4 — The contract/repo layering pattern to copy

The most recent worked example is the **task delivery-policy stack** (loomcli
PRs #357–#365 on v5 with fleet-db companion branches, both present locally in
these clones).

### fleet-db side

- Branch `feat/task-delivery-policy-contract`, single commit `8fa16cd`
  ("feat: add workspace task delivery requirement") touches exactly the layer
  stack a new entity/field must touch:
  `internal/models/task_delivery.go` (+ tests) and `internal/models/workspace.go`
  (contract type + validation), `internal/storage/workspace.go` +
  `internal/storage/marshal.go` + `internal/storage/filter.go` (Redis),
  `internal/storage/postgres/workspace.go` + `postgres/scan.go` + migration
  `internal/storage/postgres/migrate/migrations/postgres/045_workspace_task_delivery_requirement.up.sql`,
  `internal/projection/pg_handlers.go` (projection),
  `internal/service/workspace_service.go` + `service/errors.go`,
  `internal/api/workspace.go` + `api/errors.go` (handlers),
  `api/openapi.yaml`, and docs (`docs/architecture.md`, `docs/glossary.md`).
- Branch `feat/task-delivery-policy-repo`, commit `dcd1b02` ("feat: add
  repository delivery requirement override") repeats the same slice for the
  Repo entity: `internal/models/repo.go`, `internal/storage/repo.go`,
  `internal/storage/postgres/entities.go`, `internal/service/repo_service.go`,
  `internal/api/repos.go`, `internal/projection/pg_handlers.go`,
  `api/openapi.yaml`.
- For a brand-new entity (rather than a field), the fuller Role template
  applies (§2): model file → Redis store + marshal → Postgres via the generic
  entity helpers + numbered migration → event actions + projector handlers →
  service → api handlers + `RegisterXRoutes` in `cmd/fleet-db/main.go` →
  `auth.PermX*` permissions → `api/openapi.yaml`.

### loomcli side

- Branch `feat/task-delivery-policy-client` (diff vs v5) shows the client
  slice: `internal/domain/task_delivery.go` (+ `domain/repo.go`,
  `domain/workspace.go`), `internal/store/repo_store.go` +
  `store/workspace_store.go` (interfaces), `internal/infra/fleetdb/repo.go` +
  `infra/fleetdb/workspace.go` (wire), `internal/infra/memstore/*` (local
  mode), plus a pure-logic package `internal/taskdelivery/plan.go`.
- Follow-up branches `feat/task-delivery-policy-runtime` and
  `feat/task-delivery-policy-ui` layer the daemon behavior and web UI on top —
  the same client → runtime → UI stacking a skills feature should use.
- Per the project memory notes: the loomcli stack (#357–#365) targets trunk
  `v5` (not main) and the fleet-db companion branches must merge first.

### Suggested skills stack (concrete)

1. fleet-db `feat/skills-crud-contract`: `models/skill.go`
   (name/description/content, validation mirroring §4 limits),
   `storage/skill.go` + marshal, `storage/postgres` + migration
   `0NN_skills.up.sql` cloned from the `roles` DDL in
   `007_first_class_entities.up.sql`, `service/skill_service.go` +
   event actions + `projection/pg_handlers.go` entries, `api/skills.go` +
   `PermSkill*` + `main.go` registration + `openapi.yaml`.
2. loomcli `feat/skills-crud-client`: `domain/skill.go`,
   `store/skill_store.go` + `Store.Skills()`, `infra/fleetdb/skill.go`,
   `infra/memstore/skill.go`, `cmdstore` tracing, `cli/skill/skill_cmd.go`
   (clone of `role_cmd.go`).
3. loomcli `feat/skills-crud-runtime`: supervisor resolves
   `RoleConfig.Skills` → skill store → writes
   `<worktree>/.claude/skills/<name>/SKILL.md` before `buildCommand`
   (`internal/cli/daemon/supervisor/spawn.go`), with generated frontmatter
   `name:` + `description:` and the stored body; same materialization for
   interactive/terminal launches.
4. Optional UI branch for the web UI role/skill editors.

---

## 6. Open questions

1. **Semantics of `Role.Skills`**: today it is a routing signal
   (`task_router.go` matches skills against issue labels and scores +50 per
   match). If the same names become references to skill documents, a role
   adding a skill for capability reasons silently changes its task routing.
   Options: (a) accept the coupling and document it, (b) add a separate
   `Role.SkillRefs`/`skills_loaded` field, (c) resolve names against the skill
   store and keep only unresolved names as routing labels. Needs a product
   decision before the contract branch.
2. **Migration numbering**: fleet-db main is at `044_*`; the un-merged
   delivery-policy contract branch already claims
   `045_workspace_task_delivery_requirement.up.sql`. If delivery-policy merges
   first (per plan), skills takes `046_skills.up.sql`; landing skills first
   forces a renumber on the other stack.
3. **Single-file vs bundled skills**: v1 = one SKILL.md body per skill
   (bounded like `MaxRolePromptBytes`). The native format supports Level-3
   bundled files (scripts, references); storing those needs either a
   files-map on the entity or an artifact/blob story — defer, but leave the
   content field named so `files` can be added.
4. **Worktree hygiene**: materialized `.claude/skills/` inside a git worktree
   is untracked and an agent could commit it. Mitigations: write the paths
   into `.git/info/exclude` for the worktree, or materialize to a sidecar
   directory outside the repo and launch with `--add-dir` (documented to load
   its `.claude/skills/`). Symlinking `<worktree>/.claude/skills/<name>` →
   shared cache is also officially supported.
5. **Non-Claude backends**: codex/opencode workers don't read
   `.claude/skills/`. Is skill loading claude-backend-only in v1, or should
   the runtime also render skills into the prompt/AGENTS.md for other
   backends (`Role.Backend` in `models/role.go` line 188)?
6. **Delete/referential integrity**: `DeleteRole` refuses while agent
   services reference the role (`storage/role.go` lines 331–335). Should
   `DeleteSkill` similarly refuse while any role's `Skills` references it, or
   should missing skills degrade to plain routing labels at spawn?
7. **Name validation strictness**: fleet-db role names allow dots/underscores
   (`roleNamePattern`), but the Agent Skills spec allows only
   lowercase/digits/hyphens and reserves "anthropic"/"claude". Skill name
   validation should adopt the stricter spec rules so every stored skill is
   materializable verbatim.
8. **Interactive lead sessions**: terminal/web-UI agents (`loom lead`,
   webui terminal sessions) take a different launch path than the daemon
   supervisor; the materialization step needs a shared helper both paths call
   so interactive agents get the same skills as workers.
9. **Seeding**: loomcli's domain doc says built-in `plan`/`task` roles are
   auto-seeded on workspace creation (`internal/domain/role.go` lines
   165–168). Should workspaces seed any built-in skills (e.g. a loom-workflow
   skill), or start empty?
