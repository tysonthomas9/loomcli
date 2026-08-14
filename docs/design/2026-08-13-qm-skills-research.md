# How qm (yc-software/qm) manages skills

Status: research notes, primary-source reading of https://github.com/yc-software/qm
(cloned at commit `45404b5`, 2026-08-13). All file paths below are repo-relative to
that clone; line numbers are from the same commit.

Companion to `docs/design/2026-08-13-skills-crud-research.md` (the loomcli + fleet-db
skills CRUD research). §10 compares the two approaches.

---

## 1. What qm is

qm is a "multiplayer agent harness for work" — a TypeScript/Node core (Fastify HTTP,
Postgres persistence, Lit web UI, Slack plugin) that runs one shared agent for a whole
company, with per-person and per-room isolated scopes: memory, files, crons, keychain,
and a durable sandbox per scope (`README.md:1-78`). The harness layer is pluggable —
Pi, OpenCode, Codex, and Claude Code all drive the same core (`README.md:17-19`).
Skills are a first-class, advertised feature: "Skills are scope-owned and shareable by
grant, with admin-gated promotion to the whole org and skill packs imported from git
repositories" (`README.md:30-31`).

The entire skills subsystem lives in `src/skills/` (13 files, ~2 500 lines including
the API layer), plus `src/api/app-skills.ts` (application methods),
`src/api/routes/surface.ts` (user/agent HTTP routes), `src/api/routes/skill-packs.ts`
(admin pack routes), and materialization hooks in `src/core/orchestrator*` and
`src/tools/primitives.ts`. There are ~16 dedicated test files (`test/skill*.test.ts`,
`test/skills-*.test.ts`).

---

## 2. Data model

### 2.1 The Skill record (DB-native, not file-native)

A skill is a database record, not a file. The canonical shape
(`src/skills/skill-store.ts:9-63`):

```ts
interface SkillManifest {
  name: string;                     // validated, see §2.3
  description: string;
  requiredCapabilities: string[];   // e.g. "egress:api.github.com"
  body: string;                     // the SKILL.md markdown body (no frontmatter)
  files?: SkillFile[];              // supporting assets: {path, content, executable?}
}

interface Skill {
  id: string;                       // uuid
  scopeId: ScopeId;                 // "personal:U1" | "channel:C1" | "group:G1" | "team:T1" | "org:acme"
  manifest: SkillManifest;
  signature: string;                // HMAC-SHA256 over canonicalized manifest
  status: "draft" | "reviewed" | "published" | "archived";
  createdBy: string;                // principal id, or "pack:<id>" / "system:skills-seed" / …
  version: number;                  // bumped on every update
  grantedCapabilities: string[];    // granted at review time
  approvals: string[];              // reviewer ids
  createdAt?: number; updatedAt?: number; lastUsedAt?: number;
  pack?: { packId: string; commit: string; upstreamName: string };  // provenance if imported
}
```

Key points:

- **Frontmatter is an ingestion format only.** `SKILL.md` files (seed dirs, git
  packs, deployment layers) carry `name` / `description` / `requiredCapabilities`
  frontmatter, parsed by a small hand-rolled YAML-subset parser
  (`src/skills/frontmatter.ts:30-105`). Once ingested, only the *body* is stored in
  `manifest.body`; the materialized `SKILL.md` in the sandbox is the bare body with no
  frontmatter (`src/skills/materialize.ts:80-86, 254-258`). Discovery metadata
  (name/description) lives in the DB record and is injected via the system prompt
  index instead (§6.2).
- **Multi-file skills from day one, text-only v1.** `manifest.files` carries
  supporting assets with an executable bit; binary assets are detected
  (`isProbablyBinary`, `src/skills/seed.ts:14-17`) and skipped with a warning
  ("v1 stores text only", `src/skills/seed.ts:55`). A pack skill containing any
  binary asset is excluded wholesale with reason `binary-asset`
  (`src/skills/ingest.ts:197`).
- **Signature**: every create/update computes an HMAC-SHA256 over the canonicalized
  manifest (sorted capabilities, sorted files) with a server secret
  (`src/skills/skill-store.ts:118-128`). `review` and `promote` refuse if the stored
  signature no longer matches — tamper-evidence between write and review
  (`skill-store.ts:176, 246`).

