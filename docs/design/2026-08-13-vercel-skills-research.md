# Research: how vercel-labs/skills manages agent skills

Date: 2026-08-13
Source investigated: https://github.com/vercel-labs/skills at commit `c6f69c6` (v1.5.22), cloned locally for reading. All `src/...` citations below are file paths within that repo (browsable at `https://github.com/vercel-labs/skills/blob/main/<path>`).

Companion docs in this directory: `2026-08-13-skills-crud-research.md` (our loomcli/fleet-db research; §10 below compares against it).

---

## 1. What the project is

`vercel-labs/skills` is a **TypeScript CLI** (npm package `skills`, invoked as `npx skills`, with `add-skill` as a second bin alias — `package.json` `bin` field) that acts as a **package manager for agent skills**: it fetches skill directories from git repos/URLs, and materializes them into the on-disk skill folders of ~76 coding agents. It is *not* a registry, a runtime, or a spec — it is the install/update/remove tooling layer of what it calls "the open agent skills ecosystem" (`README.md:3`), leaning on two external pieces:

- **The format spec** lives elsewhere: skills "follow a shared [Agent Skills specification](https://agentskills.io)" (`README.md:494-497`). The CLI validates only the minimal invariants itself (see §2).
- **The directory/leaderboard** is the hosted service **skills.sh** (Vercel-run): search API (`src/find.ts:17`, `.../api/search`), download/blob snapshot API (`src/blob.ts:48`, `.../api/download`), telemetry endpoint (`src/telemetry.ts:1-2`, `https://add-skill.vercel.sh/t` and `/audit`).

There is no daemon and no DB; state is two JSON lockfiles plus the materialized skill directories.

## 2. Skill format / data model

A skill is **a directory containing a `SKILL.md` with YAML frontmatter**; sibling files in the directory are "supporting files" carried along verbatim.

- **Required frontmatter**: `name` and `description`, both strings. A skill missing either, or with non-string values, is skipped with a warning — not an error (`src/skills.ts:97-112`).
- **Optional frontmatter**: a free-form `metadata` map. The only key the CLI interprets is `metadata.internal: true`, which hides the skill from discovery/install unless `INSTALL_INTERNAL_SKILLS=1` or the user names the skill explicitly (`src/skills.ts:114-121`, `README.md:384-397`).
- **Parsing** is a deliberately minimal regex + `yaml` parse; the comment notes they avoid gray-matter's JS-engine `eval` RCE (`src/frontmatter.ts:1-16`).
- **In-memory model**: `Skill { name, description, path, rawContent?, pluginName?, metadata? }` (`src/types.ts:79-88`). No version field, no dependency field, no author field — the data model is intentionally tiny.
- **Other frontmatter fields are pass-through.** The README documents a compatibility matrix for `allowed-tools`, `context: fork`, and hooks across agents (`README.md:499-504`), but `grep` confirms no code in `src/` reads those fields — they are copied verbatim and interpreted (or ignored) by each agent at load time. The single exception is Eve (§7).
- **Name normalization**: install directory names are derived by `sanitizeName()` — lowercase, non-`[a-z0-9._]` runs collapsed to `-`, leading/trailing dots+hyphens stripped, 255-char cap, path-traversal-proof (`src/installer.ts:50-65`). Frontmatter `name`/`description` are also run through `sanitizeMetadata` to strip terminal escape sequences before display (`src/skills.ts:124-125`, `src/sanitize.ts`).
- **Authoring**: `skills init [name]` writes a template SKILL.md (frontmatter + "When to use" + numbered "Instructions") and prints publishing guidance (push to GitHub, or host the raw file) (`src/cli.ts:232-293`).

## 3. Skill discovery inside a source

Given a fetched source tree, `discoverSkills()` (`src/skills.ts:175-320`) resolves the set of skills:

