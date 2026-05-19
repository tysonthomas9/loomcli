# loom catalog

A web of YAML files describing the loom project from multiple angles, designed so each focus area can be owned and maintained by one specialized AI agent.

## Files

| File | What it holds | Owner agent |
|---|---|---|
| `index.yaml` | Manifest — file list, owners, reference format | `catalog-curator` |
| `personas.yaml` | User archetypes | `persona-cataloger` |
| `features.yaml` | Every CLI command, view, API, integration | `feature-cataloger` |
| `stories.yaml` | Persona-driven user journeys | `user-story-cataloger` |
| `epics.yaml` | Strategic multi-quarter themes | `epic-cataloger` |
| `glossary.yaml` | Domain vocabulary | `glossary-cataloger` |
| `index.html` | Unified browser for all of the above | — |

## How references work

Cross-references between files use a stable `<file-id>:<item-id>` form.

Examples:

- `personas:solo-dev`
- `features:loom-plan`
- `stories:run-first-agent`
- `epics:lead-driven-orchestration`
- `glossary:fleet-db`

The renderer at `index.html` resolves these into clickable links. The `catalog-curator` agent validates that every reference points at a real item.

## How agents work together

Each agent owns one file and reads from the others.

```
glossary  ←──  personas  ←──┐
                            ├──  stories  ←──  epics
glossary  ←──  features  ←──┘
```

The dependency order (defined in `index.yaml`) is the safe order to refresh the catalog: glossary first, then personas + features in parallel, then stories, then epics.

The `catalog-curator` agent is the orchestrator. Use it for full refreshes and to find broken cross-references.

## Adding a new agent or file

1. Add an entry to `index.yaml` under `files:` with a unique `id`, the path, owner agent name, and the YAML key holding items.
2. Add the file → owner row to [`.ownership.yaml`](.ownership.yaml).
3. Create the YAML file with a `meta:` block listing `refs_in` and `refs_out`.
4. Create the agent definition in `~/.claude/agents/<agent-name>.md`.
5. Tell `catalog-curator` to incorporate the new file.

## File ownership

Each catalog file has exactly one owner agent that may write to it. The contract is enforced by a `PreToolUse` hook plus prompt discipline. Full details: [OWNERSHIP.md](OWNERSHIP.md).

## Renames and lifecycle

When an item's identity changes, use the alias mechanism — see [RENAMES.md](RENAMES.md).
When an item is deprecated or removed, use the lifecycle mechanism — see [LIFECYCLE.md](LIFECYCLE.md).

## Viewing

Run a local server from the repo root:

```
python3 -m http.server 8765
open http://localhost:8765/catalog/
```