### 2.2 Storage

Stores are interface-backed `DurableMap<T>`s: Postgres-backed maps when
`DATABASE_URL` is set, in-memory otherwise
(`src/wiring.ts:399-401`, `src/persistence/durable-map.ts:226`). Three tables/maps:

- `skills` → `Skill` records (`src/wiring.ts:445-448`)
- `skill_packs` → registered git packs (`src/wiring.ts:449`)
- `skill_bundles` → per-pack shared file bundles (`src/wiring.ts:450`)

### 2.3 Validation

- **Name**: `^[A-Za-z0-9](?:[A-Za-z0-9_.-]{0,126}[A-Za-z0-9_-])?$` — 1–128 ASCII
  letters/digits/dots/underscores/hyphens, must start alphanumeric, can't end in a
  dot (`src/skills/skill-name.ts:1-14`). Asserted at create, update, review,
  publish, promote, move, restore — and again on the *read* side before resolving
  or materializing (defense in depth; `skill-store.ts`, `materialize.ts:40-42`).
- **File paths**: `safeSkillFilePath` normalizes separators and rejects absolute
  paths, `.`/`..` segments, and NULs (`src/skills/skill-store.ts:23-37`). Applied
  at ingest *and* at materialization *and* when re-reading marker files from the
  sandbox (`materialize.ts:128-149`), so a tampered marker can't direct deletions
  outside the skills dir.
- **Rename is forbidden**: `update` throws "skill update cannot rename — create a
  new skill instead" (`skill-store.ts:155`). Name is the stable identity within a
  scope; `id` (uuid) is the global identity.
- **HTTP layer** re-validates non-empty name/description/body and typing
  (`src/api/routes/surface.ts:884-896`).

---

## 3. Lifecycle: draft → reviewed → published → archived

The store implements a review pipeline (`src/skills/skill-store.ts`):

- `create` → status `draft`, version 1.
- `review(id, reviewer, grantCapabilities)` → verifies signature, unions granted
  capabilities, records approver, status `reviewed` (`skill-store.ts:172-183`).
- `publish(id)` → refuses drafts, refuses if any `requiredCapabilities` is not in
  `grantedCapabilities` (`skill-store.ts:185-196`). Only **published** skills ever
  resolve or materialize (`skill-store.ts:97-105`).
- `archive` / `restore` / `delete`: user-facing "delete" is actually archive
  (`deleteOwnedSkill` calls `skills.archive`, `src/api/app-skills.ts:398-412`);
  hard `delete` is reserved for pack removal (`app-skills.ts:355-364`). Restore
  re-reviews and re-publishes the preserved version (`app-skills.ts:263-276`).
- `update` in a **non-personal** scope knocks the skill back to `draft`
  (`skill-store.ts:158`); the app layer then immediately re-reviews/republishes
  shared-scope (channel/group) edits under a synthetic reviewer
  (`republishIfShared`, `src/api/app-helpers.ts:421-435`). Net effect today: the
  review state machine exists and is enforced, but for self-serve paths review is
  auto-granted by `system:*` reviewers (`app-skills.ts:387`, `seed.ts:109-115`) —
  the machinery is a hook for stricter policy, not a human gate yet.
- `recordUse(id)` stamps `lastUsedAt` whenever a skill's tree is actually
  materialized for an agent turn (`src/core/orchestrator/sandboxes.ts:341-342`) —
  cheap usage analytics for pruning stale skills.

`requiredCapabilities` are currently a publish-gate + surfaced metadata (admin
artifact view, `src/api/routes/admin/artifacts.ts:146-147`); they are not
separately enforced at exec time (egress control is a separate subsystem). The
`egress:` convention comes from pack normalization (§7.2).

---

## 4. Scoping, sharing, shadowing

### 4.1 Scope model

`ScopeId` is `"<kind>:<ref>"` with kinds `personal | channel | team | org | group`
(`src/types.ts:12-33`). Skills are **homed** in exactly one scope:

- Direct creation is allowed only in `personal`, `channel`, or `group` scopes —
  "a skill cannot be created directly in an org or team scope — promote a published
  skill instead" (`src/api/app-skills.ts:370-376`, mirrored at the HTTP layer
  `surface.ts:879-883`).
- **Promotion to org** is the one heavyweight gate: target must be org-kind, the
  actor must be a *live person* (not an autonomous trigger) **and** an org admin;
  promotion copies the manifest into a new/updated org-scope record and audits
  (`src/api/app-sessions.ts:558-575`, `skill-store.ts:241-266`). The agent-facing
  `share` tool routes org-ceding through this same path
  (`src/api/control-service.ts:632`, tool description in
  `src/harness/pi-tools.ts:1930-1958`).
- **Move** re-homes a skill to another non-org scope (ownership transfer);
  moving to org is explicitly rejected ("goes through promote (admin-gated), not
  move", `skill-store.ts:268-279`).
- **Share by grant**: skills participate in the generic artifact ACL/grant system
  (grant refs via `getArtifactHome` for type "skill",
  `src/api/app-sessions.ts:587-591`) alongside files, crons, and deployments.

### 4.2 Resolution and shadowing

Visibility is computed per principal/turn as an *ordered* scope list — personal
first, then shared rooms the viewer can access, then teams, then org
(`listVisibleSkills`, `src/api/app-skills.ts:220-238`; per-turn variant
`visibleSkillScopes`, `src/core/orchestrator/turn-helpers.ts:94-100`). Resolution
walks that order per name: the narrowest published skill wins and broader-scope
same-name skills are returned as `shadowed` (`skill-store.ts:107-112, 221-239`).
The system-prompt index annotates shadowing: "(shadows a broader-scope skill of the
same name)" (`materialize.ts:414`). So a person can override an org skill with a
personal one without touching the org record.

### 4.3 Live-actor (anti prompt-injection) gating

Creating/editing/deleting a skill in a *shared* scope from an automation-triggered
turn is refused: `triggerBlocksSharedSkill(homeScope, liveActor)`
(`src/api/artifact-share.ts:6-11`) and `sharedSkillCreateBlock`
(`src/api/routes/surface.ts:43-48`) return 403 with a canned refusal unless the
capability token proves a live person is driving. Promotion additionally demands a
live org admin (`app-sessions.ts:560-565`). This protects the "skills auto-load
into everyone's turns" property from being a persistence vector for injected
instructions.

---

## 5. CRUD surfaces

### 5.1 HTTP API (users, the web UI, and the agent itself)

Routes in `src/api/routes/surface.ts` (registered per `user-scoped-routes.ts:35-40`):

- `GET  /v1/skills?principalId=` — resolved visible list + archived-but-manageable,
  with `scope`, `shadowed`, `status`, `version`, `source: "pack"|"native"`,
  `assetCount`, `editable` per row (`surface.ts:697-734`)
- `POST /v1/skills` — create `{name, description, body}`; homes in the capability
  token's scope (a DM ⇒ personal, a room ⇒ that room) or an explicitly managed
  scope; auto review+publish; 409 if the name exists in that scope
  (`surface.ts:851-921`, `app-skills.ts:365-397`)
- `GET/PUT/DELETE /v1/skills/:id`, `POST /v1/skills/:id/restore` — detail (body +
  file list + capabilities), patch of description/body only, archive, restore
  (`surface.ts:736-849`)

Every mutation writes an audit-log record (`skill_create`, `skill_update`,
`skill_archive`, `skill_restore`, `skill_promote`; e.g. `app-skills.ts:253-260`).

**The agent is a first-class CRUD client.** The system prompt tells the agent:
"When you work out a procedure worth repeating, save it as a skill via the self-API
so future turns get it automatically" (`src/resolution/protocols/shared-core.md:22`),
and the self-API catalog documents the same `/v1/skills` routes to the agent with
scope-ownership semantics spelled out (`src/api/agent-api-catalog.ts:422-455`).
Skill authoring is not a special tool; it's the same HTTP surface humans use,
authenticated with the turn's capability token.

### 5.2 Web UI

