# deploy/ — Production deployment reference

After Phase 5, the loom API server and the static frontend build are
independent artifacts. This directory is the canonical production
reference: two Dockerfiles and a compose file that wire them together
the way a real deployment would.

```
deploy/
├── docker-compose.yml       # Same-origin compose (recommended)
├── server/
│   └── Dockerfile           # Pure Go API — no Node. Two targets, see below
└── frontend/
    ├── Dockerfile           # Static SPA + nginx — no Go
    └── nginx.conf           # Serves dist, proxies /api/*, SSE + WS upgrade
```

## Prerequisite: a fleet-db checkout

`loom serve` requires an issue backend. The compose file runs one (plus the
redis it needs) and builds it from a sibling checkout:

```bash
git clone https://github.com/BrowserOperator/fleet-db ../../fleet-db
```

Operators with a registry image can skip the clone and set
`DEPLOY_FLEETDB_IMAGE` instead. Either way an issue backend is not optional:
without `LOOM_FLEET_DB_URL` the server tries to start an embedded fleet-db
binary that is not in the image, and the container crash-loops.

## The two server image targets

`server/Dockerfile` builds two runtime images from one build stage:

| Target | Base | Use |
|---|---|---|
| `runtime` (default) | `gcr.io/distroless/static-debian12:nonroot` | Production. Minimal attack surface. **API-only.** |
| `runtime-terminal` | `debian:bookworm-slim` | The dev smoke stack (`docker-compose.dev.yml`). Has a shell. |

**The default production image is API-only: the Web UI terminal will not
work on it.** The terminal spawns a PTY running `$SHELL` (falling back to
`/bin/bash`) and distroless has no shell at all. Serving the terminal from a
container needs *both* the `runtime-terminal` target *and* a seeded local
workspace checkout inside the container — see the root `README.md` and
`scripts/compose-dev-seed.sh`. That is a deliberate tradeoff: the hardened
image is not downgraded to serve a dev-only feature.

For the same reason the `server` service in `docker-compose.yml` carries no
healthcheck — it has neither a shell nor `wget`, and loom exposes no self-probe
subcommand. Readiness is asserted on the **frontend** instead: `nginx:alpine`
does have `wget`, and `nginx.conf` already proxies `/health` through to the
server, so a passing frontend healthcheck transitively proves the API is
answering.

## Shape 1: Same-origin (recommended)

Frontend and API share an origin. nginx serves the SPA at `/` and
proxies `/api/*` (plus WebSocket upgrades and SSE streams) to the Go
server. No CORS is exercised.

```bash
cd deploy
docker compose up --build
# Open http://localhost:8080
```

Four services come up: `redis` → `fleet-db` → `server` → `frontend`. The
compose file publishes the frontend container's port 80 on the host as 8080
(matching the legacy single-port UX). The server, fleet-db and redis
containers are only reachable on the internal compose network.

> The compose file runs fleet-db with `--auth-dev-mode --authz-enabled=false`,
> which trusts the `X-Actor` header and disables authorization. **Replace this
> with real auth before any non-local deployment.**

## Shape 2: Cross-origin (CDN + API)

Frontend lives on a CDN or static host at one origin; the API server
runs on another. Build the frontend image with the API URL baked in and
run the server image with `--frontend-url` allowing the frontend origin.

```bash
# Build the frontend with a hard-coded API base URL.
docker build \
  -f deploy/frontend/Dockerfile \
  --build-arg VITE_API_BASE_URL=https://api.example.com \
  -t loom-frontend:cross-origin .

# Build and run the API server independently. The bare build produces the
# hardened `runtime` image — it is the last stage in the Dockerfile, so it is
# the default target.
docker build -f deploy/server/Dockerfile -t loom-server .
docker run --rm -p 8080:8080 \
  -e LOOM_ISSUE_BACKEND=fleetdb \
  -e LOOM_FLEET_DB_URL=https://fleet-db.example.com \
  loom-server \
  serve --port 8080 --bind 0.0.0.0 --frontend-url https://app.example.com
```

`--bind 0.0.0.0` is not optional in a container: the default `127.0.0.1`
binds the container's own loopback interface, so the published port accepts
connections that nothing is listening for. The image's default `CMD` already
passes it; the flag above is only needed because this `docker run` overrides
the `CMD`.

Deploy `loom-frontend:cross-origin` as a plain static container; point
your DNS at it. Deploy `loom-server` behind whatever TLS terminator you
prefer. The `--frontend-url` flag registers the frontend origin with the
server's CORS middleware.

## Kubernetes

Both images are standalone and have no shared filesystem requirements,
so they map naturally to two Deployments (one per service) behind an
Ingress. The Ingress should forward `/api/*` (with WebSocket upgrade
and SSE-friendly buffering disabled) to the server Service and
everything else to the frontend Service. See `nginx.conf` for the
nginx-flavored version of those rules — translate them to your
Ingress controller's annotations.

## Separate hosts / bare-metal

Run `loom serve --frontend-url https://yourhost` on the API host and
copy the `loomcli-frontend_<version>.tar.gz` release tarball's `dist/`
directory onto the frontend host's static root. Configure your reverse
proxy (nginx, caddy, HAProxy) using the rules in `nginx.conf` as a
starting point.