1. If the target dir itself has a `SKILL.md`, that single skill wins (early return unless `--full-depth`).
2. Otherwise it scans **priority containers**: repo root (depth 1), `skills/`, `skills/.curated|.experimental|.system/`, and ~27 known agent project dirs (`.claude/skills/`, `.agents/skills/`, `.opencode/skills/`, … — `src/skills.ts:12-40, 248-255`). Known containers are walked up to **3 levels deep** (`DEFAULT_SKILL_CONTAINER_DEPTH = 3`, `src/constants.ts:6`) to support catalog layouts `skills/<category>/<category>/<name>/SKILL.md`; a shallower SKILL.md shadows anything nested below it (`src/skills.ts:279-298`).
3. **Claude Code plugin-marketplace compat**: if `.claude-plugin/marketplace.json` or `.claude-plugin/plugin.json` exists, skill paths declared there are added as search dirs and the plugin `name` is attached to each skill as `pluginName` for grouped display (`src/plugin-manifest.ts`, `README.md:472-490`).
4. If nothing found (or `--full-depth`): full recursive scan to depth 5, skipping `node_modules/.git/dist/build/__pycache__` (`src/skills.ts:132-154, 306-317`).
5. Skills already *installed into* the project (recorded in `skills-lock.json` and living under an agent dir) are filtered out so a repo's installed skills aren't re-offered as its source skills (`src/skills.ts:208-220`).

Deduplication is by frontmatter `name` (first hit wins).

## 4. Source resolution / distribution channels

There is **no central registry requirement** — distribution is "anything git-reachable or HTTP-reachable". `parseSource()` (`src/source-parser.ts:272-481`) classifies input into `ParsedSource.type ∈ {github, gitlab, git, local, well-known, download}` (`src/types.ts:103-111`):

- **GitHub shorthand** `owner/repo`, `owner/repo/sub/path`, `owner/repo@skill-name` (skill filter), plus `#ref` fragment for branch/tag pinning and `github:`/`gitlab:` prefixes (`src/source-parser.ts:296-314, 433-463`). `GH_HOST` switches shorthand to GitHub Enterprise via the generic git path (`src/source-parser.ts:436-440`).
- **Full GitHub/GitLab URLs** including `/tree/<branch>/<subpath>` deep links; GitLab subgroups supported (`src/source-parser.ts:351-431`).
- **Any git URL** (SSH, `.git` HTTPS) as fallback (`src/source-parser.ts:475-480`). Clones are always `--depth=1`, with credential fallback chain: normal git credentials → `gh repo clone` → SSH (`src/git.ts:178-297`, `README.md:52-70`).
- **Local paths** (`./my-local-skills`).
- **Well-known URIs** (RFC 8615): any other HTTPS URL probes `/.well-known/agent-skills/index.json` (then legacy `/.well-known/skills/index.json`). Two index schemas: legacy v0.1.0 `{name, description, files[]}` and v0.2.0 `{$schema: https://schemas.agentskills.io/discovery/0.2.0/schema.json, skills: [{name, type: 'skill-md'|'archive', url, digest}]}` with content digests (`src/providers/wellknown.ts:8-107`). This is how docs sites (Mintlify etc.) publish skills without a git repo. A small pluggable `HostProvider` registry (match/fetch/toRawUrl/sourceIdentifier) backs this, with huggingface and mintlify providers (`src/providers/types.ts:38-75`, `src/providers/registry.ts`).
- **Direct download URLs**: raw SKILL.md or `.zip/.tar/.tar.gz` archives, size-capped (10 MiB download / 25 MiB extracted / 1000 files, env-overridable) (`README.md:111-115`, `src/source-parser.ts:243-270`).
- **Blob fast path**: for allowed GitHub repos, installs skip cloning entirely — GitHub Trees API discovers SKILL.md paths, raw.githubusercontent.com fetches frontmatter, and `skills.sh/api/download` serves pre-built full-file snapshots (`src/blob.ts:1-11`). Zapier self-hosts its snapshots (`src/blob.ts:51-56`).
- **`node_modules`**: `skills experimental_sync` crawls installed npm packages for SKILL.md at package root, `<pkg>/skills/`, or `<pkg>/.agents/skills/` and installs them project-scoped, so skills can ride along inside npm dependencies (`src/sync.ts:44-70`).
- **Search**: `skills find` hits `https://skills.sh/api/search` with an fzf-style interactive TUI; results ranked by install count (which comes from telemetry) (`src/find.ts:17, 86-116`).

Aliases exist for renamed sources (`SOURCE_ALIASES`, `src/source-parser.ts:145-148`).

## 5. Install lifecycle — the CRUD surface

Commands (`src/cli.ts:105-198`, `AGENTS.md:9-22`): `add` (aliases `a`, `install`, `i`), `use`, `list`/`ls`, `find`/`search`, `remove`/`rm`, `update`/`upgrade`/`check`, `init`, `experimental_install`, `experimental_sync`.