`plugins/web-ui/src/skills.ts` is a full skills page: list grouped by scope with
search + scope/source/status filters, create dialog with scope picker, edit dialog
(description/body), archive/restore, and an edit-review step
(`skill-edit-review.ts`). `plugins/web-ui/src/skill-registry.ts` backs the admin
pack registry UI.

### 5.3 Admin pack API

`src/api/routes/skill-packs.ts:184-192` — all admin-gated (`authorizeAdmin`):

```
POST   /v1/admin/skill-packs               register a git pack
GET    /v1/admin/skill-packs               list (+importedCount)
GET    /v1/admin/skill-packs/:id/catalog   dry-run ingest plan (eligible/excluded + reasons)
POST   /v1/admin/skill-packs/:id/import    import selected/all into scope(s)
POST   /v1/admin/skill-packs/:id/sync      re-reconcile all scopes that imported from it
PATCH  /v1/admin/skill-packs/:id           change ref/url/trustTier/syncMode/subset/config
DELETE /v1/admin/skill-packs/:id           remove pack + hard-delete its skills + bundle
```

### 5.4 No CLI CRUD

The `qm` CLI is a deployment tool, not a skills CLI. Deployment-owned skills ship
as files in the deployment directory and sync on `qm up` (§7.3).

---

## 6. Runtime loading — the interesting part

qm's pipeline is: **DB → per-turn resolution → system-prompt index → eager
one-file materialization → lazy full-tree materialization on first touch.**

### 6.1 Per-turn resolution

Each turn, the orchestrator waits for `skillsReady` (seeding), computes the scope
order, resolves visible skills, and filters out connector-bound skills whose
provider isn't configured (e.g. the `linear` skill disappears if no Linear
connector) — `src/core/orchestrator.ts:833-840`,
`turn-helpers.ts:102-121` (`CONNECTOR_SKILL_PROVIDERS` map).

### 6.2 System-prompt index

If any skills are visible, a `## Skills` section is appended to the system prompt
(`orchestrator.ts:861`): one line per skill —
`- **<name>** — <description> → read \`skills/<name>/SKILL.md\`` — prefaced by
"To use one, read its SKILL.md and follow it (run its steps with your tools)"
(`materialize.ts:402-422`). Name+description live in the prompt; bodies stay on
disk. This substitutes for Claude-Code-style frontmatter discovery.

### 6.3 Eager index materialization

When the scope's sandbox is provisioned for the turn, the materializer writes
`skills/<name>/SKILL.md` (body only) for every visible skill, plus a marker file
`skills/.index` containing `{version, hash, names}` (`materialize.ts:210-270`;
`SKILLS_DIR = "skills"` at sandbox root, `materialization-paths.ts:1-3`). The
content hash makes re-materialization a cheap no-op when nothing changed; when the
visible set shrinks, stale skill dirs are deleted using the previous marker's
recorded names/paths — never a blind `rm -rf` of unrecognized content
(`materialize.ts:222-249`). Writes go through batched `extractFiles` when the
sandbox backend supports it (`materialize.ts:198-208`).

### 6.4 Lazy tree materialization on first touch

Only `SKILL.md` is written up front. The full tree — supporting `files` and pack
bundles — is laid down the first time the agent actually touches the skill:

- the `read` primitive matches `skills/<dir>/SKILL.md` paths
  (`src/tools/primitives.ts:62-66, 582-583`), and
- the `execute` primitive regex-scans the shell command for `skills/<dir>`
  references (`primitives.ts:69-74, 524-525, 820-821`),

both calling `ensureSkillTree(skillDir)` (`src/core/orchestrator/sandboxes.ts:325-355`),
which re-resolves the *latest* version from the store, loads pack bundles, writes
all asset files plus a per-skill `skills/<name>/.tree` marker
`{version, hash, skillPaths, bundlePaths}`, and stamps `lastUsedAt`. Tree diffs use
the marker to delete only previously-materialized paths (`materialize.ts:272-359`).

### 6.5 Concurrency, durability, provenance

- All materialization is serialized per sandbox through a keyed queue plus an
  optional cross-process Postgres advisory lock (`materialize.ts:361-381`);
  pack imports take a global `skills:materialization` lock so imports and
  materialization don't interleave (`app-skills.ts:91-93`, `skill-collision.ts:6`).
