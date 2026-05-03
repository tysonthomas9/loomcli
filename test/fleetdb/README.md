# Empty FleetDB UI Stack

This stack models a new user setup: fleet-db starts with an empty Redis store,
Loom runs in fleet mode, and the UI is served through Caddy. No workspace,
repo, issue, or regression fixture is seeded.

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
`localhost/loomcli-fleetdb-regression_fleet-db:latest`, which the regression stack builds
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

## Reset

```bash
docker compose -f test/fleetdb/docker-compose.empty.yml down -v
```

Use `podman-compose` instead of `docker compose` if that is how you started the
stack.