**`skills add <source>`** flow (`src/add.ts:1039+`):
1. Parse source → fetch (clone / blob / well-known / download / local).
2. Discover skills; interactive multiselect unless `--skill`/`--all`/`-y`.
3. Detect installed agents by probing config dirs (`~/.claude`, `~/.codex` or `$CODEX_HOME`, `~/.config/opencode`, …) — every agent has a `detectInstalled()` probe (`src/agents.ts:70-774`); prompt for target agents (last selection remembered in the global lock, `src/skill-lock.ts:58-59, 281-293`).
4. In parallel, fire a **security audit** lookup against `https://add-skill.vercel.sh/audit` — but only after GitHub has positively confirmed the repo is public; results (partner risk ratings, e.g. Socket-style alert counts) are rendered as a table before confirmation and never block install (`src/add.ts:1371-1378, 1700-1706`, `src/telemetry.ts:96-137`).
5. Materialize per agent (§6), write lockfile entries (§8), send install telemetry.
6. First-install nudge: offer to install the bundled **`find-skills`** meta-skill — a SKILL.md that teaches the agent itself to search skills.sh and run `npx skills add` on the user's behalf (`src/add.ts:2077-2125`, `skills/find-skills/SKILL.md`).

**Scope**: project (default, `./<agent>/skills/`) vs global (`-g`, `~/<agent>/skills/`); some agents are project-only (`globalSkillsDir: undefined`, e.g. Eve — `src/agents.ts:301-305`, `src/types.ts:94-95`).

**`skills use <source>@<skill>`** — ephemeral, install-free consumption: resolves the source the same way, writes the skill's files to a temp dir, and either prints a generated prompt to stdout ("You are being given a Skill to execute… `<SKILL.md>…</SKILL.md>` … Supporting files were downloaded to: <tempdir>") for piping into any agent, or launches `claude`/`codex` interactively with that prompt as argv (`src/use.ts:82-86, 138-179`). Only those two agents have launch configs.

**`skills remove`** — interactive or by name; `--agent`/`--skill` accept `'*'`; `--all` = `--skill '*' --agent '*' -y`; removes materialized dirs and lock entries (`src/remove.ts`, `README.md:211-250`).

**`skills update`** — see §8. **`skills experimental_install`** — restore a checked-out project from `skills-lock.json`, reinstalling each entry into the canonical universal dir only (`src/install.ts:18-98`).

## 6. Materialization model — canonical dir + symlink fan-out

This is the heart of the design (`src/installer.ts:265-421`):

- **Canonical copy**: every skill is copied once to `.agents/skills/<sanitized-name>/` (project) or `~/.agents/skills/<name>/` (global; the true global canonical for universal agents is `~/.config/agents/skills` via XDG — `src/agents.ts:766-773`). Constants: `AGENTS_DIR='.agents'`, `SKILLS_SUBDIR='skills'` (`src/constants.ts:1-3`).
- **Agent dirs get relative symlinks** to the canonical copy (junctions on Windows; falls back to full copy if symlink creation fails, e.g. no perms) (`src/installer.ts:197-263, 391-405`). `--copy` forces independent copies per agent.
- **"Universal" agents need no symlink at all**: any agent whose project `skillsDir` *is* `.agents/skills` reads the canonical location natively. That set now includes **Codex, Cursor, OpenCode, Amp, Cline, Gemini CLI, GitHub Copilot, Antigravity, Zed, Warp, Replit** and more (`src/agents.ts:210-218 (codex), 255-259 (cursor), 522-526 (opencode), 80-88 (amp)`; `isUniversalAgent()` at `src/agents.ts:861-863`). The README's supported-agents table shows the same convergence (`README.md:269-340`).
- **Non-universal agents** (Claude Code `.claude/skills/`, Windsurf, Roo, Goose, …) get the symlink. To avoid littering projects, project-level symlinks are **skipped for agents whose config root doesn't already exist in the project** — with a hard-coded exemption: `agentType !== 'claude-code'`, i.e. `.claude/skills/` symlinks are always created even if `.claude/` doesn't exist yet (`src/installer.ts:374-389`).
- **Per-agent adaptation is almost nil** — one real transform exists: **Eve** gets SKILL.md frontmatter rewritten to keep only `description`/`license`/string-metadata (dropping `name`), sometimes flattened to `<name>.md`, and supports per-subagent placement under `agent/subagents/<name>/skills` (`src/installer.ts:432-460, 516-530`, `src/agents.ts:795-818`). Everything else is byte-identical fan-out.
- Installs are destructive-idempotent: target dir is `rm -rf`'d and recreated on each install (`src/installer.ts:163-170`); `metadata.json`, `.git`, `__pycache__` excluded from copies (`src/installer.ts:423-424`).
- `skills list` scans canonical + every agent dir (including agents no longer detected), parses each SKILL.md, and reports which agents each skill is linked into (`src/installer.ts:1078-1312`); `--json` for machine-readable output.