- The `skills/` dir is **excluded from workspace snapshots**
  (`orchestrator.ts:1607-1611`) — the DB is the source of truth; the on-disk tree
  is a disposable projection.
- Marker files (`skills/.index`, `skills/<name>/.tree`) are "control paths" that
  no skill or bundle file may claim (`materialization-paths.ts:5-9`,
  `skill-collision.ts:24-28`) — agents can edit their materialized copies mid-turn,
  but the projection reconciles from the DB next turn.

### 6.6 Harness integration — native skill loading is disabled

The Claude Code harness explicitly passes `skills: []` and `settingSources: []` to
the Agent SDK (`src/harness/claude-harness.ts:439-441`) — qm does **not** use
Claude Code's native `.claude/skills` auto-discovery. Skills land in the neutral
`skills/` directory and are advertised through the qm system prompt, identically
across Pi, OpenCode, and Claude Code. Harness portability is the reason: one
mechanism for all four harnesses.

---

## 7. Where skills come from (five sources, one store)

All sources converge on the same store via the same idempotent upsert
(`upsertSeedSkill`, `src/skills/seed.ts:88-117`): match on
(scope, name, createdBy); skip if unchanged+published; update+re-review+republish
if changed; refuse (outcome `"foreign"`) if a *different* creator already owns
that name in the scope — a seed can never clobber a user's skill.

1. **Built-in seed catalog** — `skills-seed/<name>/SKILL.md` (+ asset files) in the
   repo, ~19 skills (admin, browse, memory, publish, taste-skill, connector skills…).
   Installed into the org scope at boot as `system:skills-seed`
   (`src/wiring.ts:503`, `installSeedSkills` in `seed.ts:119-139`). Frontmatter
   `name`/`description`/`requiredCapabilities` is required and validated
   (`frontmatter.ts:86-105`).
2. **Plugin skill dirs** — each enabled plugin can ship a skills dir
   (`PLUGIN_SKILLS_DIRS`, `src/config.ts:799`), installed as
   `system:plugin-skills` (`wiring.ts:504-511`).
3. **Deployment layer** — an org's deployment repo ships `sandbox/skills/<id>/SKILL.md`;
   `qm up` PUTs descriptors + full text skill trees to `PUT /v1/deployment-layer`,
   which validates, stores in Postgres versioned by canonical SHA-256 content hash,
   audits, and archives removed layer-owned skills (`docs/deploy-directory.md:16-17, 99`;
   store wiring `wiring.ts:468-499`).
4. **User/agent-authored** — the `/v1/skills` surface (§5.1).
5. **Skill packs from git** (§7.2) — imported as `createdBy: "pack:<id>"` with
   `pack: {packId, commit, upstreamName}` provenance.

### 7.2 Skill packs (git import) in detail

- **Pack record** (`src/skills/skill-pack-store.ts:18-34`): git URL + ref,
  `syncMode: "pinned" | "tracked"`, `trustTier: "internal" | "third-party"`,
  optional `PackConfig {skillGlobs, exclude, fieldOverrides}`, target scope,
  name subset, optional `authCredentialSlug` for private repos, `lastImport`
  status/counts, `updateAvailable`.
- **Fetcher** (`src/skills/pack-fetcher.ts`): shallow git fetch to a temp dir with
  hard security posture — https only, credential-free URLs, DNS-resolved and
  private-IP-denied (SSRF), redirects disabled, resolved IPs pinned via
  `http.curloptResolve`, proxy cleared (`pack-fetcher.ts:69-106`); auth headers come
  from a named service credential or the creator's GitHub connector token, host-matched
  to the repo (`pack-fetcher.ts:109+`); file-count/byte caps; binary flagged per file.
