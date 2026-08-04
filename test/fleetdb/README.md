# Empty FleetDB UI Stack

> **Status:** Current · *audited 2026-07-24*. Ports, env overrides, and the
> compose-provider auto-select checked against
> `test/fleetdb/docker-compose.empty.yml:75,119,140` and `Makefile:131-158`.

This stack models a new user setup: fleet-db starts with an empty Redis store,
Loom runs in fleet mode, the UI is served through Caddy, and a small daemon
manager starts a workspace-scoped `loom daemon` whenever a UI-created workspace
has runnable agents. No workspace, repo, issue, or regression fixture is seeded.

## Start

From the repo root:

```bash
npm --prefix internal/webui/frontend run build
docker compose -f test/fleetdb/docker-compose.empty.yml up --build
```

`podman-compose` works too, and `make fleetdb-empty-up` auto-selects Docker
Compose or Podman Compose based on what is installed.

Open `http://localhost:8091`.

The compose file expects a fleet-db image. By default it uses
`loomcli-fleetdb-regression-fleet-db:latest`, which the regression stack builds
locally. Override it with `FLEET_DB_IMAGE` when using a different tag.

## Create A Workspace From The UI

The Loom server runs inside a container, so repository paths entered in the UI
must be container paths. The compose file mounts `test/fleetdb/repos` to
`/repos` by default.

Example:

```bash
mkdir -p test/fleetdb/repos/demo
git -C test/fleetdb/repos/demo init
```

Then in the UI:

- Click `Create Workspace`
- Select `Empty`
- Add repository path `/repos/demo`
- Use a workspace location such as `/loom-config/workspaces/demo`

To use another host repo directory:

```bash
LOOM_REPOS_DIR=/path/to/repos docker compose -f test/fleetdb/docker-compose.empty.yml up --build
```

## Ports

- UI: `http://localhost:8091`
- Loom API: `http://localhost:8092`
- fleet-db: `http://localhost:8090`

Override them with `LOOM_UI_PORT`, `LOOM_API_PORT`, and `FLEET_DB_PORT`.
These defaults collide with `test/distributed/`'s (8090–8094) — do not run
both stacks at once.

## Reset

```bash
docker compose -f test/fleetdb/docker-compose.empty.yml down -v
```

`make fleetdb-empty-down` does the same and picks the compose provider for you
(`Makefile:146-158`). Use `podman-compose` instead of `docker compose` if that
is how you started the stack.

## Related

- [`../../docs/testing/README.md`](../../docs/testing/README.md) — index of all
  test surfaces
- [`../../docs/product/web-onboarding-spec.md`](../../docs/product/web-onboarding-spec.md)
  — the new-user flow this stack exists to exercise
- [`../local-mode/README.md`](../local-mode/README.md) — the seeded counterpart
  (workspace, repo, agents, and tasks already created)
- [`../distributed/README.md`](../distributed/README.md) — two-server fleet
  smoke; note the port overlap above