## 7. Namespacing and identity

- **No org/skill namespacing on disk**: the install directory is just the sanitized skill name — two sources exporting `pr-review` collide, last-write-wins (canonical dir is cleaned per install, `src/installer.ts:359`). Provenance lives only in the lockfiles (`source: "owner/repo"`).
- Source identity for locks/telemetry is `owner/repo` (or `wellknown/<hostname>`, `mintlify/<domain>` from providers) (`src/source-parser.ts:11-65`, `src/providers/types.ts:26-27`).
- Skill selection syntax `owner/repo@skill-name` and ref pinning `source#ref` / `#ref@skill` give a de-facto addressing scheme `owner/repo[#ref][@skill]` (`src/source-parser.ts:204-241, 442-451`).
- **No dependencies between skills** — the model has no dependency field and no resolver.

## 8. Lockfiles, versioning, updates

Two lockfiles with different jobs:

- **Global**: `~/.agents/.skill-lock.json` (or `$XDG_STATE_HOME/skills/`), schema v3. Per skill: `source`, `sourceType`, `sourceUrl`, `ref?`, `skillPath?` (path of SKILL.md in the repo), `skillFolderHash` (**GitHub tree SHA of the skill's folder**), `installedAt`/`updatedAt`, `pluginName?`, `wellKnownDigest?` (`src/skill-lock.ts:13-60`). Older versions are wiped, not migrated (`src/skill-lock.ts:87-97`). Also stores UX state (dismissed prompts, last selected agents).
- **Project**: `./skills-lock.json`, schema v1, **meant to be committed**. Entries are deliberately minimal and timestamp-free, sorted alphabetically, with relative local-path sources — all explicitly to minimize git merge conflicts (`src/local-lock.ts:8-14, 53-60, 106-123`). Hash here is a locally computed SHA-256 over all files (path + content, sorted) rather than a GitHub tree SHA (`src/local-lock.ts:141-160`).

**Versioning model**: there are **no semver versions**. Pinning is by git ref (branch/tag) only; "is there an update" is answered by **content hash drift**: `skills update` re-fetches the GitHub tree (`/git/trees/<ref>?recursive=1`, main→master fallback, anonymous → env token → `gh api` → authenticated clone fallback) and compares the folder's tree SHA against the lock's `skillFolderHash`; mismatch ⇒ reinstall via the equivalent of `skills add <source-tree-url> -y` (`AGENTS.md:81-96`, `src/skill-lock.ts:151-172`, `src/update.ts`, `src/update-source.ts:102-149`). Update scope is global/project/both (`-g`/`-p`, interactive prompt otherwise).

**Reproducibility**: `experimental_install` restores a project from `skills-lock.json`, but into the canonical `.agents/skills/` only (not agent-specific dirs), re-resolving sources at their locked `ref`+`skillPath` (`src/install.ts:9-98`). It restores *latest at ref*, not the exact hashed content — the hash is a change detector, not a content address.

## 9. Notable extras

- **Telemetry**: query-string pings to `add-skill.vercel.sh/t` for install/remove/update/find/sync events, with CLI version, CI flag, and — notably — **which agent the CLI itself is running inside** (via `@vercel/detect-agent`, `src/detect-agent.ts`); repo/skill identifiers sent only for confirmed-public GitHub repos; opt-out `DISABLE_TELEMETRY`/`DO_NOT_TRACK` (`src/telemetry.ts`, `README.md:537-541`). Install counts feed the skills.sh leaderboard that `find` ranks by.
- **Security posture** is pervasive: path-traversal checks on every join (`isPathSafe`, `sanitizeSubpath`, `sanitizeName`), terminal-escape stripping of frontmatter before display, no-eval frontmatter parsing, download/extract size caps, refusal to read `gh auth token`, and the pre-install audit table (§5.4). Sync's closing line: "Review skills before use; they run with full agent permissions." (`src/sync.ts:450`).
- **Agent metadata is code-generated docs**: `scripts/sync-agents.ts` regenerates the README agent tables and package.json keywords from `src/agents.ts`, with `validate-agents.ts` as a check (`AGENTS.md:168-172`) — one source of truth for the 76-agent matrix.
- **`skills init`** + the agentskills.io spec + skills.sh browsing form the authoring loop; publishing is "push to GitHub" — there is no publish command.

## 10. Comparison with our recommended loomcli + fleet-db approach

Our prior doc (`2026-08-13-skills-crud-research.md`) recommends DB-stored skills materialized into agent worktrees as `.claude/skills/<name>/SKILL.md`, layered via the contract/repo pattern. vercel-labs/skills solves an adjacent problem (user-driven install into many agents on one machine, sources = git repos) rather than ours (fleet-driven materialization into worktrees, source = DB), but several of its choices transfer directly:

**Adopt:**

1. **Canonical-dir + fan-out materialization, and specifically `.agents/skills/` as the cross-agent answer.** This resolves our identified caveat ("codex/opencode won't read `.claude/skills/`") *without any per-agent format conversion*: Codex, OpenCode, Cursor, Amp, Cline, Gemini CLI, and Copilot all read project-level `.agents/skills/<name>/SKILL.md` natively (`src/agents.ts:210-218, 522-526, 255-259`), and Claude Code is covered by a `.claude/skills/<name>` → `../../.agents/skills/<name>` relative symlink (or plain copy). Materializing to `.agents/skills/` and symlinking `.claude/skills/` (vercel does the reverse-priority: canonical + link, with claude-code always linked even when `.claude/` is absent, `src/installer.ts:378-389`) gives us every backend for the cost of one extra symlink per skill. Symlinks must be **relative** to survive worktree paths (`src/installer.ts:251-258`).
2. **Minimal-diff, committed project lockfile discipline.** Their `skills-lock.json` design notes are directly applicable to any file we drop in worktrees or store per-stack: no timestamps, sorted keys, relative paths, per-skill entries that git auto-merges (`src/local-lock.ts:8-14, 106-123`). Likewise their content-hash-of-folder (sorted path+bytes SHA-256, `src/local-lock.ts:141-160`) is a good fit for fleet-db skill-version change detection and idempotent re-materialization (skip write if hash matches).
3. **Validate-loose, copy-verbatim.** They enforce only `name`+`description` as strings and pass all other frontmatter through untouched, warning-and-skipping instead of failing (`src/skills.ts:97-112`). For a DB CRUD surface that's the right contract too: schema-check the two required fields at write time, keep everything else opaque so Claude-Code-specific fields (`allowed-tools`, `context: fork`) survive round-trips. Also steal `sanitizeName()` (`src/installer.ts:50-65`) for deriving directory names from DB skill names.

**Avoid / diverge:**

- **Flat name-only namespace**: their install dirs collide across sources (last-write-wins after `rm -rf`). With multiple stacks/contracts feeding one worktree we should keep our namespaced identity (org/stack-qualified in the DB) even if the on-disk dir stays the bare name, and detect collisions at materialization instead of silently clobbering.
- **Destructive idempotency** (`cleanAndCreateDirectory` rm -rf per install, `src/installer.ts:163-170`) is fine for their CLI but risky inside active agent worktrees; prefer hash-compare-then-replace.
- **No pinning beyond ref + drift detection**: they wipe old lockfile versions rather than migrate, and "update" means "latest at ref". A DB gives us real versioning; don't import their weakest area.
- Their prompt-injection-style fallback for agents without a skills dir is simply *nothing* (only `skills use` generates a one-shot prompt wrapper, `src/use.ts:138-156`) — worth remembering that `use`-style prompt materialization is a viable shim for a backend that reads no skills directory at all.

---

### Appendix: agent → path mapping excerpt (from `src/agents.ts` / `README.md:269-340`)

| Agent | Project dir | Global dir |
|---|---|---|
| Claude Code | `.claude/skills/` | `~/.claude/skills/` (or `$CLAUDE_CONFIG_DIR/skills`) |
| Codex | `.agents/skills/` | `~/.codex/skills/` (or `$CODEX_HOME/skills`) |
| OpenCode | `.agents/skills/` | `~/.config/opencode/skills/` |
| Cursor | `.agents/skills/` | `~/.cursor/skills/` |
| Amp / Replit / Universal | `.agents/skills/` | `~/.config/agents/skills/` |
| Gemini CLI | `.agents/skills/` | `~/.gemini/skills/` |
| GitHub Copilot | `.agents/skills/` | `~/.copilot/skills/` |
| Windsurf | `.windsurf/skills/` | `~/.codeium/windsurf/skills/` |
| Eve | `agent/skills/` (+ per-subagent) | — (project-only) |