- **Normalization of foreign SKILL.md formats** (`src/skills/normalize.ts`): name
  falls back to the directory name; description falls back to first prose line or
  heading (truncated to 200 chars); `egress:` capabilities derived from an `egress`
  frontmatter key; `scope`/`visibility` hints map personal-ish values to
  ineligibility; `private`/`agent_only` flags (or a `THE-AGENT-ONLY` body marker)
  exclude; `declaredCreds` fall back to `$ENV_VAR` references scraped from the body;
  unrecognized frontmatter keys are preserved as `meta`. `fieldOverrides` lets an
  admin map a foreign repo's key names.
- **Ingest plan** (`src/skills/ingest.ts:173-211`): every `SKILL.md` in the repo
  becomes a candidate with `eligible` or an `excludeReason` in
  `scope | private | collision | binary-asset | malformed`; the catalog endpoint
  returns this as a dry run with per-reason counts, so import is a two-step
  choose-then-apply flow.
- **Collision defense** (`src/skills/skill-collision.ts`, `ingest.ts:249-263`):
  imports compute every path the pack would write (skill dirs + shared bundle) and
  refuse with a typed `SkillPackCollisionError` if any path is already claimed by a
  native skill, another pack, or materialization control files. Name collisions
  with native skills also mark candidates ineligible pre-import (`ingest.ts:196`).
- **Shared bundles**: repo files *outside* any skill dir (a plugin's shared
  `references/`, minus README/LICENSE/.github noise) are captured as a per-pack
  bundle (`ingest.ts:98-166`), stored once (`skill-bundle-store.ts`), materialized
  under `skills/.packs/<packId>/…`, and the skill body gets an appended
  "## Pack files" note telling the agent to resolve repo-relative paths against
  that root (`materialize.ts:76-86`).
- **Sync engine** (`src/skills/skill-sync-engine.ts`): a 5-minute sweeper under a
  leader lease; `tracked` packs auto-reconcile when the remote ref moves, `pinned`
  packs only flip an `updateAvailable` flag for the admin UI. Sync re-imports into
  every scope that previously imported from the pack and archives skills whose
  upstream source disappeared (`app-skills.ts:341-354, 58-70`).
- **Removal** hard-deletes the pack's skills (they're upstream-owned, not
  user-authored) and its bundle (`app-skills.ts:355-364`).

---

## 8. qm dogfooding note

The qm repo itself keeps repo-development skills as plain Claude Code / Codex
skills (`.claude/skills/{dev-instance,update-qm,upstream-pr}`,
`.codex/skills/…`) — those are for engineers hacking on qm, entirely separate
from the runtime skills subsystem above.

---

## 9. Summary of the design in one paragraph

qm stores skills as scope-owned, versioned, HMAC-signed records in Postgres with a
draft→reviewed→published→archived lifecycle; five ingestion sources (seed dirs,
plugin dirs, deployment layer, user/agent HTTP CRUD, git skill packs) converge on
one idempotent upsert; per turn, the core resolves the viewer's ordered scopes
(personal shadows room shadows team shadows org), injects a name+description index
into the system prompt, eagerly materializes only each `skills/<name>/SKILL.md`
into the scope's durable sandbox with hash-marker idempotence, and lazily lays down
full asset trees the first time the agent reads or executes anything under a
skill's directory — with the DB as source of truth, snapshots excluding the
projection, advisory locks serializing writers, and live-actor + admin gates
protecting shared and org scopes.

---

## 10. Comparison with our recommended loomcli + fleet-db approach

Baseline: `docs/design/2026-08-13-skills-crud-research.md` recommends a fleet-db
`Skill` entity mirroring Role, and loomcli materializing role-bound skills as
Claude-Code-native `.claude/skills/<name>/SKILL.md` in the worktree at spawn time.

### Same shape, independently arrived at

- DB is the source of truth; `SKILL.md` on disk is a materialized projection.
- name/description/body as the core model; frontmatter as interchange, not storage.
- Name as the stable identity within its owner; rename forbidden (qm) — matches
  our name-keyed `/skills/{name}` routes.
- Materialize-at-start-of-work into the agent's filesystem.

### What qm does differently — worth adopting

1. **Marker-file idempotence + scoped cleanup.** qm writes `skills/.index` and
   per-skill `.tree` markers recording a content hash and the exact paths it
   materialized, so re-materialization is a no-op when unchanged and deletion only
   ever touches recorded paths (`materialize.ts:210-359`). Our spawn-time writer
   should do the same (even a single `.materialized.json` in
   `.claude/skills/`), so repeated spawns in a reused worktree don't churn and
   removed skills get cleaned without risking user files.
