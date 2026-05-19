# Catalog file ownership

Every YAML file in `catalog/` has **one owner agent** that may write to it. The contract is enforced by three soft layers:

1. **Prompt discipline** — each owner agent's `.md` prompt declares its single write target and a hard rule against touching siblings. This is the primary enforcement.
2. **Advisory hook** — `.claude/hooks/catalog-write-guard.sh` (registered as a `PreToolUse` hook on `Edit`/`Write`/`NotebookEdit`) reads `catalog/.ownership.yaml` and prints `[catalog-write-guard]` advisories naming the owner before every catalog write. Advisory only — never blocks. Any agent seeing an advisory with a name that isn't its own must abort.
3. **Post-hoc audit** — `catalog-curator` has an `Audit ownership` mode that reads `git log` and reports any catalog change made without the right owner running.

## Why no hard `deny` rules

The earlier design considered using `.claude/settings.local.json` to deny `Edit`/`Write` on catalog files in the main session. We dropped that approach: Claude Code permissions are session-scoped (subagents inherit the same rules), so denying `Edit(catalog/features.yaml)` would also block `feature-cataloger` from doing its job. The honest choice is soft enforcement everywhere with the curator auditing afterward.

If hard blocking becomes necessary later, the path forward is to change the hook's final `exit 0` to `exit 2` — that turns the advisory into a block.

## File → owner map

Canonical source: [`.ownership.yaml`](.ownership.yaml).

| File | Owner agent |
|------|-------------|
| `personas.yaml` | `persona-cataloger` |
| `features.yaml` | `feature-cataloger` |
| `stories.yaml`  | `user-story-cataloger` |
| `epics.yaml`    | `epic-cataloger` |
| `glossary.yaml` | `glossary-cataloger` |
| `index.yaml`    | `catalog-curator` |
| `.ownership.yaml` | `catalog-curator` |

## Curator exception

The `catalog-curator` is the only agent that may touch more than one file in a single run, and only for **deliberate cross-file migrations** (e.g. adding a new field across all stories). Every such override must be logged in the curator's final summary.

## Adding a new catalog file

1. Add a row to `.ownership.yaml`.
2. Create `~/.claude/agents/<name>-cataloger.md` with the file ownership contract block.
3. Run `catalog-curator` in **Register** mode to update `index.yaml`.

## Tightening enforcement

The hook is advisory by default. To make it block, change the final `exit 0` in `.claude/hooks/catalog-write-guard.sh` to `exit 2`. This will cause Claude Code to refuse any catalog write whose path is in `.ownership.yaml` — useful once the catalogers are stable and rogue writes become surprising rather than expected.