2. **Body-size discipline via lazy loading.** qm puts only name+description in
   the prompt and defers even asset files until first touch. We get the prompt
   half for free from Claude Code's native discovery (frontmatter description
   only); the lesson is to keep SKILL.md bodies lean and plan for a `files[]`
   asset model rather than growing mega-bodies. qm shipped multi-file (text-only)
   in v1 — our "v2 concern" might be cheaper than we assumed: it's just
   `{path, content, executable}` with a path-safety gate.
3. **Provenance + idempotent upsert for non-user sources.** qm's
   (scope, name, createdBy) upsert with a `"foreign"` refusal outcome
   (`seed.ts:88-117`) is a clean pattern if we ever seed skills from repos or
   sync from a catalog: system-owned skills update themselves without ever
   clobbering a human's same-named skill.
4. **Audit + `lastUsedAt`.** Cheap to add (stamp on materialization), and it
   answers "which skills are dead weight" later.
5. **Path-safety validation on file paths at write *and* read of any manifest we
   materialize** (`safeSkillFilePath`, plus re-validation of marker contents) —
   we must do this the moment skills carry more than one file, since we write
   into worktrees that also contain user code.
6. **Shadowing semantics for scoped skills.** If fleet-db skills ever get
   per-role/per-workspace layering, qm's "narrowest published wins, broader
   listed as shadowed" resolution with an explicit prompt annotation is a proven
   UX (`skill-store.ts:107-112`, `materialize.ts:414`).

### What qm does differently — probably avoid or defer for us

1. **Bypassing the harness's native skills.** qm disables Claude Code skill
   discovery (`skills: []`) and rebuilds indexing in its own prompt because it
   must serve four harnesses identically. We are Claude-Code-first and should
   keep riding native `.claude/skills` discovery — less machinery, and we get
   Claude's own skill-triggering behavior. Only revisit if loomcli goes
   multi-harness.
2. **The review/signature state machine.** Draft→reviewed→published with HMAC
   tamper-evidence is real code but effectively auto-approved by `system:*`
   reviewers everywhere today — machinery ahead of policy. For our v1, a status
   field is enough; skip signatures and approval arrays until there's a human
   review requirement.
3. **Skill packs (git import) as core.** ~600 lines of fetcher/normalizer/
   collision code plus a sync daemon. Powerful, but our equivalent ("import
   skills from a repo") can start life as a loomcli command that reads a checkout
   and POSTs to fleet-db — no server-side git, no SSRF surface. If we do build
   server-side import later, qm's dry-run catalog endpoint
   (eligible/excluded-with-reason before import) and its private-IP/redirect/
   credential-host pinning checklist are the reference.
4. **Live-actor gating.** qm needs it because its agent can CRUD shared skills
   mid-turn from injected content. Our v1 (skills edited via CLI/UI by humans,
   materialized read-only into worktrees) doesn't have that write path; note it
   as a requirement *if* we ever give loom agents self-service skill authoring —
   which qm shows is a compelling feature ("save it as a skill via the self-API
   so future turns get it automatically", `shared-core.md:22`).
5. **Scope-homed ownership vs role-binding.** qm binds skills to *who can see
   them* (scopes) and computes visibility per principal; our design binds skills
   to *roles* attached at spawn. Ours is simpler and fits fleet-db's model; qm's
   is the richer end-state if skills later need per-person/team ownership and
   org promotion. Don't import the scope machinery now, but keep the skill
   record self-contained (no role FK inside the skill) so a binding layer can
   change independently — qm's records know nothing about who consumes them.

### Top-level takeaway

qm validates our architecture (DB-first skills, materialized SKILL.md projection)
at production depth, and its most transferable ideas are operational, not
structural: hash-marker idempotent materialization with recorded-path cleanup,
path-safety validation everywhere, provenance-aware idempotent upserts, and
usage stamps — plus a warning not to over-build review/pack machinery before the
policy that needs it exists.
